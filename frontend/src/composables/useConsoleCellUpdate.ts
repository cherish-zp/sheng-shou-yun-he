// 控制台单元格更新三段式状态机:预览(后端生成 UPDATE 语句全文 + 匹配行数)
// → 确认弹窗 → 执行(不含同页刷新,刷新由调用方经 refreshStatementInPlace
// 完成,以便携带各自的执行上下文)。行为与既有 Mysql/Postgres 控制台逐字一致。
import { ref } from 'vue'

function toErrorMessage(e: unknown): string {
  return e instanceof Error ? e.message : String(e)
}

export interface ConsoleCellUpdateOptions<T> {
  // 预览:返回将执行的语句全文与匹配行数。
  preview(target: T): Promise<{ statement: string; matched_rows: number }>
  // 执行更新。
  execute(target: T): Promise<void>
}

export function useConsoleCellUpdate<T>(opts: ConsoleCellUpdateOptions<T>) {
  const previewing = ref(false)
  const error = ref<string | null>(null)
  const confirmOpen = ref(false)
  const statement = ref('')
  const matchedRows = ref(0)
  // 预览通过后暂存的待执行目标,确认时原样发给执行。
  let pending: T | null = null

  async function request(target: T): Promise<void> {
    pending = target
    previewing.value = true
    error.value = null
    try {
      const res = await opts.preview(target)
      statement.value = res.statement
      matchedRows.value = res.matched_rows
      confirmOpen.value = true
    } catch (e) {
      error.value = toErrorMessage(e)
      pending = null
    } finally {
      previewing.value = false
    }
  }

  // confirm 确认执行:成功 → 关闭弹窗并返回 true(调用方接着做同页刷新);
  // 失败 → 弹窗保持打开、错误复用错误展示区显示并返回 false;
  // 无待执行目标 → 写错误提示(与既有文案一致)并返回 false。
  async function confirm(): Promise<boolean> {
    if (!pending) {
      error.value = '没有待执行的单元格更新'
      return false
    }
    error.value = null
    try {
      await opts.execute(pending)
      confirmOpen.value = false
      return true
    } catch (e) {
      error.value = toErrorMessage(e)
      return false
    }
  }

  // 取消:关闭弹窗并清空全部状态(含待执行目标)。
  function cancel(): void {
    pending = null
    confirmOpen.value = false
    statement.value = ''
    matchedRows.value = 0
    error.value = null
  }

  return { previewing, error, confirmOpen, statement, matchedRows, request, confirm, cancel }
}
