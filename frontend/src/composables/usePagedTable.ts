// 表浏览器共用分页/过滤/排序状态:offset/PAGE_SIZE 分页、where 过滤草稿、
// orderBy 排序与翻页/重置动作;取数由调用方注入(refetch),组合式只管状态,
// 不发请求(行为与既有 Mysql/Postgres/CH/ES/Hive 浏览器逐字一致)。
import { computed, ref, type Ref } from 'vue'

export interface PagedTableOptions {
  // 与后端 PageRows 的 limit 对齐的每页行数。
  pageSize: number
  // 触发一次取数(通常为组件的 fetchPage)。
  refetch: () => unknown
}

export interface PagedTableState {
  // 过滤草稿:回车/按钮提交后才随请求下发(appliedWhere)。
  where: Ref<string>
  appliedWhere: Ref<string>
  orderBy: Ref<string>
  asc: Ref<boolean>
  offset: Ref<number>
  page: Ref<number>
  // applyWhere 提交过滤条件并回到第一页重新请求。
  applyWhere(): void
  refresh(): void
  // sortBy 点击列头排序:同一列翻转方向,新列默认升序;回第一页。
  sortBy(col: string): void
  nextPage(): void
  prevPage(): void
  // resetFilters 换表/外部切换时丢弃过滤/排序/分页状态(不触发取数)。
  resetFilters(): void
}

export function usePagedTable(opts: PagedTableOptions): PagedTableState {
  const where = ref('')
  const appliedWhere = ref('')
  const orderBy = ref('')
  const asc = ref(true)
  const offset = ref(0)
  const page = computed(() => Math.floor(offset.value / opts.pageSize) + 1)

  function applyWhere(): void {
    appliedWhere.value = where.value.trim()
    offset.value = 0
    void opts.refetch()
  }

  function refresh(): void {
    void opts.refetch()
  }

  function sortBy(col: string): void {
    if (orderBy.value === col) {
      asc.value = !asc.value
    } else {
      orderBy.value = col
      asc.value = true
    }
    offset.value = 0
    void opts.refetch()
  }

  function nextPage(): void {
    offset.value += opts.pageSize
    void opts.refetch()
  }

  function prevPage(): void {
    offset.value = Math.max(0, offset.value - opts.pageSize)
    void opts.refetch()
  }

  function resetFilters(): void {
    where.value = ''
    appliedWhere.value = ''
    orderBy.value = ''
    asc.value = true
    offset.value = 0
  }

  return {
    where,
    appliedWhere,
    orderBy,
    asc,
    offset,
    page,
    applyWhere,
    refresh,
    sortBy,
    nextPage,
    prevPage,
    resetFilters,
  }
}
