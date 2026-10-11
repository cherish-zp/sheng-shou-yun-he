// SQL 控制台服务端分页(默认 500 条/页):行结果聚合、精确/未知总数、
// 分页条可见性(仅只读脚本可重放翻页)与翻页动作。翻页 = lastRequest(上次
// 成功执行的请求)+ 新 offset 重新执行,不经过编辑器;行为与既有
// Mysql/Postgres/CH/Hive/ES 控制台逐字一致(ES 的「仅 SQL 模式」守卫经
// canReplay 注入)。
import { computed, shallowRef, ref, type ComputedRef, type Ref } from 'vue'

// 默认只读关键字集合(Hive 无 TABLE/VALUES 语句,经 readonlyKeywords 裁剪)。
const DEFAULT_READONLY_KEYWORDS = ['SELECT', 'SHOW', 'DESC', 'DESCRIBE', 'EXPLAIN', 'TABLE', 'VALUES']

// 取语句首个关键字:跳过前导空白与注释(-- 与 # 行注释、/* */ 块注释),
// 大小写不敏感;纯注释/空白/无法识别返回 null(保守视为非只读)。
function firstStatementKeyword(stmt: string): string | null {
  let i = 0
  const n = stmt.length
  for (;;) {
    while (i < n && /\s/.test(stmt[i] as string)) i += 1
    if (stmt.startsWith('--', i) || stmt[i] === '#') {
      const nl = stmt.indexOf('\n', i)
      i = nl === -1 ? n : nl + 1
      continue
    }
    if (stmt.startsWith('/*', i)) {
      const end = stmt.indexOf('*/', i + 2)
      i = end === -1 ? n : end + 2
      continue
    }
    break
  }
  const m = /^[A-Za-z_]+/.exec(stmt.slice(i, i + 64))
  return m ? m[0].toUpperCase() : null
}

// 脚本只读判定:全部语句只读 → true;空集或含任何非只读语句 → false。
export function scriptIsReadonly(statements: string[], readonlyKeywords: string[]): boolean {
  const set = new Set(readonlyKeywords)
  return (
    statements.length > 0 &&
    statements.every((s) => {
      const kw = firstStatementKeyword(s)
      return kw !== null && set.has(kw)
    })
  )
}

export interface ConsolePagingOptions<R, T extends { sql: string }> {
  // 语句结果流(含出错结果;行结果 = 无错误且 rows 是数组者)。
  results: Ref<R[]>
  // 执行中标志(翻页按钮禁用判定)。
  running: Ref<boolean>
  // 结果上的语句文本(PG 为 statement,其余源为 sql)。
  statementOf: (r: R) => string
  // 只读关键字集合(缺省 SELECT/SHOW/DESC/DESCRIBE/EXPLAIN/TABLE/VALUES)。
  readonlyKeywords?: string[]
  // 额外翻页守卫(ES:仅 SQL 模式可重放;同时作用于分页条可见性)。
  canReplay?: () => boolean
  // 重放:上次成功执行的请求 + 目标页(调用方闭包携带库/schema 等上下文)。
  replay: (req: T, targetPage: number) => void
}

export interface ConsolePagingState<R, T extends { sql: string }> {
  pageSize: number
  page: Ref<number>
  lastRequest: Ref<T | null>
  rowResults: ComputedRef<R[]>
  exactTotal: ComputedRef<number | null>
  allUnknownTotal: ComputedRef<boolean>
  pagerVisible: ComputedRef<boolean>
  lastPage: ComputedRef<number | null>
  pagerInfo: ComputedRef<string>
  canPrev: ComputedRef<boolean>
  canNext: ComputedRef<boolean>
  // commitRequest 新查询成功后记录 lastRequest(重放依据)并落到目标页。
  commitRequest(req: T, targetPage: number): void
  goToPage(target: number): void
  prevPage(): void
  nextPage(): void
}

export function useConsolePaging<R, T extends { sql: string }>(
  opts: ConsolePagingOptions<R, T>,
): ConsolePagingState<R, T> {
  const PAGE_SIZE = 500
  const page = ref(1)
  // lastRequest 仅整体读写(重放依据),shallowRef 避免泛型解包分歧。
  const lastRequest = shallowRef(null) as Ref<T | null>
  const readonlyKeywords = opts.readonlyKeywords ?? DEFAULT_READONLY_KEYWORDS

  // 行结果:无错误且 rows 是数组的结果,分页信息以行为准。
  const rowResults = computed(() =>
    opts.results.value.filter((r) => {
      const w = r as { error?: string; rows?: unknown }
      return !w.error && Array.isArray(w.rows)
    }),
  )

  // 精确总数:第一个行结果上 ≥0 的 total_rows(-1 是「无法计数」);null = 全部
  // 不可计数或未启用分页。
  const exactTotal = computed<number | null>(() => {
    for (const r of rowResults.value) {
      const total = (r as { total_rows?: number }).total_rows
      if (total != null && total >= 0) return total
    }
    return null
  })

  // 所有行结果都无法计数(total_rows === -1)→ 「至少 N 条」。
  const allUnknownTotal = computed(
    () =>
      rowResults.value.length > 0 &&
      rowResults.value.every((r) => (r as { total_rows?: number }).total_rows === -1),
  )

  // 分页条可见:有行结果,且本次执行的脚本整体只读(重放翻页安全)。
  const pagerVisible = computed(() => {
    if (opts.canReplay && !opts.canReplay()) return false
    return rowResults.value.length > 0 && scriptIsReadonly(opts.results.value.map(opts.statementOf), readonlyKeywords)
  })

  const lastPage = computed(() =>
    exactTotal.value == null ? null : Math.max(1, Math.ceil(exactTotal.value / PAGE_SIZE)),
  )

  // 页码信息:精确总数 → 共 N 条 · 第 p/last 页;全部 -1 → 至少 N 条 · 第 p 页;
  // 结果上没有 total_rows(未启用分页的响应)→ 第 x-y 条。
  const pagerInfo = computed(() => {
    const first = rowResults.value[0] as { rows?: (string | null)[] } | undefined
    const pageLen = first?.rows?.length ?? 0
    const offset = (page.value - 1) * PAGE_SIZE
    if (exactTotal.value != null) {
      return `共 ${exactTotal.value.toLocaleString('en-US')} 条 · 第 ${page.value}/${lastPage.value ?? 1} 页`
    }
    if (allUnknownTotal.value) {
      return `至少 ${(offset + pageLen).toLocaleString('en-US')} 条 · 第 ${page.value} 页`
    }
    if (pageLen === 0) return `第 ${page.value} 页`
    return `第 ${offset + 1}-${offset + pageLen} 条`
  })

  const canPrev = computed(() => !opts.running.value && page.value > 1)

  // 下一页:执行中禁用;有精确总数时按 lastPage 判定(恰好整页已取完不可再翻,
  // total=0 亦不可翻);总数未知(-1/缺省)沿用「本页行数 < PAGE_SIZE 则无更多」。
  const canNext = computed(() => {
    if (opts.running.value || rowResults.value.length === 0) return false
    if (exactTotal.value != null) {
      return lastPage.value != null && page.value < lastPage.value
    }
    return rowResults.value.some((r) => (((r as { rows?: (string | null)[] }).rows?.length ?? 0) >= PAGE_SIZE))
  })

  // commitRequest:新查询/翻页成功后记录 lastRequest 与当前页码。
  function commitRequest(req: T, targetPage: number): void {
    lastRequest.value = req
    page.value = targetPage
  }

  // 翻页 = 上次成功执行的请求 + 新 offset 重新执行。
  function goToPage(target: number): void {
    const req = lastRequest.value
    if (!req) return
    if (opts.canReplay && !opts.canReplay()) return
    opts.replay(req, target)
  }

  function prevPage(): void {
    if (canPrev.value) goToPage(page.value - 1)
  }

  function nextPage(): void {
    if (canNext.value) goToPage(page.value + 1)
  }

  return {
    pageSize: PAGE_SIZE,
    page,
    lastRequest,
    rowResults,
    exactTotal,
    allUnknownTotal,
    pagerVisible,
    lastPage,
    pagerInfo,
    canPrev,
    canNext,
    commitRequest,
    goToPage,
    prevPage,
    nextPage,
  }
}
