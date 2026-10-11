// 控制台结果删除行状态机(第一期单行):预览(后端生成 DELETE 语句全文 +
// 命中行数)→ 确认弹窗(命中 >1 定位不唯一,警示并禁用执行)→ 执行 →
// 同页刷新(refreshStatementInPlace)。行为与既有 Mysql/Postgres 控制台逐字
// 一致;来源定位与主键校验的方言差异(来源字段名、视图禁删)经 hint/buildTarget
// 注入,主键可定位校验与 where 构造为共享实现。
import { computed, ref, type Ref } from 'vue'
import type { ResultTabItem } from '@/components/common/SqlResultTabs.vue'
import { refreshStatementInPlace, type MarkedStatement } from './useConsoleStatementRefresh'

// 行来源定位/主键信息的最小形状(各源语句结果均满足)。
export interface DeletableRowResult {
  primary_key?: string[]
  columns?: { name: string }[]
  rows?: (string | null)[][] | null
}

// 单行删除的主键可定位校验(共享):主键为空 / 行不存在 / 主键值为 NULL
// 时给出禁用原因;返回 null = 可删。
export function pkLocateHint(r: DeletableRowResult, rowIndex: number): string | null {
  const pk = r.primary_key ?? []
  if (pk.length === 0) return '该表无主键,无法定位行'
  const row = r.rows?.[rowIndex]
  if (!row) return '行不存在'
  const cols = r.columns ?? []
  for (const name of pk) {
    const ci = cols.findIndex((c) => c.name === name)
    if (ci === -1 || row[ci] == null) return '主键值为 NULL,无法定位行'
  }
  return null
}

// 该行主键列原值(NULL → null)构造 where;调用前 hint 已保证主键值非 NULL。
export function buildPkWhere(
  r: DeletableRowResult,
  rowIndex: number,
): { column: string; value: string | null }[] {
  const row = r.rows?.[rowIndex] ?? []
  const cols = r.columns ?? []
  return (r.primary_key ?? []).map((name) => {
    const ci = cols.findIndex((c) => c.name === name)
    return { column: name, value: ci === -1 ? null : (row[ci] ?? null) }
  })
}

export interface ConsoleRowDeleteOptions<T, R extends { duration_ms: number; error?: string }> {
  results: Ref<R[]>
  running: Ref<boolean>
  markedStatements: Ref<MarkedStatement[]>
  resultTabs: Ref<ResultTabItem[]>
  // 结果上的语句原文(PG 为 statement,其余为 sql)。
  statementOf: (r: R) => string
  // 页面错误展示区落点(与运行错误共用)。
  errorSink(msg: string): void
  // 单行删除的禁用判定:返回 null = 可删;非空 = 原因文案。
  hint(r: R, rowIndex: number): string | null
  // 组装删除请求(来源定位为方言差异)。
  buildTarget(r: R, rowIndex: number): T
  // 预览:返回将执行的 DELETE 语句全文与命中行数。
  preview(target: T): Promise<{ statement: string; matched_rows: number }>
  // 执行删除。
  execute(target: T): Promise<void>
  // 同页重放某条语句(与原执行同页)。
  refetchStatement(sql: string): Promise<R[]>
}

export function useConsoleRowDelete<T, R extends { duration_ms: number; error?: string }>(
  opts: ConsoleRowDeleteOptions<T, R>,
) {
  const previewing = ref(false)
  const error = ref<string | null>(null)
  const confirmOpen = ref(false)
  const statement = ref('')
  const matchedRows = ref(0)
  // 预览通过后暂存的待执行目标与结果索引,确认时原样发给执行。
  let pending: T | null = null
  let pendingStmtIndex: number | null = null

  // request 点击行首删除:守卫 + 预览,成功才开确认弹窗;预览失败写入错误区,
  // 不弹框、不残留待执行目标。
  async function request(stmtIndex: number, rowIndex: number): Promise<void> {
    const r = opts.results.value[stmtIndex]
    if (!r || opts.running.value || previewing.value) return
    if (opts.hint(r, rowIndex) !== null) return
    const target = opts.buildTarget(r, rowIndex)
    pendingStmtIndex = stmtIndex
    pending = target
    previewing.value = true
    error.value = null
    try {
      const res = await opts.preview(target)
      statement.value = res.statement
      matchedRows.value = res.matched_rows
      confirmOpen.value = true
    } catch (e) {
      error.value = e instanceof Error ? e.message : String(e)
      pending = null
      pendingStmtIndex = null
    } finally {
      previewing.value = false
    }
  }

  // 确认弹窗文案:DELETE 全文 + 命中行数;命中 >1 = 定位不唯一,警示并禁用执行。
  const message = computed(() => {
    const n = matchedRows.value
    const base = `${statement.value}\n匹配 ${n} 行`
    return n > 1 ? `${base}\n注意:该条件命中 ${n} 行,定位不唯一,已禁止执行。` : base
  })

  async function confirm(): Promise<void> {
    if (matchedRows.value > 1) return // 定位不唯一:执行已被禁用,兜底拦截
    const idx = pendingStmtIndex
    pendingStmtIndex = null
    if (!pending) {
      error.value = '没有待执行的删除'
      return
    }
    error.value = null
    try {
      await opts.execute(pending)
      confirmOpen.value = false
      pending = null
    } catch (e) {
      error.value = e instanceof Error ? e.message : String(e)
      return // 失败:弹窗保持打开,错误复用错误展示区显示
    }
    const target = idx === null ? undefined : opts.results.value[idx]
    if (!target) return
    await refreshStatementInPlace({
      idx: idx as number,
      sql: opts.statementOf(target),
      refetch: () => opts.refetchStatement(opts.statementOf(target)),
      results: opts.results,
      markedStatements: opts.markedStatements,
      resultTabs: opts.resultTabs,
      onError: opts.errorSink,
    })
  }

  function cancel(): void {
    pendingStmtIndex = null
    pending = null
    confirmOpen.value = false
    statement.value = ''
    matchedRows.value = 0
    error.value = null
  }

  return { previewing, error, confirmOpen, statement, matchedRows, message, request, confirm, cancel }
}
