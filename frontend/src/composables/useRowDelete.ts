// 按行删除三段式状态机:行首危险按钮 → 预览(后端生成 DELETE 语句全文 +
// 命中行数)→ 确认弹窗 → 执行 + 同页刷新。状态每个实例独立;消费方按契约:
// await request(target) → 模板依据 confirmOpen 渲染 ConfirmDialog(message 展示
// DELETE 全文 + 命中行数 + 不可恢复警示,命中 >1 强警示并禁止执行)→
// confirm() / cancel()。文案与守卫与既有 Mysql/Postgres/CH 表浏览器逐字一致。
import { computed, ref } from 'vue'

export interface RowDeleteOptions<T> {
  // 预览:返回将执行的 DELETE 语句全文与命中行数。
  preview(req: T): Promise<{ statement: string; matched_rows: number }>
  // 执行删除。
  execute(req: T): Promise<void>
  // 执行成功后的同页刷新(通常为组件的 fetchPage)。
  refetch(): void | Promise<void>
}

export function useRowDelete<T>(opts: RowDeleteOptions<T>) {
  const error = ref<string | null>(null)
  const confirmOpen = ref(false)
  const statement = ref('')
  const matchedRows = ref(0)
  // 预览通过后暂存的待执行删除请求,确认时原样发给执行。
  let pending: T | null = null

  // request 点击行首删除:先预览拿语句全文与命中行数,成功才开确认弹窗;
  // 预览失败写入错误区,不弹框、不残留待执行目标。
  async function request(target: T): Promise<void> {
    error.value = null
    pending = target
    try {
      const res = await opts.preview(target)
      statement.value = res.statement
      matchedRows.value = res.matched_rows
      confirmOpen.value = true
    } catch (e) {
      error.value = e instanceof Error ? e.message : String(e)
      pending = null
    }
  }

  // 弹窗文案:DELETE 语句全文 + 命中行数 + 不可恢复警示;命中多行说明主键
  // 定位不唯一,强警示并禁止执行。
  const message = computed(() => {
    const n = Number(matchedRows.value ?? 0)
    const lines = [`将执行以下语句:\n${statement.value}`, `命中 ${n} 行`, '此操作不可恢复。']
    if (n > 1) lines.push(`主键条件命中 ${n} 行,删除范围超出目标行,已禁止删除。`)
    return lines.join('\n')
  })

  // confirm 确认执行:命中多行时直接禁止(弹窗保持,不执行);成功后关闭
  // 弹窗并刷新当前页;失败弹窗保持,错误同时进错误区。
  async function confirm(): Promise<void> {
    if (Number(matchedRows.value) > 1) return
    const target = pending
    if (!target) {
      confirmOpen.value = false
      return
    }
    try {
      await opts.execute(target)
      pending = null
      confirmOpen.value = false
      statement.value = ''
      matchedRows.value = 0
      void opts.refetch()
    } catch (e) {
      error.value = e instanceof Error ? e.message : String(e)
    }
  }

  // cancel 取消:关闭弹窗并清空全部删除状态。
  function cancel(): void {
    pending = null
    confirmOpen.value = false
    statement.value = ''
    matchedRows.value = 0
    error.value = null
  }

  return { error, confirmOpen, statement, matchedRows, message, request, confirm, cancel }
}
