<script setup lang="ts">
import { computed, onMounted, ref, unref, watch } from 'vue'
import type { SqlTableSchema, StatementMark } from '@/components/common/SqlEditor.vue'
import { splitSqlStatements } from '@/utils/sqlSplit'
import { EsDslParseError, parseEsDslRequests, prettyJson, type EsDslRequestPart } from '@/utils/esDsl'
import { useQueryFiles } from '@/composables/queryFiles'
import { useConsolePaging } from '@/composables/useConsolePaging'
import { useConsoleLayout } from '@/composables/useConsoleLayout'
import { useStatementMarks, useConsoleCursor, statementSummary, tabLabel, tabsFromSegments, applyTabStatus, marksFromResults } from '@/composables/useConsoleStatements'
import type { MarkedStatement } from '@/composables/useConsoleStatementRefresh'
import { useTabsStore } from '@/store/tabs'
import { useToastStore } from '@/store/toast'
import SqlEditor from '@/components/common/SqlEditor.vue'
import SqlResultCard from '@/components/common/SqlResultCard.vue'
import SqlResultTabs, { type ResultTabItem } from '@/components/common/SqlResultTabs.vue'
import PromptDialog from '@/components/common/PromptDialog.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import * as App from '../../../wailsjs/go/backend/App'

// tabId 必填:控制台据此把 tabs store 中自己的 tab 重命名为当前关联文件名。
const props = defineProps<{ tabId: string; connectionId: string }>()

// ESExecute / ListESIndices / ESDsl 绑定尚未由 wails generate module 生成,这里按
// 显式形状断言调用(req 为 snake_case 字段原样,Wails 按字段名序列化);生成
// 后签名一致,无需改动本文件。ES SQL 端点探测(6.3-6.8 /_xpack/sql、7.x/8.x
// /_sql、OpenSearch /_plugins/_sql)在后端完成,前端只管发 esExecute;服务端
// 版本过低时后端返回明确错误,经顶部错误条展示。ESDsl 透传任意 HTTP 响应
// (4xx/5xx 也 resolve {status, body},由结果卡展示),仅传输层错误 reject。
interface EsColumn {
  name: string
  type: string
  comment?: string
}
interface EsStatementResult {
  sql: string
  duration_ms: number
  error?: string
  columns?: EsColumn[]
  rows?: (string | null)[][]
  // 服务端分页结果:≥0 精确总数(本地翻译 select 取 hits.total);-1 无法计数
  // (SQL 端点路径与 count/show/describe 截断回退);缺省未启用。
  total_rows?: number
}
interface EsIndexInfo {
  name: string
  docs_count: number
  store_size_bytes: number
}

const app = App as unknown as {
  ESExecute(req: { connection_id: string; sql: string; limit?: number; offset?: number }): Promise<EsStatementResult[]>
  ListESIndices(id: string): Promise<EsIndexInfo[]>
  ESDsl(req: { connection_id: string; method: string; path: string; body: string }): Promise<{ status: number; body: string }>
}

const toast = useToastStore()

// --- 执行模式:SQL(默认,完整保留)与 DSL(Kibana Dev Tools 风格,所有版本
// 通用:服务端 6.1.0 OSS 无 X-Pack/SQL 端点,SQL 模式不可用时自动切 DSL)。 ---
type ConsoleMode = 'sql' | 'dsl'
const mode = ref<ConsoleMode>('sql')

// 模式按连接记忆:同一连接下次打开控制台沿用上次使用的模式,
// 避免在无 SQL 的服务端上每次都被默认的 SQL 模式绊倒。
const MODE_KEY = 'es-console-mode'

function loadMode(): ConsoleMode {
  return localStorage.getItem(`${MODE_KEY}:${props.connectionId}`) === 'dsl' ? 'dsl' : 'sql'
}

const DSL_TEMPLATE = 'GET /_cat/indices?format=json\n'

// 进入 DSL 模式:编辑器内容若解析不出任何 DSL 请求(典型是残留的 SQL
// 文本),替换为可直接执行的起始模板,避免点运行立刻「解析失败」。
function enterDslMode(): void {
  mode.value = 'dsl'
  markedStatements.value = []
  markedDsl.value = []
  try {
    parseEsDslRequests(sql.value)
  } catch {
    sql.value = DSL_TEMPLATE
    markedDsl.value = []
  }
}

function setMode(m: ConsoleMode): void {
  if (mode.value === m) return
  localStorage.setItem(`${MODE_KEY}:${props.connectionId}`, m)
  if (m === 'dsl') {
    enterDslMode()
    return
  }
  mode.value = m
  markedStatements.value = []
  markedDsl.value = []
}

mode.value = loadMode()

const sql = ref('')
const running = ref(false)
const error = ref<string | null>(null)
const results = ref<EsStatementResult[]>([])
const editor = ref<InstanceType<typeof SqlEditor> | null>(null)

// --- 结果 Tab 条:每个语句(请求)结果一个 tab,Tab 条下方只渲染 active 的那张卡。 ---
// SQL/DSL 各持一组 tab(结果本身也按模式分存);label = 语句起始上方最近的注释
// 文本(commentAbove),无注释回退「结果 N」;status 随运行推进 running → ok/fail;
// title 为语句单行摘要(DSL 为 METHOD /path)。
const sqlResultTabs = ref<ResultTabItem[]>([])
const sqlActive = ref(0)
const activeCard = computed(() => results.value[sqlActive.value])

// --- 结果区分页(服务端分页,默认 500 条/页,仅 SQL 模式) --------------------
// 新查询成功后记录 lastRequest(重放依据)并回到第 1 页;翻页 = lastRequest
// + 新 offset 重新执行,不经过编辑器(编辑器内容可能与重放脚本不同)。
// 状态与翻页动作由 useConsolePaging 组合式承担;「仅 SQL 模式可重放/可见」
// 经 canReplay 注入(DSL 模式不渲染分页条,翻页动作直接忽略)。
const {
  pageSize: PAGE_SIZE,
  page,
  rowResults,
  exactTotal,
  allUnknownTotal,
  pagerVisible,
  lastPage,
  pagerInfo,
  canPrev,
  canNext,
  commitRequest,
  prevPage,
  nextPage,
} = useConsolePaging<EsStatementResult, { sql: string }>({
  results,
  running,
  statementOf: (r) => r.sql,
  canReplay: () => mode.value === 'sql',
  replay: (req, targetPage) => {
    void executeScript(req.sql, targetPage)
  },
})

// (只读判定/分页可见性/页码信息/翻页动作均在 useConsolePaging 组合式内;
// DSL 模式下 pagerVisible 恒为 false,与既有行为一致。)
const dslResultTabs = ref<ResultTabItem[]>([])

const dslActive = ref(0)
const activeDslCard = computed(() => dslResults.value[dslActive.value])

// (语句摘要/tab 标签由 useConsoleStatements 共享模块提供;SQL/DSL 各持一组
// tab,tab 点击随模式写入各自的 active 下标。)
function onTabSelect(i: number): void {
  if (mode.value === 'sql') sqlActive.value = i
  else dslActive.value = i
}

// tab 标签:source 内语句起始 offset 上方最近的注释;无注释回退「结果 N」。

// --- 索引补全(仅索引名一层):挂载时拉一次索引清单;ES 无 DESCRIBE 惯例,
// 列级补全不做。失败静默降级为无补全,不打断编辑。 ---
const tables = ref<SqlTableSchema[]>([])

async function loadIndices(): Promise<void> {
  try {
    const list = await app.ListESIndices(props.connectionId)
    tables.value = list.map((i) => ({ name: i.name }))
  } catch {
    tables.value = []
  }
}

// --- 语句状态标记(useConsoleStatements 共享模块;DSL 标记单独维护) ---
const { markedStatements, statementMarks: sqlStatementMarks } = useStatementMarks({
  sql,
  split: splitSqlStatements,
})

// --- 光标位置与「执行当前语句」(useConsoleStatements 共享模块) ---
const { cursorPos, onCursor, statementAtCursor } = useConsoleCursor({ sql, split: splitSqlStatements })

// 服务端没有 SQL 端点(<6.3 / OSS 发行版)时,自动切换到 DSL 模式并播种
// 起始模板——SQL 模式在这种服务端上永远失败,不把用户留在必败路径上。
function maybeFallbackToDsl(msgs: Array<string | null | undefined>): void {
  if (mode.value !== 'sql') return
  if (!msgs.some((m) => m?.includes('无 SQL 能力'))) return
  toast.show('该服务端不支持 SQL,已切换到 DSL 模式')
  enterDslMode()
}

// 单语句执行:只发该段文本,结果数组替换为该条结果;发起即置 running 标记,
// 并预置单个 running SQL tab(标签优先取该语句的前置注释)。
// payload 不带 database 字段:ES 无库概念。
async function runSingle(text: string): Promise<void> {
  if (running.value) return
  if (!text.trim()) return
  running.value = true
  error.value = null
  resultsOpen.value = true
  markedStatements.value = [{ text, status: 'running' }]
  const seg = splitSqlStatements(sql.value).find((s) => s.text.trim() === text.trim())
  sqlResultTabs.value = [
    {
      label: seg ? tabLabel(sql.value, seg.from, 0) : '结果 1',
      status: 'running',
      title: statementSummary(text),
    },
  ]
  sqlActive.value = 0
  try {
    const res = await app.ESExecute({ connection_id: props.connectionId, sql: text, limit: PAGE_SIZE, offset: 0 })
    results.value = res
    commitRequest({ sql: text }, 1)
    // 选中文本可能含多条语句:结果多于预置 tab 时按序回退「结果 N」补齐。
    sqlResultTabs.value = res.map((r, i) => ({
      label: sqlResultTabs.value[i]?.label ?? `结果 ${i + 1}`,
      status: r.error ? 'fail' : 'ok',
      title: sqlResultTabs.value[i]?.title ?? statementSummary(r.sql),
    }))
    markedStatements.value = marksFromResults([text], res, (r) => r.sql)
    maybeFallbackToDsl(res.map((r) => r.error))
  } catch (e) {
    const msg = e instanceof Error ? e.message : String(e)
    error.value = msg
    markedStatements.value = [{ text, status: 'fail', detail: msg }]
    sqlResultTabs.value = sqlResultTabs.value.map((t) => ({ ...t, status: 'fail' }))
    maybeFallbackToDsl([msg])
  } finally {
    running.value = false
  }
}

// ⌘Enter:选区非空仍执行选中文本(沿用 getSelection 优先);否则执行光标所在语句。
async function runCurrentStatement(): Promise<void> {
  const selection = editor.value?.getSelection() ?? ''
  if (selection.trim() !== '') {
    await runSingle(selection)
    return
  }
  const text = statementAtCursor()
  if (text === null || !text.trim()) return
  await runSingle(text)
}

// 运行全部:整段脚本交给后端拆分逐条执行,标记按 split 段与返回结果顺序映射;
// SQL 结果 tab 同步推进(running → ok/fail),active 重置到第 0 个。
async function runAll(): Promise<void> {
  if (running.value) return
  if (mode.value !== 'sql') return
  const script = sql.value
  if (!script.trim()) return
  await executeScript(script, 1)
}

// 执行一段 SQL 脚本(新查询 targetPage=1;翻页 = 重放 lastRequest + 新 offset)。
// 成功后记录 lastRequest 与当前页码;失败保持原页码,便于重试。
async function executeScript(script: string, targetPage: number): Promise<void> {
  if (running.value) return
  running.value = true
  error.value = null
  resultsOpen.value = true
  const segs = splitSqlStatements(script)
  markedStatements.value = segs.map((s): MarkedStatement => ({ text: s.text, status: 'running' }))
  sqlResultTabs.value = tabsFromSegments(segs, script)
  sqlActive.value = 0
  try {
    const res = await app.ESExecute({
      connection_id: props.connectionId,
      sql: script,
      limit: PAGE_SIZE,
      offset: (targetPage - 1) * PAGE_SIZE,
    })
    results.value = res
    sqlResultTabs.value = applyTabStatus(sqlResultTabs.value, res)
    markedStatements.value = res.map((r, i): MarkedStatement => ({
      text: segs[i]?.text ?? r.sql,
      status: r.error ? 'fail' : 'ok',
      detail: r.error ?? `${r.duration_ms} ms`,
    }))
    maybeFallbackToDsl(res.map((r) => r.error))
    commitRequest({ sql: script }, targetPage)
  } catch (e) {
    const msg = e instanceof Error ? e.message : String(e)
    error.value = msg
    markedStatements.value = segs.map((s): MarkedStatement => ({ text: s.text, status: 'fail', detail: msg }))
    sqlResultTabs.value = sqlResultTabs.value.map((t) => ({ ...t, status: 'fail' }))
    maybeFallbackToDsl([msg])
  } finally {
    running.value = false
  }
}

// SqlEditor 主动请求执行某条语句(gutter ▶ / 编辑器内置快捷键)——SQL 模式走
// 单语句逻辑;DSL 模式把携带的文本按 DSL 解析后执行(同现有管道)。
function onRunStatement(text: string): void {
  if (mode.value === 'dsl') {
    void runDslText(text)
    return
  }
  void runSingle(text)
}

// 右键菜单「执行选中语句」:SQL 模式按整段选中文本执行(后端拆分多语句);
// DSL 模式把选中文本整体按 DSL 解析执行;空选区忽略。
function onRunSelection(text: string): void {
  if (!text.trim()) return
  if (mode.value === 'dsl') {
    void runDslText(text)
    return
  }
  void runSingle(text)
}

// 右键菜单「执行当前语句」:SQL = 光标所在语句;DSL = 光标所在请求。
function onRunMenuCurrent(): void {
  if (mode.value === 'dsl') void runDslCurrent()
  else void runCurrentStatement()
}

// --- DSL 模式:Kibana Dev Tools 风格请求执行 ----------------------------------
// `VERB /path` + 可选 JSON body,空行分隔多请求(esDsl.ts 解析);逐条透传后端
// ESDsl,任意 HTTP 响应(含 4xx/5xx)都渲染结果卡,仅传输层错误在请求卡内展示。

interface DslResultCard {
  statement: string // 请求原文(method 行 + body);解析错误卡 = 失败块原文
  method: string // 解析错误卡为 ''
  path: string
  status: number | null // HTTP 状态;解析错误/传输错误为 null
  durationMs: number
  body: string // 原始响应 body(复制按钮复制它)
  error: string | null
}

const dslResults = ref<DslResultCard[]>([])
const markedDsl = ref<MarkedStatement[]>([])

// 请求的规范化原文:用于结果卡展示与标记在编辑器内容变化后的重新定位。
function requestText(r: EsDslRequestPart): string {
  return r.body ? `${r.method} ${r.path}\n${r.body}` : `${r.method} ${r.path}`
}

// 1 基行号 → 该行首字符的文档 offset(供 DSL 标记的 from,startLine 定位)。
function lineStartOffset(text: string, line: number): number {
  const lines = text.split('\n')
  let offset = 0
  for (let i = 0; i < Math.min(line - 1, lines.length); i += 1) {
    offset += (lines[i] as string).length + 1
  }
  return offset
}

const dslStatementMarks = computed<StatementMark[]>(() => {
  if (mode.value !== 'dsl') return []
  let reqs: EsDslRequestPart[]
  try {
    reqs = parseEsDslRequests(sql.value)
  } catch {
    return []
  }
  const used = new Set<number>()
  const marks: StatementMark[] = []
  for (const m of markedDsl.value) {
    const idx = reqs.findIndex((r, i) => !used.has(i) && requestText(r) === m.text)
    if (idx === -1) continue
    used.add(idx)
    marks.push({ from: lineStartOffset(sql.value, reqs[idx].startLine), status: m.status, detail: m.detail })
  }
  return marks
})

// 传给编辑器的标记随模式切换:SQL 按语句段定位,DSL 按 startLine 定位。
const activeStatementMarks = computed<StatementMark[]>(() =>
  mode.value === 'sql' ? sqlStatementMarks.value : dslStatementMarks.value,
)

function dslOk(status: number | null): boolean {
  return status != null && status >= 200 && status < 300
}

function dslTone(c: DslResultCard): 'ok' | 'fail' {
  return c.error || !dslOk(c.status) ? 'fail' : 'ok'
}

function dslMeta(c: DslResultCard): string {
  return c.error ?? `HTTP ${c.status} · ${c.durationMs}ms`
}

function dslBodyText(c: DslResultCard): string {
  return c.method ? prettyJson(c.body) : c.statement
}

// 解析失败 → 单张错误卡(statement = 失败块原文,error 带行号)+ 对应失败 tab。
function showDslParseError(e: unknown): void {
  if (running.value) return
  resultsOpen.value = true
  dslResults.value = [
    {
      statement: e instanceof EsDslParseError ? e.block : sql.value,
      method: '',
      path: '',
      status: null,
      durationMs: 0,
      body: '',
      error: e instanceof Error ? e.message : String(e),
    },
  ]
  dslResultTabs.value = [
    {
      label: '解析失败',
      status: 'fail',
      title: statementSummary(dslResults.value[0]?.error ?? ''),
    },
  ]
  dslActive.value = 0
}

// 逐请求顺序执行:结果卡与标记随每个响应增量刷新;传输层错误不中断后续请求。
// source 为本次解析所用的原文(编辑器全文或选中文本),供 tab 前置注释标签定位。
async function runDslRequests(reqs: EsDslRequestPart[], source: string): Promise<void> {
  if (running.value || reqs.length === 0) return
  running.value = true
  error.value = null
  resultsOpen.value = true
  dslResults.value = []
  dslResultTabs.value = reqs.map((r, i) => ({
    label: tabLabel(source, lineStartOffset(source, r.startLine), i),
    status: 'running',
    title: `${r.method} ${r.path}`,
  }))
  dslActive.value = 0
  const marks = reqs.map((r): MarkedStatement => ({ text: requestText(r), status: 'running' }))
  markedDsl.value = [...marks]
  const cards: DslResultCard[] = []
  for (let i = 0; i < reqs.length; i += 1) {
    const r = reqs[i] as EsDslRequestPart
    const started = Date.now()
    try {
      const res = await app.ESDsl({
        connection_id: props.connectionId,
        method: r.method,
        path: r.path,
        body: r.body,
      })
      cards.push({
        statement: requestText(r),
        method: r.method,
        path: r.path,
        status: res.status,
        durationMs: Date.now() - started,
        body: res.body,
        error: null,
      })
      marks[i] = { text: requestText(r), status: dslOk(res.status) ? 'ok' : 'fail', detail: `HTTP ${res.status}` }
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e)
      cards.push({
        statement: requestText(r),
        method: r.method,
        path: r.path,
        status: null,
        durationMs: Date.now() - started,
        body: '',
        error: msg,
      })
      marks[i] = { text: requestText(r), status: 'fail', detail: msg }
    }
    markedDsl.value = [...marks]
    dslResults.value = [...cards]
    // 该请求的 tab 随响应脱离 running(HDR 响应 4xx/5xx 也算完成,状态为 fail)。
    const tab = dslResultTabs.value[i]
    if (tab) {
      dslResultTabs.value[i] = {
        ...tab,
        status: marks[i]?.status === 'ok' ? 'ok' : 'fail',
      }
    }
  }
  running.value = false
}

async function runDslText(text: string): Promise<void> {
  try {
    await runDslRequests(parseEsDslRequests(text), text)
  } catch (e) {
    showDslParseError(e)
  }
}

// 运行全部请求(按钮 / ⌘Shift+Enter)。
async function runDslAll(): Promise<void> {
  if (running.value) return
  if (!sql.value.trim()) return
  await runDslText(sql.value)
}

// 运行全部按钮随模式分发:SQL 整段脚本交后端拆分,DSL 前端解析后逐请求透传。
function onRunClick(): void {
  if (mode.value === 'dsl') void runDslAll()
  else void runAll()
}

// 光标行定位当前请求:startLine ≤ 光标行的最后一个请求(段间空白归属前一段);
// 光标在首个请求之前的注释/空白上时取第一个请求。
function dslRequestAtCursor(reqs: EsDslRequestPart[]): EsDslRequestPart | null {
  if (reqs.length === 0) return null
  let target: EsDslRequestPart | null = null
  for (const r of reqs) {
    if (r.startLine <= cursorPos.value.line) target = r
    else break
  }
  return target ?? reqs[0]
}

// ⌘Enter:选区非空 → 选中文本整体解析执行;否则执行光标所在请求。
async function runDslCurrent(): Promise<void> {
  const selection = editor.value?.getSelection() ?? ''
  if (selection.trim() !== '') {
    await runDslText(selection)
    return
  }
  try {
    const target = dslRequestAtCursor(parseEsDslRequests(sql.value))
    if (target) await runDslRequests([target], sql.value)
  } catch (e) {
    showDslParseError(e)
  }
}

// 复制原始响应 body(与 SqlResultCard 相同的剪贴板降级策略)。
async function copyDslBody(c: DslResultCard): Promise<void> {
  let ok = false
  try {
    if (navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(c.body)
      ok = true
    }
  } catch {
    ok = false
  }
  if (!ok) {
    const textarea = document.createElement('textarea')
    textarea.value = c.body
    textarea.setAttribute('readonly', '')
    textarea.style.position = 'fixed'
    textarea.style.opacity = '0'
    document.body.appendChild(textarea)
    textarea.select()
    try {
      ok = document.execCommand('copy')
    } catch {
      ok = false
    }
    textarea.remove()
  }
  if (ok) toast.show('已复制响应')
}

// --- IDE 式布局:可拖拽结果区(useConsoleLayout 组合式;高度持久化与
// 双击恢复语义不变) ---
const { resultsOpen, resultsHeight, resultsResizing, rootRef, startResultsResize, resetResultsHeight } =
  useConsoleLayout()

// --- 状态栏:光标行列 / 语句(请求)条数 / 最近耗时 ------------------------------------
const dslRequestCount = computed(() => {
  try {
    return parseEsDslRequests(sql.value).length
  } catch {
    return 0
  }
})
const statementCount = computed(() =>
  mode.value === 'sql' ? splitSqlStatements(sql.value).length : dslRequestCount.value,
)
const lastDurationMs = computed(() =>
  mode.value === 'sql'
    ? results.value.reduce((sum, r) => sum + (r.duration_ms ?? 0), 0)
    : dslResults.value.reduce((sum, r) => sum + r.durationMs, 0),
)

// --- 查询文件:保存/载入/删除由共享 composable 驱动,文件列表展示在全局右栏
// (Layout 层);本组件只负责编辑器侧的对话框与脏检查。ES 无库概念,
// getDatabase/setDatabase 不传(保存 payload 不带 database 字段)。 ---
// 快照:最近一次「载入/保存」完成时的编辑器内容,用于载入前的脏检查。
const savedSnapshot = ref('')

const qf = useQueryFiles({
  connectionId: () => props.connectionId,
  getContent: () => sql.value,
  // 文件内容回填编辑器的时刻 = 已保存状态,同步刷新快照。
  setContent: (s: string) => {
    sql.value = s
    savedSnapshot.value = s
  },
  // 同名覆盖保存时 currentFile 不变、下方 watcher 不触发,靠 onSaved 对齐
  // 快照,避免刚保存的内容被下一次载入误判为「未保存」。
  onSaved: () => {
    savedSnapshot.value = sql.value
  },
})

// composable 把 ref 嵌在普通对象里返回,模板不自动解包,这里统一取值。
const currentFile = computed(() => unref(qf.currentFile))
const nameDialogShow = computed(() => unref(qf.nameDialog.open))
const nameDialogMode = computed(() => unref(qf.nameDialog.mode))
const nameDialogTitle = computed(() => (nameDialogMode.value === 'save-as' ? '另存查询' : '保存查询'))
const overwriteShow = computed(() => unref(qf.overwriteConfirm.open))
const deleteConfirmShow = computed(() => unref(qf.deleteConfirm.open))
const deleteConfirmMessage = computed(() => unref(qf.deleteConfirm.message))

// 挂载恢复:本组件随 Layout 的 :key="active.id" 在切 tab 时销毁重建,编辑器
// 内容与文件关联持久化在所属 tab 的 draft 上;有草稿则在首次渲染前同步恢复
// (sql + 脏检查快照 + 文件关联,不读盘),标题由下方 renameTab watch 校准。
const mountedDraft = useTabsStore().openTabs.find((t) => t.id === props.tabId)?.draft
if (mountedDraft) {
  sql.value = mountedDraft.sql
  savedSnapshot.value = mountedDraft.sql
  qf.restoreFile(mountedDraft.file)
}

// 保存成功(currentFile 变化)也刷新快照,避免刚保存的内容被误判为脏。
watch(currentFile, () => {
  savedSnapshot.value = sql.value
})

// --- tab 标题跟随当前打开的 SQL 文件 -----------------------------------------
// 载入文件、⌘S 保存为新文件(关联变化)、删除/取消关联(currentFile 归空回退
// 默认标题「SQL 控制台」)都经 currentFile 变化驱动;immediate 保证挂载时校准。
const tabs = useTabsStore()
watch(
  currentFile,
  (f) => {
    tabs.renameTab(props.tabId, f ?? 'SQL 控制台')
  },
  { immediate: true },
)

// 编辑器内容或文件关联变化时写回 tab draft,供切 tab 销毁重建后恢复
// (见上方挂载恢复);draft 随 tab 对象存在,关闭 tab 自然丢弃。
watch([sql, currentFile], ([v, f]) => {
  tabs.setTabDraft(props.tabId, { sql: v, file: f })
})

// ⌘S / Ctrl+S 保存;⌘Enter / Ctrl+Enter 执行当前语句(DSL 模式 = 当前请求);
// ⌘Shift+Enter 运行全部(与 MySQL/CH SQL 控制台一致,忽略 IME 组合中的按键)。
// 事件从 CodeMirror contentDOM 冒泡到外层容器在此接住;编辑器自带的
// run-statement emit 也接入,双入口由 running 守卫去重。
function onEditorKeydown(e: KeyboardEvent): void {
  if (e.isComposing || e.keyCode === 229) return
  if (running.value) return
  if ((e.metaKey || e.ctrlKey) && (e.key === 's' || e.key === 'S')) {
    e.preventDefault()
    qf.requestSave()
    return
  }
  if (mode.value === 'dsl') {
    if ((e.metaKey || e.ctrlKey) && e.shiftKey && e.key === 'Enter') {
      e.preventDefault()
      void runDslAll()
      return
    }
    if ((e.metaKey || e.ctrlKey) && e.key === 'Enter') {
      e.preventDefault()
      void runDslCurrent()
    }
    return
  }
  if ((e.metaKey || e.ctrlKey) && e.shiftKey && e.key === 'Enter') {
    e.preventDefault()
    void runAll()
    return
  }
  if ((e.metaKey || e.ctrlKey) && e.key === 'Enter') {
    e.preventDefault()
    void runCurrentStatement()
  }
}

// --- 载入前的未保存确认(组件本地状态):右栏载入文件时,若编辑器有
// 未保存改动,先弹确认,避免无声覆盖用户正在编辑的 SQL。 ---
const loadConfirmShow = ref(false)
let pendingLoadName: string | null = null

function isDirty(): boolean {
  // 已关联文件:内容与快照不一致即脏;未关联:只要输入过非空白内容即脏。
  return currentFile.value ? sql.value !== savedSnapshot.value : sql.value.trim() !== ''
}

function requestLoadQueryFile(name: string): void {
  if (isDirty()) {
    pendingLoadName = name
    loadConfirmShow.value = true
    return
  }
  void qf.loadQueryFile(name)
}

function confirmLoad(): void {
  loadConfirmShow.value = false
  const name = pendingLoadName
  pendingLoadName = null
  if (name) void qf.loadQueryFile(name)
}

function cancelLoad(): void {
  loadConfirmShow.value = false
  pendingLoadName = null
}

// 暴露给全局右栏(Layout 层)调用的查询文件能力。
defineExpose({
  requestSave: () => {
    qf.requestSave()
  },
  requestSaveAs: () => {
    qf.requestSaveAs()
  },
  loadQueryFile: requestLoadQueryFile,
  askRemoveCurrentFile: () => {
    qf.askRemoveCurrentFile()
  },
  currentFile: (): string | null => currentFile.value,
})

onMounted(() => {
  void loadIndices()
  // 记忆的模式为 DSL 时,在此播种起始模板(此时 sql/marks 均已声明)。
  if (mode.value === 'dsl') enterDslMode()
})
</script>

<template>
  <div ref="rootRef" class="es-sql" data-test="es-sql-console">
    <div class="editor-pane">
      <div class="command-bar">
        <button class="btn primary" type="button" data-test="btn-es-run" :disabled="running || !sql.trim()" @click="onRunClick">
          {{ running ? '运行中…' : '运行全部' }}
        </button>
        <!-- SQL/DSL 分段切换:DSL(Kibana Dev Tools 风格)为所有 ES 版本通用的
             执行方式;SQL 模式保持默认且完整保留。 -->
        <div class="mode-switch">
          <button type="button" data-test="es-mode-sql" :class="{ active: mode === 'sql' }" @click="setMode('sql')">SQL</button>
          <button type="button" data-test="es-mode-dsl" :class="{ active: mode === 'dsl' }" @click="setMode('dsl')">DSL</button>
        </div>
        <span class="toolbar-hint">⌘Enter 执行当前语句 · ⌘Shift+Enter 运行全部 · ⌘S 保存</span>
      </div>
      <div class="editor-wrap" @keydown="onEditorKeydown">
        <SqlEditor
          ref="editor"
          v-model="sql"
          :tables="mode === 'sql' ? tables : []"
          height="100%"
          statement-gutter
          :statement-marks="activeStatementMarks"
          highlight-cursor-statement
          enable-run-menu
          data-test="es-sql-input"
          placeholder='SELECT * FROM "my-index" LIMIT 10'
          @run-statement="onRunStatement"
          @run-selection="onRunSelection"
          @run-current="onRunMenuCurrent"
          @run-all="onRunClick"
          @cursor="onCursor"
        />
      </div>

      <!-- 控制台级错误条:运行/保存链路抛错(如后端报告 ES 服务端版本过低、
           端点不可用)在此展示;语句级错误由 SqlResultCard 的错误卡渲染。 -->
      <div v-if="error" class="msg err" data-test="es-sql-error">{{ error }}</div>
    </div>

    <!-- 结果区:仅打开时渲染,高度可拖拽(160..窗口 80%),双击分隔条恢复 50/50。 -->
    <template v-if="resultsOpen">
      <div
        class="splitter"
        :class="{ active: resultsResizing }"
        data-test="console-splitter"
        title="拖拽调整结果区高度,双击恢复 50/50"
        @mousedown.prevent="startResultsResize"
        @dblclick="resetResultsHeight"
      ></div>
      <div class="results-pane" data-test="results-pane" :style="{ height: `${resultsHeight}px` }">
        <div class="results-head">
          <span class="results-title">结果</span>
          <button class="results-close" type="button" data-test="results-close" title="关闭结果区" @click="resultsOpen = false">×</button>
        </div>
        <div class="results-body" data-test="results-body">
          <!-- 结果 Tab 条:每条语句(请求)一个 tab,SQL/DSL 各持一组,点击切换下方唯一结果卡。 -->
          <SqlResultTabs
            :tabs="mode === 'sql' ? sqlResultTabs : dslResultTabs"
            :active="mode === 'sql' ? sqlActive : dslActive"
            @select="onTabSelect"
          />
          <!-- 分页条:SQL 模式服务端分页(500 条/页),翻页按上次成功请求重放;仅只读脚本可翻。 -->
          <div v-if="pagerVisible" class="result-pager" data-test="result-pager" title="跨页分页由数据库 ORDER BY 保证顺序;无排序查询顺序以数据库返回为准">
            <button type="button" class="pager-btn" data-test="pager-prev" :disabled="!canPrev" @click="prevPage">上一页</button>
            <span class="pager-info" data-test="pager-info">{{ pagerInfo }}</span>
            <button type="button" class="pager-btn" data-test="pager-next" :disabled="!canNext" @click="nextPage">下一页</button>
          </div>
          <div v-if="(mode === 'sql' ? results : dslResults).length === 0" class="empty" data-test="results-empty">
            {{ mode === 'dsl' ? 'GET /索引/_search ⏎ JSON body · ⌘Enter 执行当前请求' : '⌘Enter 执行当前语句 · ⌘Shift+Enter 运行全部' }}
          </div>
          <!-- SQL 模式:仅渲染 active tab 对应的只读结果卡(逻辑不变)。 -->
          <SqlResultCard
            v-else-if="mode === 'sql' && activeCard"
            :key="sqlActive"
            :statement="activeCard.sql"
            :duration-ms="activeCard.duration_ms"
            :columns="activeCard.columns ?? []"
            :rows="activeCard.rows ?? []"
            :total-rows="activeCard.total_rows ?? null"
            :insert-target="null"
            :export-name="`es-result-${sqlActive}`"
            :error="activeCard.error ?? null"
          />
          <!-- DSL 模式:仅渲染 active tab 对应的请求卡:头部 METHOD /path · HTTP 状态 ·
               耗时(2xx 绿 / 4xx 5xx 红),正文 pretty JSON 等宽滚动区,复制按钮复制原始
               body;解析错误渲染错误卡(statement=失败块原文)。 -->
          <div
            v-else-if="activeDslCard"
            :key="`dsl-${dslActive}`"
            class="dsl-card"
            :class="dslTone(activeDslCard)"
            data-test="es-dsl-card"
          >
            <div class="dsl-head">
              <span class="dsl-dot" :class="dslTone(activeDslCard)"></span>
              <span class="dsl-title" data-test="es-dsl-card-title">{{ activeDslCard.method ? `${activeDslCard.method} ${activeDslCard.path}` : '解析失败' }}</span>
              <span class="dsl-meta" data-test="es-dsl-card-meta">{{ dslMeta(activeDslCard) }}</span>
              <button v-if="activeDslCard.method" type="button" class="dsl-copy" data-test="es-dsl-copy" title="复制原始响应" @click="copyDslBody(activeDslCard)">
                复制
              </button>
            </div>
            <pre class="dsl-body" data-test="es-dsl-result">{{ dslBodyText(activeDslCard) }}</pre>
          </div>
        </div>
      </div>
    </template>

    <!-- 状态栏:光标行列 / 语句(请求)条数 / 最近耗时。 -->
    <div class="statusbar" data-test="console-statusbar">
      <span data-test="statusbar-cursor">行 {{ cursorPos.line }} : 列 {{ cursorPos.col }}</span>
      <span data-test="statusbar-statements">{{ mode === 'dsl' ? '请求' : '语句' }} {{ statementCount }} 条</span>
      <span data-test="statusbar-duration">最近耗时 {{ lastDurationMs }} ms</span>
    </div>

    <!-- 查询文件相关弹窗:名称输入(保存/另存为)、覆盖确认、删除确认由
         composable 的状态驱动;载入确认是本组件的本地状态。 -->
    <PromptDialog
      :show="nameDialogShow"
      :title="nameDialogTitle"
      label="文件名"
      confirm-text="保存"
      @confirm="qf.confirmName"
      @cancel="qf.nameDialog.cancel()"
    />
    <ConfirmDialog
      :show="overwriteShow"
      message="文件已存在,是否覆盖?"
      confirm-text="覆盖"
      :danger="false"
      @confirm="qf.overwriteConfirm.confirm()"
      @cancel="qf.overwriteConfirm.cancel()"
    />
    <ConfirmDialog
      :show="deleteConfirmShow"
      :message="deleteConfirmMessage"
      confirm-text="删除"
      danger
      @confirm="qf.deleteConfirm.confirm()"
      @cancel="qf.deleteConfirm.cancel()"
    />
    <ConfirmDialog
      :show="loadConfirmShow"
      message="当前 SQL 未保存,载入将替换?"
      confirm-text="载入"
      :danger="false"
      @confirm="confirmLoad"
      @cancel="cancelLoad"
    />
  </div>
</template>

<style scoped>
.es-sql {
  height: 100%;
  display: flex; flex-direction: column;
  padding: 14px 16px; box-sizing: border-box;
  color: var(--text); font-family: var(--font);
}

/* 编辑器区:占据结果区之外的剩余空间。 */
.editor-pane { flex: 1; min-height: 0; display: flex; flex-direction: column; }
.command-bar { flex: none; display: flex; align-items: center; gap: 10px; padding-bottom: 8px; }
.toolbar-hint { font-size: 12px; color: var(--text-tertiary); }
.editor-wrap { flex: 1; min-height: 0; display: flex; }
.editor-wrap :deep(.sql-editor) { flex: 1; min-width: 0; }
.msg { font-size: 13px; border-radius: 9px; padding: 8px 12px; }
.msg.err { background: var(--danger-soft); color: var(--danger); }
.editor-wrap + .msg { margin-top: 10px; }

/* 可拖拽分隔条与结果区。 */
.splitter { flex: none; height: 7px; margin: 4px -16px; cursor: row-resize; }
.splitter:hover, .splitter.active { background: var(--accent-soft); }
.results-pane {
  flex: none; display: flex; flex-direction: column;
  border: 1px solid var(--border); border-radius: 10px; background: var(--bg-elevated); overflow: hidden;
}
.results-head { display: flex; align-items: center; gap: 8px; padding: 5px 10px; border-bottom: 1px solid var(--border); }
.results-title { font-size: 12px; font-weight: 600; color: var(--text-secondary); }
.results-close {
  margin-left: auto; border: none; background: transparent; color: var(--text-secondary);
  font-size: 14px; line-height: 1; cursor: pointer; padding: 2px 7px; border-radius: 5px;
}
.results-close:hover { background: var(--bg-hover); color: var(--text); }
.results-body { flex: 1; min-height: 0; overflow: auto; padding: 10px; display: flex; flex-direction: column; gap: 12px; }
.empty { text-align: center; color: var(--text-tertiary); padding: 24px; font-size: 13px; }

/* 结果分页条:上一页 / 页码信息 / 下一页。 */
.result-pager { flex: none; display: flex; align-items: center; justify-content: center; gap: 12px; }
.pager-btn {
  border: 1px solid var(--border); background: var(--bg-subtle); color: var(--text);
  border-radius: 7px; padding: 3px 12px; font-size: 12px; cursor: pointer;
  transition: background 0.15s ease, border-color 0.15s ease;
}
.pager-btn:hover:not(:disabled) { background: var(--bg-hover); border-color: var(--accent); }
.pager-btn:disabled { opacity: 0.45; cursor: not-allowed; }
.pager-info { font-size: 12px; color: var(--text-secondary); font-family: var(--mono); }

/* 状态栏:光标行列 / 语句条数 / 最近耗时。 */
.statusbar {
  flex: none; display: flex; gap: 14px; margin-top: 8px;
  font-size: 11px; color: var(--text-tertiary); font-family: var(--mono);
}

.btn { border-radius: 7px; padding: 7px 14px; font-size: 13px; cursor: pointer; border: 1px solid transparent; transition: background 0.15s ease, opacity 0.15s ease; }
.btn:disabled { opacity: 0.5; cursor: not-allowed; }
.btn.primary { background: var(--accent); color: #fff; }
.btn.primary:hover:not(:disabled) { background: var(--accent-hover); }

/* SQL/DSL 分段按钮。 */
.mode-switch { display: flex; border: 1px solid var(--border); border-radius: 7px; overflow: hidden; }
.mode-switch button {
  border: none; background: transparent; color: var(--text-secondary);
  font-size: 12px; padding: 6px 12px; cursor: pointer; transition: background 0.15s ease, color 0.15s ease;
}
.mode-switch button + button { border-left: 1px solid var(--border); }
.mode-switch button:hover { color: var(--text); background: var(--bg-hover); }
.mode-switch button.active { background: var(--accent-soft); color: var(--accent); font-weight: 600; }

/* DSL 结果卡:头部状态行 + 等宽正文滚动区。 */
.dsl-card {
  border: 1px solid var(--border); border-radius: 8px; background: var(--bg-elevated); overflow: hidden;
}
.dsl-card.ok { border-color: var(--ok-soft); }
.dsl-card.fail { border-color: var(--danger-soft); }
.dsl-head { display: flex; align-items: center; gap: 8px; padding: 6px 10px; border-bottom: 1px solid var(--border); background: var(--bg-subtle); }
.dsl-dot { flex: none; width: 8px; height: 8px; border-radius: 50%; }
.dsl-dot.ok { background: var(--ok); }
.dsl-dot.fail { background: var(--danger); }
.dsl-title { font-family: var(--mono); font-size: 12px; color: var(--text); min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.dsl-meta { flex: none; margin-left: auto; font-size: 12px; font-family: var(--mono); color: var(--text-tertiary); white-space: nowrap; }
.dsl-copy {
  flex: none; border: 1px solid var(--border); border-radius: 6px; background: var(--bg-elevated);
  color: var(--text); font-size: 12px; padding: 2px 8px; cursor: pointer; transition: border-color 0.15s ease, color 0.15s ease;
}
.dsl-copy:hover { border-color: var(--accent); color: var(--accent); }
.dsl-body {
  margin: 0; padding: 10px 12px; overflow: auto; max-height: 320px;
  font-family: var(--mono); font-size: 12px; line-height: 1.55; color: var(--text);
  white-space: pre; user-select: text;
}
</style>
