// 库 → 表二级懒加载子树的通用组合式函数。
//
// ConnectionTree 里 ClickHouse / MySQL(TiDB) / Hive 三类数据源各自维护一套同构
// 的「连接展开 → 库清单 → 首次展开库节点懒加载表清单」二级树:5 个 Record ref
// (库清单/表清单/加载中/展开态/过滤词)+ key 拼接 + toggle/过滤/计数徽标/reload
// 函数群。本组合式把这套结构参数化为一个实例,树与树的差异收敛到两个回调:
// - listTables:如何拉取某库的表清单(CH 带 show_system:false 且接口必在;
//   MySQL/Hive 经可选链兜底空数组——由调用方在回调里各自表达);
// - onError:加载失败的落点(统一写父组件的 errorByConn,树头错误横幅)。
// 过滤词仍由父模板 v-model 直写 filterByDb(输入即时回显),过滤结果读下方
// 防抖副本,keyedComputed 按 key 缓存——与父组件其余搜索框共用同一套基建与
// 退化策略(测试构建 0ms 同步直通,生产 150ms 防抖,渲染输出不受影响)。
import { computed, onUnmounted, ref, watch, type ComputedRef, type Ref } from 'vue'
import { fuzzyFilter } from '@/utils/fuzzy'

// useDebouncedRef 返回跟随 source 的防抖只读副本:source 内任一键变化都会
// 重置同一计时器,到点整体快照一次;组件卸载时清理未触发的定时器。
// (自 ConnectionTree 迁出:三棵二级树与其余搜索框共用同一实现。)
export function useDebouncedRef<T extends Record<string, unknown>>(source: Ref<T>, delayMs: number): Readonly<Ref<T>> {
  const debounced = ref({ ...source.value }) as Ref<T>
  let timer: ReturnType<typeof setTimeout> | null = null
  watch(source, () => {
    if (delayMs <= 0) {
      debounced.value = { ...source.value }
      return
    }
    if (timer !== null) clearTimeout(timer)
    timer = setTimeout(() => {
      timer = null
      debounced.value = { ...source.value }
    }, delayMs)
  }, { deep: true })
  onUnmounted(() => {
    if (timer !== null) clearTimeout(timer)
  })
  return debounced
}

// keyedComputed 按 key 惰性创建并缓存 computed:过滤结果以「connId / db /
// schema 组合 key」为维度各缓存一份,同一 key 在同一次渲染里被 v-for、空态
// 与计数徽标多次读取时只求值一次,跨渲染依赖未变也直接复用,替代原先每次
// 读取都全量 map/filter/sort 的普通函数。
export function keyedComputed<T>(compute: (key: string) => T): (key: string) => T {
  const cache = new Map<string, ComputedRef<T>>()
  return (key: string) => {
    let entry = cache.get(key)
    if (!entry) {
      entry = computed(() => compute(key))
      cache.set(key, entry)
    }
    return entry.value
  }
}

export interface LazySubtreeOptions<T> {
  // 拉取某连接某库的表清单;抛错会经 onError 上报并结束该库的加载态。
  listTables: (connId: string, db: string) => Promise<T[]>
  // 表清单加载失败的落点(父组件统一写 errorByConn,树头显示错误横幅)。
  onError: (connId: string, message: string) => void
  // 过滤词防抖毫秒数;测试构建传 0 退化为同步直通(vitest 不推进真实定时器)。
  debounceMs: number
}

// useLazySubtree 维护一棵「库 → 表」二级懒加载子树的全部状态与行为。
// 键约定:`${connId}/${db}` 定位单个库节点(库清单按 connId 直存)。
export function useLazySubtree<T extends { name: string }>(options: LazySubtreeOptions<T>) {
  const dbs = ref<Record<string, string[]>>({})
  const tablesByDb = ref<Record<string, T[]>>({})
  const loadingByDb = ref<Record<string, boolean>>({})
  const expandedByDb = ref<Record<string, boolean>>({})
  // 每个库节点独立的表名模糊过滤词;由父模板 v-model 直写(输入即时回显)。
  const filterByDb = ref<Record<string, string>>({})

  // 过滤侧统一读防抖副本:击键停止后才触发一次「过滤 + 排序」。
  const debouncedFilterByDb = useDebouncedRef(filterByDb, options.debounceMs)

  function keyOf(connId: string, db: string): string {
    return `${connId}/${db}`
  }

  // dbsOf 未加载时返回 undefined:连接节点 toggle 侧以此判断是否需要首拉。
  function dbsOf(connId: string): string[] | undefined {
    return dbs.value[connId]
  }

  function setDbs(connId: string, list: string[]): void {
    dbs.value[connId] = list
  }

  function isDbExpanded(connId: string, db: string): boolean {
    return !!expandedByDb.value[keyOf(connId, db)]
  }

  // toggleDb 展开/收起库节点;首次展开懒加载表清单(系统库表是否过滤由
  // listTables 回调决定)。
  async function toggleDb(connId: string, db: string): Promise<void> {
    const key = keyOf(connId, db)
    expandedByDb.value[key] = !expandedByDb.value[key]
    if (!expandedByDb.value[key] || tablesByDb.value[key]) return
    loadingByDb.value[key] = true
    try {
      tablesByDb.value[key] = await options.listTables(connId, db)
    } catch (e) {
      options.onError(connId, e instanceof Error ? e.message : String(e))
    } finally {
      loadingByDb.value[key] = false
    }
  }

  function isLoading(connId: string, db: string): boolean {
    return !!loadingByDb.value[keyOf(connId, db)]
  }

  // isLoaded 判断表清单是否已拉取(含空数组,此时徽标显示「0」),供计数
  // 徽标的 v-if 使用;isEmpty 是空态文案的判据(未加载或加载为空都算空)。
  function isLoaded(connId: string, db: string): boolean {
    return !!tablesByDb.value[keyOf(connId, db)]
  }

  function isEmpty(connId: string, db: string): boolean {
    return (tablesByDb.value[keyOf(connId, db)] ?? []).length === 0
  }

  // filterOf 读防抖副本,与 filtered/countLabel 共用同一份生效过滤词。
  function filterOf(connId: string, db: string): string {
    return (debouncedFilterByDb.value[keyOf(connId, db)] ?? '').trim()
  }

  // filtered 对表名做本地模糊过滤(fuzzyFilter),按相关度排序。结果按库
  // key 缓存为 computed:v-for、空态与计数徽标共享同一份,不再重复排序。
  const filteredByKey = keyedComputed((key: string): T[] =>
    fuzzyFilter(tablesByDb.value[key] ?? [], debouncedFilterByDb.value[key] ?? '', (t) => t.name),
  )
  function filtered(connId: string, db: string): T[] {
    return filteredByKey(keyOf(connId, db))
  }

  // countLabel 库节点的表计数徽标:未过滤显示总数,过滤中显示「可见/总数」。
  function countLabel(connId: string, db: string): string {
    const total = (tablesByDb.value[keyOf(connId, db)] ?? []).length
    const q = filterOf(connId, db)
    if (!q) return String(total)
    return `${filtered(connId, db).length}/${total}`
  }

  // reload 清空某库的表清单缓存;库节点仍展开时立即重新拉取,让删除/截断
  // 后的树立即反映新状态(与 toggleDb 的懒加载逻辑一致)。
  async function reload(connId: string, db: string): Promise<void> {
    const key = keyOf(connId, db)
    delete tablesByDb.value[key]
    if (!isDbExpanded(connId, db)) return
    loadingByDb.value[key] = true
    try {
      tablesByDb.value[key] = await options.listTables(connId, db)
    } catch (e) {
      options.onError(connId, e instanceof Error ? e.message : String(e))
    } finally {
      loadingByDb.value[key] = false
    }
  }

  return { filterByDb, keyOf, dbsOf, setDbs, isDbExpanded, toggleDb, isLoading, isLoaded, isEmpty, filterOf, filtered, countLabel, reload }
}
