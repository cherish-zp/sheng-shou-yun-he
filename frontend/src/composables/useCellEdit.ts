// 数据网格行内编辑态:同一时刻仅一个单元格可编辑(editing 记录行/列下标),
// editValue 为输入框草稿,提交时空串按写 NULL 处理;进入编辑的守卫可由调用方
// 扩展(如无主键表只读、ES _id 首列只读),loading 与「已有单元格在编辑」
// 守卫内聚在此(行为与既有各表浏览器逐字一致)。
import { ref, type ComponentPublicInstance, type Ref } from 'vue'

export interface CellEditOptions {
  // loading 期间禁止进入编辑。
  loading: Ref<boolean>
  // 行数据内存:编辑草稿初值取自该行原值(null 显示为空输入框)。
  rows: Ref<(string | null)[][]>
  // 列定义:startEdit 校验目标列存在(防御 wire 形状异常)。
  columns: Ref<unknown[]>
  // 额外守卫(无主键表只读、_id 列只读等):返回 false 则忽略本次进入编辑。
  canStart?: (ri: number, ci: number) => boolean
}

export interface CellEditState {
  editing: Ref<{ row: number; col: number } | null>
  editValue: Ref<string>
  isEditing(ri: number, ci: number): boolean
  startEdit(ri: number, ci: number): void
  cancelEdit(): void
  // 取数开始时丢弃未完成的编辑态(行集合将重建)。
  discard(): void
  // 编辑框渲染后自动聚焦(function ref 挂载时触发)。
  focusEditor(el: Element | ComponentPublicInstance | null): void
}

export function useCellEdit(opts: CellEditOptions): CellEditState {
  const editing = ref<{ row: number; col: number } | null>(null)
  const editValue = ref('')

  function isEditing(ri: number, ci: number): boolean {
    return editing.value?.row === ri && editing.value?.col === ci
  }

  // startEdit 双击进入编辑:原始值取自 rows 内存(null 显示为空输入框)。
  function startEdit(ri: number, ci: number): void {
    if (opts.loading.value || editing.value || !opts.columns.value[ci]) return
    if (opts.canStart && !opts.canStart(ri, ci)) return
    editing.value = { row: ri, col: ci }
    editValue.value = opts.rows.value[ri]?.[ci] ?? ''
  }

  function cancelEdit(): void {
    editing.value = null
    editValue.value = ''
  }

  function discard(): void {
    editing.value = null
  }

  function focusEditor(el: Element | ComponentPublicInstance | null): void {
    if (el) (el as HTMLInputElement).focus()
  }

  return { editing, editValue, isEditing, startEdit, cancelEdit, discard, focusEditor }
}
