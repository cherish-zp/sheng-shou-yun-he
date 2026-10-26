<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'
import {
  EditorState,
  EditorSelection,
  Compartment,
  Range,
  RangeSet,
  RangeValue,
  StateEffect,
  type ChangeSpec,
  type Extension,
} from '@codemirror/state'
import {
  EditorView,
  keymap,
  lineNumbers,
  drawSelection,
  gutter,
  GutterMarker,
  ViewPlugin,
  Decoration,
  placeholder as cmPlaceholder,
  type DecorationSet,
  type ViewUpdate,
} from '@codemirror/view'
import { sql } from '@codemirror/lang-sql'
import {
  autocompletion,
  closeBrackets,
  closeBracketsKeymap,
  completionKeymap,
  completeFromList,
  ifNotIn,
  type CompletionSource,
} from '@codemirror/autocomplete'
import { defaultKeymap, history, historyKeymap } from '@codemirror/commands'
import {
  bracketMatching,
  indentOnInput,
  syntaxHighlighting,
  HighlightStyle,
} from '@codemirror/language'
import { tags as t } from '@lezer/highlight'
import { splitSqlStatements } from '@/utils/sqlSplit'
import { SQL_EXTRA_KEYWORDS, dedupeSqlKeywords } from '@/utils/sqlKeywords'

export interface SqlTableSchema {
  name: string
  columns?: string[]
}

// 语句执行状态标记:from 为语句起始 offset(与 splitSqlStatements 的 segment.from 对应),
// 内容变化后失配的标记会被忽略,由消费方重传。
export interface StatementMark {
  from: number
  status: 'ok' | 'fail' | 'running'
  detail?: string
}

// cursor 事件的 1 基行列。
export interface CursorPos {
  line: number
  col: number
}

// Ctrl+表名探测事件载荷:x/y 为鼠标 client 坐标,name 为 props.tables 中
// 命中的规范表名(大小写以表清单为准)。
export interface TableProbePayload {
  name: string
  x: number
  y: number
}

const props = withDefaults(
  defineProps<{
    modelValue: string
    tables?: SqlTableSchema[]
    placeholder?: string
    height?: string
    /** 行号槽渲染每条语句首行的 ▶ 执行按钮(点击 emit run-statement)。 */
    statementGutter?: boolean
    /** 语句执行状态标记,渲染在语句首行行号旁(✓/✗/转圈,悬浮 detail)。 */
    statementMarks?: StatementMark[]
    /** 光标所在语句整段背景微高亮。 */
    highlightCursorStatement?: boolean
    /** 右键执行菜单(执行选中语句 / 执行当前语句 / 运行全部),默认关闭。 */
    enableRunMenu?: boolean
    /** Ctrl+表名探测层(悬停预览建表语句),默认关闭,仅 MySQL 控制台开启。 */
    tableProbe?: boolean
  }>(),
  {
    height: '180px',
    tables: () => [],
    placeholder: undefined,
    statementGutter: false,
    statementMarks: () => [],
    highlightCursorStatement: false,
    enableRunMenu: false,
    tableProbe: false,
  },
)

const emit = defineEmits<{
  (e: 'update:modelValue', value: string): void
  (e: 'run-statement', text: string): void
  (e: 'cursor', pos: CursorPos): void
  /** 右键菜单「执行选中语句」:携带选中文本。 */
  (e: 'run-selection', text: string): void
  /** 右键菜单「执行当前语句」:定位由控制台按光标自行处理。 */
  (e: 'run-current'): void
  /** 右键菜单「运行全部」。 */
  (e: 'run-all'): void
  /** Ctrl 按住且鼠标在表名上悬停 300ms(防抖期内移动重置)。 */
  (e: 'table-hover', payload: TableProbePayload): void
  /** 鼠标离开编辑器 / 离开表名 / Ctrl 松开(仅存在活跃悬停时发出)。 */
  (e: 'table-leave'): void
  /** Ctrl+mousedown 命中表名(已 preventDefault,不扰动光标)。 */
  (e: 'table-pin', payload: TableProbePayload): void
  /** Ctrl+mousedown 落在非表名处(消费方据此收起固定卡)。 */
  (e: 'table-unpin'): void
}>()

const host = ref<HTMLElement | null>(null)
const rootEl = ref<HTMLElement | null>(null)
let view: EditorView | null = null

// schema / placeholder / 语句级扩展均用独立 compartment,支持运行期热替换。
const sqlCompartment = new Compartment()
const placeholderCompartment = new Compartment()
const runGutterCompartment = new Compartment()
const marksGutterCompartment = new Compartment()
const cursorDecoCompartment = new Compartment()
const probeCompartment = new Compartment()

function schemaOf(tables: SqlTableSchema[]): Record<string, string[]> {
  const out: Record<string, string[]> = {}
  for (const table of tables ?? []) out[table.name] = table.columns ?? []
  return out
}

function sqlExtension(tables: SqlTableSchema[]): Extension {
  // upperCaseKeywords:键入 as/select 等自动转大写。
  return sql({ schema: schemaOf(tables), upperCaseKeywords: true })
}

// lang-sql 默认方言(StandardSQL)的关键词补全不含 TRUNCATE/SHOW 等常用语句关键词
// (见 utils/sqlKeywords.ts),这里构建一个只补缺失词的额外补全源:标签 type 为
// keyword,经 completeFromList 大小写不敏感匹配、选中后整词大写应用,与
// upperCaseKeywords 行为一致;ifNotIn 与 lang-sql 内置关键词补全同款,字符串/
// 注释/点号后不触发。经 EditorState.languageData 全局 facet 注册,与 lang-sql
// 自带的表/列/关键词补全源并存(autocompletion 用 languageDataAt 收集),不做
// override、不改变现有补全行为。
function extraKeywordCompletionSource(): CompletionSource {
  const options = dedupeSqlKeywords(SQL_EXTRA_KEYWORDS).map((label) => ({
    label,
    type: 'keyword',
  }))
  return ifNotIn(
    ['QuotedIdentifier', 'String', 'LineComment', 'BlockComment', '.'],
    completeFromList(options),
  )
}

const extraKeywordCompletion = EditorState.languageData.of(() => [
  { autocomplete: extraKeywordCompletionSource() },
])

// 主题色全部走 CSS 变量,自动跟随 [data-theme] 深浅色/少女粉切换。
const editorTheme = EditorView.theme({
  '&': { height: '100%', fontSize: '13px', backgroundColor: 'transparent' },
  // 行高取整(13×1.5=19.5px 的亚像素舍入会在选区上留下逐行细缝)。
  '.cm-scroller': { fontFamily: 'var(--mono)', lineHeight: '20px', overflow: 'auto' },
  '.cm-content': { padding: '8px 10px 10px 0' },
  '&.cm-focused': { outline: 'none' },
  // 选区层提到行背景之上:当前语句底色(accent-soft)与光标行底色
  // (bg-hover,不透明)原先盖在选区矩形上,长语句选区被切成深浅
  // 相间的条纹;选区色本身低透明度,置顶不影响文字可读性。
  '.cm-selectionLayer': { zIndex: '1' },
  '.cm-gutters': { backgroundColor: 'var(--cm-gutter-bg)', border: 'none', color: 'var(--text-tertiary)' },
  '.cm-activeLine': { backgroundColor: 'var(--bg-hover)' },
  '.cm-activeLineGutter': { backgroundColor: 'var(--bg-hover)', color: 'var(--text-secondary)' },
  '.cm-placeholder': { color: 'var(--text-tertiary)' },
  '.cm-cursor, .cm-dropCursor': { borderLeftColor: 'var(--cm-cursor)' },
  '.cm-selectionBackground': { backgroundColor: 'var(--cm-selection)' },
  '.cm-tooltip': {
    backgroundColor: 'var(--bg-elevated)',
    border: '1px solid var(--border)',
    borderRadius: '8px',
    boxShadow: 'var(--shadow-md)',
  },
  '.cm-tooltip-autocomplete ul li[aria-selected]': {
    backgroundColor: 'var(--accent-soft)',
    color: 'var(--accent)',
  },
  // —— 语句 ▶ 运行按钮(主题色三角,hover 放大)——
  '.cm-run-statement': {
    color: 'var(--primary)',
    cursor: 'pointer',
    fontSize: '13px',
    lineHeight: '19px',
    background: 'none',
    border: 'none',
    padding: '0 4px',
    borderRadius: '4px',
    transition: 'transform .12s ease, background .12s ease',
  },
  '.cm-run-statement:hover': { background: 'var(--primary-weak)', transform: 'scale(1.2)' },
  '.cm-run-statement:active': { transform: 'scale(0.95)' },
  // —— 语句执行状态标记(行号旁 ✓/✗/⟳)——
  '.cm-statement-gutter .cm-gutterElement, .cm-marks-gutter .cm-gutterElement': {
    padding: '0 2px',
  },
  '.cm-statement-mark': { fontSize: '11px', padding: '0 2px', display: 'inline-block' },
  '.cm-statement-mark-ok': { color: 'var(--ok)' },
  '.cm-statement-mark-fail': { color: 'var(--danger)' },
  '.cm-statement-mark-running': {
    color: 'var(--info)',
    animation: 'cm-mark-spin 0.9s linear infinite',
  },
  // —— 光标所在语句微高亮 ——
  '.cm-current-statement': { backgroundColor: 'var(--accent-soft)' },
  // —— Ctrl+表名探测下划线(tableProbe 开启时) ——
  '.cm-table-probe': {
    textDecoration: 'underline dotted var(--primary)',
    cursor: 'pointer',
  },
})

// 语法高亮复用现有 --json-* / --text-* 变量,深浅色主题均已定义。
const sqlHighlightStyle = HighlightStyle.define([
  { tag: t.keyword, color: 'var(--json-boolean)' },
  { tag: t.string, color: 'var(--json-string)' },
  { tag: t.number, color: 'var(--json-number)' },
  { tag: t.comment, color: 'var(--text-tertiary)', fontStyle: 'italic' },
  { tag: [t.operator, t.punctuation, t.separator], color: 'var(--text-secondary)' },
  { tag: t.typeName, color: 'var(--json-key)' },
])

// —— gutter markers:▶ 运行按钮 ——

class RunStatementMarker extends GutterMarker {
  text: string
  constructor(text: string) {
    super()
    this.text = text
  }
  eq(other: RangeValue): boolean {
    return other instanceof RunStatementMarker && other.text === this.text
  }
  toDOM(): HTMLElement {
    const btn = document.createElement('button')
    btn.type = 'button'
    btn.className = 'cm-run-statement'
    btn.title = '执行该语句'
    btn.textContent = '▶'
    // mousedown 并 preventDefault:避免点击时编辑器失焦/选区丢失。
    btn.addEventListener('mousedown', (ev) => {
      ev.preventDefault()
      emit('run-statement', this.text)
    })
    return btn
  }
}

// ▶ 槽:每条语句一个按钮。CM 的 gutter 只收集锚在行首(from == line.from)的
// marker(见 @codemirror/view UpdateContext.line → advanceCursor),挂在行中
// seg.from 的 marker 会被静默丢弃;因此同行多条语句的 marker 统一锚定到所在
// 行行首,按段序逐个入 RangeSet(同位置多个 marker 均被收集、依序并排渲染),
// 各自携带段文本,不再互相去重——每个 ▶ 点击执行自己的语句。
function runGutterExtension(): Extension {
  return gutter({
    class: 'cm-statement-gutter',
    markers(view) {
      const doc = view.state.doc
      const ranges: Range<GutterMarker>[] = []
      for (const seg of splitSqlStatements(doc.toString())) {
        ranges.push(new RunStatementMarker(seg.text).range(doc.lineAt(seg.from).from))
      }
      return ranges.length ? RangeSet.of(ranges, true) : RangeSet.empty
    },
  })
}

// —— gutter markers:语句执行状态 ——

class StatementStatusMarker extends GutterMarker {
  status: StatementMark['status']
  detail: string | undefined
  constructor(mark: StatementMark) {
    super()
    this.status = mark.status
    this.detail = mark.detail
  }
  eq(other: RangeValue): boolean {
    return (
      other instanceof StatementStatusMarker &&
      other.status === this.status &&
      other.detail === this.detail
    )
  }
  toDOM(): HTMLElement {
    const el = document.createElement('span')
    el.className = `cm-statement-mark cm-statement-mark-${this.status}`
    if (this.detail) el.title = this.detail
    el.textContent = this.status === 'ok' ? '✓' : this.status === 'fail' ? '✗' : '⟳'
    return el
  }
}

// 状态槽:statementMarks 的 from 映射到语句首行(行号右侧);映射不到的忽略
// (内容变化后由消费方重传)。同一行多条语句时,后到的标记覆盖先前的。
function marksGutterExtension(marks: StatementMark[]): Extension {
  return gutter({
    class: 'cm-marks-gutter',
    markers(view) {
      const doc = view.state.doc
      const segByFrom = new Map(splitSqlStatements(doc.toString()).map((seg) => [seg.from, seg]))
      const markByLine = new Map<number, StatementMark>()
      for (const mark of marks) {
        const seg = segByFrom.get(mark.from)
        if (!seg) continue
        markByLine.set(doc.lineAt(seg.from).from, mark)
      }
      const ranges: Range<GutterMarker>[] = [...markByLine].map(([lineFrom, mark]) =>
        new StatementStatusMarker(mark).range(lineFrom),
      )
      return ranges.length ? RangeSet.of(ranges, true) : RangeSet.empty
    },
  })
}

// —— 光标所在语句微高亮 ——

function buildCursorDeco(view: EditorView): DecorationSet {
  const pos = view.state.selection.main.head
  const seg = splitSqlStatements(view.state.doc.toString()).find(
    (s) => s.from <= pos && pos <= s.to,
  )
  if (!seg) return Decoration.none
  return Decoration.set([
    Decoration.mark({ class: 'cm-current-statement' }).range(seg.from, seg.to),
  ])
}

const cursorStatementPlugin = ViewPlugin.fromClass(
  class {
    decorations: DecorationSet
    constructor(view: EditorView) {
      this.decorations = buildCursorDeco(view)
    }
    update(update: ViewUpdate) {
      if (update.docChanged || update.selectionSet) {
        this.decorations = buildCursorDeco(update.view)
      }
    }
  },
  { decorations: (v) => v.decorations },
)

// —— Ctrl+表名探测层(tableProbe,默认关闭;MySQL 控制台开启)——
//
// Ctrl(或 mac ⌘)按住时:文档中所有命中表清单的标识符加 cm-table-probe 点状
// 下划线装饰;悬停某表名 300ms emit table-hover,Ctrl+mousedown 命中表名 emit
// table-pin(阻断编辑器默认行为),落在非表名处 emit table-unpin。

// 探测快照:表集合(小写键 → 规范名)+ Ctrl 按住状态,经 effect 推给装饰插件
// 触发装饰重建(文档变化由插件按 docChanged 自行重建)。
interface ProbeSnapshot {
  tables: ReadonlyMap<string, string>
  ctrl: boolean
}

const probeSnapshotEffect = StateEffect.define<ProbeSnapshot>()

// 表名规范化:剥反引号,取 `库.` 前缀最后一段。
function normalizeTableName(raw: string): string {
  const stripped = raw.replace(/`/g, '').trim()
  const parts = stripped.split('.')
  return (parts[parts.length - 1] ?? '').trim()
}

function probeTableMap(tables: SqlTableSchema[]): ReadonlyMap<string, string> {
  const map = new Map<string, string>()
  for (const table of tables ?? []) {
    const name = normalizeTableName(table.name)
    if (name) map.set(name.toLowerCase(), name)
  }
  return map
}

// 扫描文档中的标识符(含反引号包裹形式),规范化后命中表集合 → 加探测装饰。
// matches 按出现序天然有序,Decoration.set(sorted) 直接收。
function buildProbeDeco(v: EditorView, snap: ProbeSnapshot): DecorationSet {
  if (!snap.ctrl || snap.tables.size === 0) return Decoration.none
  const text = v.state.doc.toString()
  const ranges: Range<Decoration>[] = []
  const tokenRe = /`[^`\n]*`|[\w$]+/g
  let m: RegExpExecArray | null
  while ((m = tokenRe.exec(text)) !== null) {
    const name = normalizeTableName(m[0])
    if (!name || !snap.tables.has(name.toLowerCase())) continue
    ranges.push(Decoration.mark({ class: 'cm-table-probe' }).range(m.index, m.index + m[0].length))
  }
  return ranges.length ? Decoration.set(ranges, true) : Decoration.none
}

// —— 探测层组件作用域状态(仅 tableProbe 开启时活动)——

const TABLE_HOVER_DEBOUNCE_MS = 300

let probeTables: ReadonlyMap<string, string> = new Map()
let probeCtrl = false
let probeHoverTimer: ReturnType<typeof setTimeout> | null = null
let probeHoverName: string | null = null

function dispatchProbeSnapshot(): void {
  view?.dispatch({ effects: probeSnapshotEffect.of({ tables: probeTables, ctrl: probeCtrl }) })
}

function clearProbeHoverTimer(): void {
  if (probeHoverTimer !== null) {
    clearTimeout(probeHoverTimer)
    probeHoverTimer = null
  }
}

// 结束悬停:存在活跃悬停才 emit table-leave(notify=false 仅静默清理,用于 pin)。
function endProbeHover(notify = true): void {
  clearProbeHoverTimer()
  if (probeHoverName !== null) {
    probeHoverName = null
    if (notify) emit('table-leave')
  }
}

function scheduleProbeHover(name: string, x: number, y: number): void {
  clearProbeHoverTimer()
  probeHoverTimer = setTimeout(() => {
    probeHoverTimer = null
    probeHoverName = name
    emit('table-hover', { name, x, y })
  }, TABLE_HOVER_DEBOUNCE_MS)
}

// 坐标 → 命中的规范表名;未命中(非表名词/无法定位)返回 null。
function probeTableNameAt(v: EditorView, x: number, y: number): string | null {
  if (probeTables.size === 0) return null
  const pos = v.posAtCoords({ x, y })
  if (pos == null) return null
  const word = v.state.wordAt(pos)
  if (!word) return null
  const lower = normalizeTableName(v.state.sliceDoc(word.from, word.to)).toLowerCase()
  return probeTables.get(lower) ?? null
}

function setProbeCtrl(down: boolean): void {
  if (down === probeCtrl) return
  probeCtrl = down
  dispatchProbeSnapshot()
  // Ctrl 松开:清悬停计时器并结束活跃悬停。
  if (!down) endProbeHover()
}

function onProbeWindowKeydown(e: KeyboardEvent): void {
  setProbeCtrl(e.ctrlKey || e.metaKey)
}

function onProbeWindowKeyup(e: KeyboardEvent): void {
  // 修饰键自身抬起后 ctrlKey/metaKey 均为 false;普通键抬起不影响按住状态。
  if (!e.ctrlKey && !e.metaKey) setProbeCtrl(false)
}

function onProbeWindowBlur(): void {
  setProbeCtrl(false)
}

function setProbeListeners(on: boolean): void {
  if (on) {
    window.addEventListener('keydown', onProbeWindowKeydown)
    window.addEventListener('keyup', onProbeWindowKeyup)
    window.addEventListener('blur', onProbeWindowBlur)
  } else {
    window.removeEventListener('keydown', onProbeWindowKeydown)
    window.removeEventListener('keyup', onProbeWindowKeyup)
    window.removeEventListener('blur', onProbeWindowBlur)
  }
}

function tableProbeExtension(): Extension {
  const probePlugin = ViewPlugin.fromClass(
    class {
      decorations: DecorationSet
      snap: ProbeSnapshot
      constructor(v: EditorView) {
        this.snap = { tables: probeTables, ctrl: probeCtrl }
        this.decorations = buildProbeDeco(v, this.snap)
      }
      update(u: ViewUpdate) {
        let dirty = u.docChanged
        for (const tr of u.transactions) {
          for (const e of tr.effects) {
            if (e.is(probeSnapshotEffect)) {
              this.snap = e.value
              dirty = true
            }
          }
        }
        if (dirty) this.decorations = buildProbeDeco(u.view, this.snap)
      }
    },
    { decorations: (v) => v.decorations },
  )
  return [
    probePlugin,
    EditorView.domEventHandlers({
      mousemove(ev, v) {
        if (!probeCtrl) return
        const name = probeTableNameAt(v, ev.clientX, ev.clientY)
        if (name === null) {
          endProbeHover()
          return
        }
        // 同一表名内移动不重发;换表/防抖期内移动 → 重置计时。
        if (name === probeHoverName) return
        scheduleProbeHover(name, ev.clientX, ev.clientY)
      },
      mouseleave() {
        endProbeHover()
      },
      mousedown(ev, v) {
        if (!probeCtrl) return
        const name = probeTableNameAt(v, ev.clientX, ev.clientY)
        if (name !== null) {
          // 阻断编辑器默认行为(Ctrl+点击不应扰动光标/选区)。
          ev.preventDefault()
          endProbeHover(false)
          emit('table-pin', { name, x: ev.clientX, y: ev.clientY })
          return true
        }
        // Ctrl+点击非表名处:通知消费方收起固定卡(hover 模式由消费方忽略)。
        emit('table-unpin')
        return
      },
    }),
  ]
}

// —— 更新监听:v-model 回写 + cursor 事件 ——

const updateListener = EditorView.updateListener.of((update) => {
  if (update.docChanged) emit('update:modelValue', update.state.doc.toString())
  if (update.docChanged || update.selectionSet) {
    const head = update.state.selection.main.head
    const line = update.state.doc.lineAt(head)
    emit('cursor', { line: line.number, col: head - line.from + 1 })
  }
})

// —— 复制当前行(Windows/Linux Ctrl+D,macOS ⌘D)——

// 标准编辑器 duplicate-line 行为:无选区时复制光标所在行;有选区时复制选区
// 触及的每一行(VSCode 语义);多选区一并处理,同一行只复制一次。单个
// transaction 提交(⌘Z 一步撤销);复制后每个光标/选区端点落到所在行副本
// 上的同列位置。CodeMirror 6 无内置该命令,此处自定义。
function duplicateLine(view: EditorView): boolean {
  const state = view.state
  const doc = state.doc
  // 1. 收集所有待复制行号(多选区/跨行选区去重,升序)。
  const lineNums = new Set<number>()
  for (const range of state.selection.ranges) {
    const from = doc.lineAt(range.from).number
    const to = doc.lineAt(range.to).number
    for (let n = from; n <= to; n++) lineNums.add(n)
  }
  if (lineNums.size === 0) return false
  const lines = [...lineNums].sort((a, b) => a - b).map((n) => doc.line(n))
  // 2. 每行行尾插入「\n + 该行内容」(末行原文无换行符时同样成立)。
  const changes: ChangeSpec[] = lines.map((line) => ({
    from: line.to,
    insert: `\n${line.text}`,
  }))
  // 3. 各行副本在新文档中的起始 offset:原行尾 + 先前所有副本长度 + 自身换行符。
  const copyStart = new Map<number, number>()
  let acc = 0
  for (const line of lines) {
    copyStart.set(line.number, line.to + acc + 1)
    acc += line.text.length + 1
  }
  // 4. 选区映射:端点落到所在行副本上的同列(所在行未被复制时原位,理论不可达)。
  const mapToCopy = (pos: number): number => {
    const line = doc.lineAt(pos)
    const start = copyStart.get(line.number)
    return start === undefined ? pos : start + (pos - line.from)
  }
  const selection = EditorSelection.create(
    state.selection.ranges.map((range) =>
      EditorSelection.range(mapToCopy(range.anchor), mapToCopy(range.head)),
    ),
    state.selection.mainIndex,
  )
  view.dispatch(state.update({ changes, selection, scrollIntoView: true }))
  return true
}

// —— 右键执行菜单(enableRunMenu)——

const menuEl = ref<HTMLElement | null>(null)
const menuVisible = ref(false)
// 菜单锚点:相对组件根元素的偏移(absolute 定位)。
const menuPos = ref({ x: 0, y: 0 })
const menuHasSelection = ref(false)
// 打开菜单瞬间捕获的选中文本(emit 用,无需响应式)。
let menuSelectionText = ''

function onContentContextmenu(ev: MouseEvent): void {
  if (!props.enableRunMenu) return
  ev.preventDefault()
  const sel = getSelection()
  menuSelectionText = sel
  menuHasSelection.value = sel.length > 0
  const rect = rootEl.value?.getBoundingClientRect()
  menuPos.value = { x: ev.clientX - (rect?.left ?? 0), y: ev.clientY - (rect?.top ?? 0) }
  menuVisible.value = true
}

function closeRunMenu(): void {
  menuVisible.value = false
}

function onRunSelection(): void {
  closeRunMenu()
  emit('run-selection', menuSelectionText)
}

function onRunCurrent(): void {
  closeRunMenu()
  emit('run-current')
}

function onRunAll(): void {
  closeRunMenu()
  emit('run-all')
}

// 点击菜单外关闭;菜单内部 mousedown 不关(mousedown 先于 click,提前关会吞掉菜单项点击)。
function onDocMouseDown(ev: MouseEvent): void {
  if (!menuVisible.value) return
  if (menuEl.value && ev.target instanceof Node && menuEl.value.contains(ev.target)) return
  closeRunMenu()
}

function onDocKeydown(ev: KeyboardEvent): void {
  if (menuVisible.value && ev.key === 'Escape') closeRunMenu()
}

onMounted(() => {
  if (!host.value) return
  // 探测层表集合初值(tables watcher 不在挂载时触发)。
  probeTables = probeTableMap(props.tables)
  const extensions: Extension[] = [
    // 允许多光标/多选区(默认会折叠为单光标),复制当前行等命令依赖它。
    EditorState.allowMultipleSelections.of(true),
    runGutterCompartment.of(props.statementGutter ? runGutterExtension() : []),
    lineNumbers(),
    marksGutterCompartment.of(
      props.statementMarks.length ? marksGutterExtension(props.statementMarks) : [],
    ),
    cursorDecoCompartment.of(props.highlightCursorStatement ? cursorStatementPlugin : []),
    probeCompartment.of(props.tableProbe ? tableProbeExtension() : []),
    drawSelection(),
    history(),
    indentOnInput(),
    bracketMatching(),
    autocompletion(),
    extraKeywordCompletion,
    closeBrackets(),
    keymap.of([
      // 复制当前行:CM 的 Mod-d 自动映射 mac ⌘D / 其它平台 Ctrl+D;
      // 置于最前,优先于 defaultKeymap 等既有绑定。
      { key: 'Mod-d', run: duplicateLine, preventDefault: true },
      ...closeBracketsKeymap,
      ...defaultKeymap,
      ...historyKeymap,
      ...completionKeymap,
    ]),
    sqlCompartment.of(sqlExtension(props.tables)),
    placeholderCompartment.of(props.placeholder ? cmPlaceholder(props.placeholder) : []),
    editorTheme,
    syntaxHighlighting(sqlHighlightStyle),
    updateListener,
  ]
  view = new EditorView({
    state: EditorState.create({ doc: props.modelValue, extensions }),
    parent: host.value,
  })
  // 右键执行菜单:拦截 contentDOM 的 contextmenu(enableRunMenu 关闭时不拦截,行为不变)。
  view.contentDOM.addEventListener('contextmenu', onContentContextmenu)
  document.addEventListener('mousedown', onDocMouseDown)
  document.addEventListener('keydown', onDocKeydown)
  if (props.tableProbe) setProbeListeners(true)
})

// tables 变化(如异步加载完元数据)→ 热替换 sql schema,并同步探测层表集合。
watch(
  () => props.tables,
  (tables) => {
    probeTables = probeTableMap(tables)
    if (props.tableProbe) dispatchProbeSnapshot()
    view?.dispatch({ effects: sqlCompartment.reconfigure(sqlExtension(tables)) })
  },
  { deep: true },
)

watch(
  () => props.placeholder,
  (p) => {
    view?.dispatch({ effects: placeholderCompartment.reconfigure(p ? cmPlaceholder(p) : []) })
  },
)

// 语句级 props 变化 → 重配三组扩展(▶ 槽 / 状态槽 / 光标高亮)。
watch(
  () => [props.statementGutter, props.statementMarks, props.highlightCursorStatement] as const,
  () => {
    view?.dispatch({
      effects: [
        runGutterCompartment.reconfigure(props.statementGutter ? runGutterExtension() : []),
        marksGutterCompartment.reconfigure(
          props.statementMarks.length ? marksGutterExtension(props.statementMarks) : [],
        ),
        cursorDecoCompartment.reconfigure(
          props.highlightCursorStatement ? cursorStatementPlugin : [],
        ),
      ],
    })
  },
  { deep: true },
)

// 外部值变化(且与内部文档不同)→ 全文替换;相同则跳过,避免 v-model 回环与光标跳动。
watch(
  () => props.modelValue,
  (value) => {
    if (!view || value === view.state.doc.toString()) return
    view.dispatch({ changes: { from: 0, to: view.state.doc.length, insert: value } })
  },
)

// 运行期关闭右键菜单 → 顺带收起已打开的菜单。
watch(
  () => props.enableRunMenu,
  (on) => {
    if (!on) closeRunMenu()
  },
)

// 探测层开关:热替换扩展;关闭时复位 Ctrl 状态并清理悬停计时。
watch(
  () => props.tableProbe,
  (on) => {
    if (on) {
      setProbeListeners(true)
    } else {
      setProbeListeners(false)
      probeCtrl = false
      endProbeHover()
    }
    view?.dispatch({
      effects: probeCompartment.reconfigure(on ? tableProbeExtension() : []),
    })
  },
)

onBeforeUnmount(() => {
  view?.contentDOM.removeEventListener('contextmenu', onContentContextmenu)
  document.removeEventListener('mousedown', onDocMouseDown)
  document.removeEventListener('keydown', onDocKeydown)
  if (props.tableProbe) setProbeListeners(false)
  clearProbeHoverTimer()
  view?.destroy()
  view = null
})

function focus(): void {
  view?.focus()
}

function getValue(): string {
  return view?.state.doc.toString() ?? props.modelValue
}

// 主光标选中的文本;无选区(光标)返回空串。供控制台「选中运行」读取选区。
function getSelection(): string {
  if (!view) return ''
  const range = view.state.selection.main
  return range.empty ? '' : view.state.sliceDoc(range.from, range.to)
}

defineExpose({ focus, getValue, getSelection })
</script>

<template>
  <div ref="rootEl" class="sql-editor" data-test="sql-editor">
    <div ref="host" class="editor-host" data-test="sql-editor-content" :style="{ height }"></div>
    <div
      v-if="menuVisible"
      ref="menuEl"
      class="run-menu"
      data-test="editor-run-menu"
      :style="{ left: `${menuPos.x}px`, top: `${menuPos.y}px` }"
      @contextmenu.prevent
    >
      <button
        v-if="menuHasSelection"
        type="button"
        class="run-menu-item"
        data-test="menu-run-selection"
        @click="onRunSelection"
      >
        执行选中语句
      </button>
      <button
        type="button"
        class="run-menu-item"
        data-test="menu-run-current"
        title="⌘Enter"
        @click="onRunCurrent"
      >
        执行当前语句
      </button>
      <button
        type="button"
        class="run-menu-item"
        data-test="menu-run-all"
        title="⌘⇧Enter"
        @click="onRunAll"
      >
        运行全部
      </button>
    </div>
  </div>
</template>

<style scoped>
/* 边框/圆角/聚焦描边与现有 .input 风格保持一致;relative 供右键菜单 absolute 锚定 */
.sql-editor {
  position: relative;
  border: 1px solid var(--border);
  border-radius: 7px;
  background: var(--editor-bg);
  overflow: hidden;
  transition: border-color 0.15s ease, background 0.15s ease, box-shadow 0.15s ease;
}
.sql-editor:focus-within {
  outline: none;
  border-color: var(--accent);
  background: var(--bg-elevated);
  box-shadow: var(--focus-ring);
}
.editor-host :deep(.cm-editor) { height: 100%; }
/* 右键执行菜单:自绘,主题变量跟随深浅色 */
.run-menu {
  position: absolute;
  z-index: 30;
  min-width: 148px;
  padding: 5px;
  display: flex;
  flex-direction: column;
  gap: 1px;
  background: var(--bg-elevated);
  border: 1px solid var(--border);
  border-radius: 10px;
  box-shadow: var(--shadow-md);
}
.run-menu-item {
  text-align: left;
  background: none;
  border: none;
  color: var(--text);
  font-size: 13px;
  font-family: var(--font);
  padding: 6px 10px;
  border-radius: 6px;
  cursor: pointer;
  white-space: nowrap;
  transition: background 0.12s ease;
}
.run-menu-item:hover { background: var(--bg-hover); }
.run-menu-item:focus-visible { outline: none; box-shadow: 0 0 0 2px var(--accent); }
</style>

<style>
/* running 状态标记的旋转动画(@keyframes 无法放进 EditorView.theme) */
@keyframes cm-mark-spin {
  to { transform: rotate(360deg); }
}
</style>
