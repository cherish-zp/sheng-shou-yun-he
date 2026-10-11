// SQL 控制台 IDE 式布局:可拖拽结果区(结果区仅打开时渲染;高度
// 160..窗口 80% 可拖拽,双击分隔条恢复 50/50,持久化到 localStorage,
// 各控制台共用同一 key)。行为与既有 Mysql/Postgres/CH/Hive/ES 控制台
// 逐字一致;rootRef 由消费方绑定到根节点(双击恢复按容器高折半)。
import { ref, type Ref } from 'vue'

const RESULTS_HEIGHT_KEY = 'dbclient-sql-results-height'
const MIN_RESULTS_HEIGHT = 160
const DEFAULT_RESULTS_HEIGHT = 320

function maxResultsHeight(): number {
  return Math.max(MIN_RESULTS_HEIGHT, Math.floor(window.innerHeight * 0.8))
}

function clampResultsHeight(h: number): number {
  return Math.min(maxResultsHeight(), Math.max(MIN_RESULTS_HEIGHT, h))
}

function readStoredResultsHeight(): number {
  const v = Number(localStorage.getItem(RESULTS_HEIGHT_KEY))
  if (!Number.isFinite(v) || v <= 0) return DEFAULT_RESULTS_HEIGHT
  return clampResultsHeight(Math.round(v))
}

export interface ConsoleLayoutState {
  // 结果区开合与高度(高度持久化在拖拽结束/双击恢复时写回)。
  resultsOpen: Ref<boolean>
  resultsHeight: Ref<number>
  resultsResizing: Ref<boolean>
  rootRef: Ref<HTMLElement | null>
  startResultsResize(e: MouseEvent): void
  resetResultsHeight(): void
}

export function useConsoleLayout(): ConsoleLayoutState {
  const resultsOpen = ref(false)
  const rootRef = ref<HTMLElement | null>(null)
  const resultsHeight = ref(readStoredResultsHeight())
  const resultsResizing = ref(false)
  let resultsDragStartY = 0
  let resultsDragStartHeight = 0

  function onResultsResize(e: MouseEvent): void {
    // 结果区在下方:向上拖(dy 为负)增高。
    resultsHeight.value = clampResultsHeight(resultsDragStartHeight - (e.clientY - resultsDragStartY))
  }

  function endResultsResize(): void {
    resultsResizing.value = false
    localStorage.setItem(RESULTS_HEIGHT_KEY, String(resultsHeight.value))
    window.removeEventListener('mousemove', onResultsResize)
    window.removeEventListener('mouseup', endResultsResize)
  }

  function startResultsResize(e: MouseEvent): void {
    resultsResizing.value = true
    resultsDragStartY = e.clientY
    resultsDragStartHeight = resultsHeight.value
    window.addEventListener('mousemove', onResultsResize)
    window.addEventListener('mouseup', endResultsResize)
  }

  // 双击分隔条:恢复上下 50/50(容器无布局高度时按窗口高折半)。
  function resetResultsHeight(): void {
    const total = rootRef.value?.clientHeight || window.innerHeight
    resultsHeight.value = clampResultsHeight(Math.round(total / 2))
    localStorage.setItem(RESULTS_HEIGHT_KEY, String(resultsHeight.value))
  }

  return {
    resultsOpen,
    resultsHeight,
    resultsResizing,
    rootRef,
    startResultsResize,
    resetResultsHeight,
  }
}
