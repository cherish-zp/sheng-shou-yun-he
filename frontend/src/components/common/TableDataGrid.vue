<script setup lang="ts">
import { computed, type ComponentPublicInstance } from 'vue'

// 表数据网格:六种数据源浏览器(MySQL/Postgres/CH/ES/Hive)共用的表格渲染
// (列头排序/主键角标/字段注释、行首删除按钮、NULL/截断/只读样式、行内编辑
// 输入框与空态)。方言差异用 props/插槽表达:
// - Hive 只读:sortable=false、不传编辑态与行删除;
// - ES 文档流:leadingColumn + #leading-cell 插槽渲染 _id 列(JSON/删除按钮),
//   editorTag=textarea,首列不可排序(nosortFirstColumn)、空态经 #empty 插槽;
// - PG 无障碍:keyboardSortable(th 键盘排序 + aria-sort)、keyboardEditable
//   (单元格键盘进入编辑)。
// data-test 契约沿用各源既有命名:由 testPrefix 拼接({prefix}-grid/{prefix}-col/
// {prefix}-cell/{prefix}-cell-editor/…),行删除按钮固定为 btn-row-delete。
export interface TableGridColumn {
  name: string
  type: string
  comment?: string
  // 后端 wire 形状沿用 is_in_primary_key(Mysql/PG 列定义原样透传)。
  is_in_primary_key?: boolean
}

const props = withDefaults(
  defineProps<{
    testPrefix: string
    columns: TableGridColumn[]
    rows: (string | null)[][]
    loading: boolean
    orderBy?: string
    asc?: boolean
    // 列头排序(指针 + 悬停反馈);Hive 只读浏览器为 false。
    sortable?: boolean
    // PG:th 可聚焦,Enter/Space 触发排序并带 aria-sort。
    keyboardSortable?: boolean
    // 主键角标(Mysql/PG)。
    showPkBadge?: boolean
    // 列头第三行字段描述(PG 不展示)。
    showColComment?: boolean
    // 行首操作列(按行删除按钮)。
    rowActions?: boolean
    // 行首删除按钮是否渲染(PG 视图/物化视图不渲染,td 保留占位)。
    rowDeleteButton?: boolean
    // 行首删除是否可用:false 时按钮禁用并以 title 说明原因。
    rowActionEnabled?: boolean
    // 行内编辑态(由父级 useCellEdit 持有,单向传入)。
    editing?: { row: number; col: number } | null
    editValue?: string
    // 编辑器控件:input(Mysql/PG/CH)或 textarea(ES,行数随内容增长)。
    editorTag?: 'input' | 'textarea'
    editorRows?: number
    // 只读单元格样式(cell-readonly)判定:入参为列下标。
    cellReadonly?: (ci: number) => boolean
    // 首列为特殊标识列(ES _id):内容由 #leading-cell 插槽渲染。
    leadingColumn?: boolean
    // ES _id 列头不可排序样式(col-nosort)。
    nosortFirstColumn?: boolean
    // 单元格键盘进入编辑(PG:tabindex + Enter/Space);键位判定为 false 时不挂监听。
    keyboardEditable?: boolean
    // 列头/单元格悬停提示(方言各异,由调用方给出)。
    colTitle: (c: TableGridColumn, ci: number) => string
    cellTitle: (v: string | null, ci: number) => string
  }>(),
  {
    orderBy: '',
    asc: true,
    sortable: true,
    keyboardSortable: false,
    showPkBadge: false,
    showColComment: true,
    rowActions: false,
    rowDeleteButton: true,
    rowActionEnabled: true,
    editing: null,
    editValue: '',
    editorTag: 'input',
    editorRows: 1,
    cellReadonly: undefined,
    leadingColumn: false,
    nosortFirstColumn: false,
    keyboardEditable: false,
  },
)

const emit = defineEmits<{
  sort: [col: string, ci: number]
  'start-edit': [ri: number, ci: number]
  'submit-edit': []
  'cancel-edit': []
  'row-delete': [ri: number]
  'update:editValue': [value: string]
}>()

// 超长单元格客户端截断展示(完整值保留在 title 提示中)。
const CELL_TRUNCATE = 200

function isTruncated(v: string | null): boolean {
  return v !== null && v.length > CELL_TRUNCATE
}

function cellDisplay(v: string | null): string {
  if (v === null) return 'NULL'
  return isTruncated(v) ? `${v.slice(0, CELL_TRUNCATE)}…` : v
}

function isEditing(ri: number, ci: number): boolean {
  return props.editing?.row === ri && props.editing?.col === ci
}

// focusEditor 编辑框渲染后自动聚焦(function ref 挂载时触发)。
function focusEditor(el: Element | ComponentPublicInstance | null): void {
  if (el) (el as HTMLInputElement).focus()
}

function onEditorInput(e: Event): void {
  emit('update:editValue', (e.target as HTMLInputElement).value)
}

// 列头排序入口:Hive 等不可排序网格不 emit。
function onSortClick(col: string, ci: number): void {
  if (props.sortable) emit('sort', col, ci)
}

// 复刻 Vue 系统键修饰符(.enter/.space)的匹配语义:hyphenate(event.key) 后
// 与修饰符比对(real 浏览器 Space 键 key=' ',测试触发器给 'space',都命中)。
function keyEventKey(e: KeyboardEvent): string {
  return e.key.replace(/\B([A-Z])/g, '-$1').toLowerCase()
}

function isEnterOrSpace(e: KeyboardEvent): boolean {
  const k = keyEventKey(e)
  return k === 'enter' || k === ' ' || k === 'space'
}

// PG 单元格键盘进入编辑:守卫关闭时直接返回(不 preventDefault),保证
// 其它数据源(以及编辑框自身回车提交)的冒泡语义不受影响。
function onCellKeydown(e: KeyboardEvent, ri: number, ci: number): void {
  if (!props.keyboardEditable) return
  if (isEnterOrSpace(e)) {
    e.preventDefault()
    emit('start-edit', ri, ci)
  }
}

// PG 列头键盘排序(Enter/Space):同 .prevent 修饰语义,先阻止默认再 emit。
function onThKeydown(e: KeyboardEvent, col: string, ci: number): void {
  if (!props.keyboardSortable) return
  if (isEnterOrSpace(e)) {
    e.preventDefault()
    emit('sort', col, ci)
  }
}

// 空态行占位列数:行首操作列存在时多一列。
const emptyColspan = computed(() => props.columns.length + (props.rowActions ? 1 : 0))
</script>

<template>
  <table class="table data-table" :data-test="`${testPrefix}-grid`">
    <thead>
      <tr>
        <!-- 行首操作列:每行挂删除按钮(悬停行显现)。 -->
        <th v-if="rowActions" class="row-action" aria-hidden="true"></th>
        <th
          v-for="(c, ci) in columns"
          :key="c.name"
          :data-test="`${testPrefix}-col`"
          :class="{ 'th-sort': sortable, 'col-nosort': nosortFirstColumn && ci === 0 }"
          :tabindex="keyboardSortable ? 0 : undefined"
          :aria-sort="keyboardSortable ? (orderBy === c.name ? (asc ? 'ascending' : 'descending') : 'none') : undefined"
          :title="colTitle(c, ci)"
          @click="onSortClick(c.name, ci)"
          @keydown="onThKeydown($event, c.name, ci)"
        >
          <!-- 弹性布局只在内层 div:th 必须保持 table-cell,否则脱离
               表格列布局,WebKit 会把整行列头堆进一个匿名单元格。 -->
          <div class="col-head">
            <span class="col-name">
              <span :data-test="`${testPrefix}-col-name`">{{ c.name }}</span>
              <!-- 主键角标:后端 is_in_primary_key 标记。 -->
              <span v-if="showPkBadge && c.is_in_primary_key" class="pk-badge" :data-test="`${testPrefix}-pk-badge`" title="主键">🔑</span>
              <span v-if="orderBy === c.name" class="col-arrow">{{ asc ? '↑' : '↓' }}</span>
            </span>
            <span class="col-type" :data-test="`${testPrefix}-col-type`">{{ c.type }}</span>
            <span v-if="showColComment && c.comment" class="col-comment" :data-test="`${testPrefix}-col-comment`">{{ c.comment }}</span>
          </div>
        </th>
      </tr>
    </thead>
    <tbody>
      <tr v-for="(row, ri) in rows" :key="ri" :data-test="`${testPrefix}-row`">
        <!-- 行首删除:危险色小按钮,不可用时禁用并以 title 说明原因。 -->
        <td v-if="rowActions" class="row-action">
          <button
            v-if="rowDeleteButton"
            class="mini-btn danger row-del"
            type="button"
            data-test="btn-row-delete"
            :disabled="!rowActionEnabled"
            :title="rowActionEnabled ? '删除此行' : '结果无主键,无法定位行'"
            @click="emit('row-delete', ri)"
          >
            ✕
          </button>
        </td>
        <td
          v-for="(cell, ci) in row"
          :key="ci"
          class="mono cell"
          :class="{ 'cell-null': cell === null && !isEditing(ri, ci), 'cell-trunc': isTruncated(cell), 'cell-readonly': cellReadonly?.(ci) }"
          :title="cellTitle(cell, ci)"
          :tabindex="keyboardEditable && rowActionEnabled ? 0 : undefined"
          :data-test="`${testPrefix}-cell`"
          @dblclick="emit('start-edit', ri, ci)"
          @keydown="onCellKeydown($event, ri, ci)"
        >
          <!-- 首列特殊标识(ES _id):行首挂 JSON 编辑与删除按钮,交给插槽。 -->
          <slot v-if="leadingColumn && ci === 0" name="leading-cell" :cell="cell" :row-index="ri">
            {{ cellDisplay(cell) }}
          </slot>
          <!-- 行内编辑:双击进入,Esc/blur 取消,回车提交预览。
               textarea 行数随内容增长(ES),尺寸与展示态一致。 -->
          <textarea
            v-else-if="editorTag === 'textarea' && isEditing(ri, ci)"
            :ref="focusEditor"
            :value="editValue"
            :rows="editorRows"
            class="cell-editor"
            :data-test="`${testPrefix}-cell-editor`"
            spellcheck="false"
            autocapitalize="off"
            autocorrect="off"
            autocomplete="off"
            @blur="emit('cancel-edit')"
            @input="onEditorInput"
            @keydown.enter.prevent="emit('submit-edit')"
            @keydown.esc.prevent="emit('cancel-edit')"
          />
          <input
            v-else-if="isEditing(ri, ci)"
            :ref="focusEditor"
            :value="editValue"
            class="cell-editor"
            :data-test="`${testPrefix}-cell-editor`"
            @blur="emit('cancel-edit')"
            @input="onEditorInput"
            @keydown.enter.prevent="emit('submit-edit')"
            @keydown.esc.prevent="emit('cancel-edit')"
          />
          <template v-else>{{ cellDisplay(cell) }}</template>
        </td>
      </tr>
      <!-- 零行(空表/过滤无匹配):表头保持横排,空态作为表内一行。
           loading 期间不渲染,避免首屏闪现;ES 用 #empty 插槽替换文案。 -->
      <tr v-if="rows.length === 0 && !loading">
        <slot name="empty" :colspan="emptyColspan">
          <td :colspan="emptyColspan" class="empty" :data-test="`${testPrefix}-grid-empty`">
            <div class="empty-icon">◌</div>
            <div>该表暂无数据</div>
            <div class="empty-hint">数据写入后点击「刷新」查看</div>
          </td>
        </slot>
      </tr>
    </tbody>
  </table>
</template>

<style scoped>
/* 基础表样式(width/border-collapse/th·td 通用属性/hover)走全局 .data-table 基座,
   这里只保留差异:max-content 宽度、粘性表头、紧凑 padding、斑马纹与单元格编辑。 */
.table {
  /* width:max-content:列数多时按内容自然宽度横向滚动,而不是把列头
     压成竖排文字;min-width:100% 保证列少时仍铺满容器。 */
  width: max-content; min-width: 100%; font-size: 13px;
}
.table th {
  position: sticky; top: 0; z-index: 1;
  padding: 7px 10px; user-select: none;
}
/* 可排序列头(除 Hive 只读外):指针 + 悬停反馈。 */
.table th.th-sort { cursor: pointer; transition: background 0.12s ease; }
.table th.th-sort:hover { background: var(--bg-hover); }
/* _id 列头不可排序:去掉指针与悬停反馈(ES)。 */
.table th.col-nosort { cursor: default; }
.table th.col-nosort:hover { background: var(--bg-subtle); }
.col-head { display: flex; flex-direction: column; gap: 1px; white-space: nowrap; }
.col-name { font-weight: 600; display: flex; align-items: center; gap: 4px; }
/* 主键角标:列名后的小钥匙,悬停提示「主键」。 */
.pk-badge { font-size: 10px; line-height: 1; }
.col-type { font-size: 10px; color: var(--text-tertiary); font-family: var(--mono); font-weight: 400; }
.col-arrow { color: var(--accent); font-size: 11px; }
/* 第三行:字段描述(comment),灰色小字;超长省略,悬停 title 已含全文。 */
.col-comment { font-size: 10px; color: var(--text-tertiary); max-width: 200px; overflow: hidden; text-overflow: ellipsis; }
.table tbody tr:nth-child(even) td { background: var(--zebra); }
.table td { padding: 5px 10px; word-break: break-all; max-width: 420px; }
.cell-null { color: var(--text-tertiary); font-style: italic; }
.cell-trunc { color: var(--text-secondary); }
/* 只读单元格:不出现文本光标,提示走 title。 */
.cell-readonly { cursor: default; }
/* 行首操作列:危险色删除小按钮,悬停所在行时显现。 */
.table .row-action { width: 34px; padding: 4px 8px; text-align: center; }
.mini-btn {
  flex: none; font-size: 10px; line-height: 1;
  padding: 2px 5px; border-radius: 4px; cursor: pointer;
  background: transparent; color: var(--danger); border: 1px solid var(--danger);
}
.mini-btn.danger:hover:not(:disabled) { background: var(--danger-soft); }
.mini-btn:disabled { opacity: 0.35; cursor: not-allowed; }
.row-del { opacity: 0; transition: opacity 0.12s ease; }
.table tbody tr:hover .row-del { opacity: 1; }
/* 行内编辑输入框:覆盖单元格内容,细边框紧凑,与表格行高一致;
   textarea 随内容多行(ES),细边框一致。 */
.cell-editor {
  box-sizing: border-box; width: 100%; min-width: 80px;
  background: var(--bg-elevated); color: var(--text);
  border: 1px solid var(--accent); border-radius: 4px;
  padding: 1px 6px; font-size: 13px; font-family: var(--mono);
}
textarea.cell-editor { display: block; line-height: 1.5; resize: none; overflow-y: auto; }
.cell-editor:focus { outline: none; box-shadow: 0 0 0 2px var(--accent-soft); }
.mono { font-family: var(--mono); }
.empty { text-align: center; color: var(--text-tertiary); padding: 28px 24px; }
.empty-icon { font-size: 26px; margin-bottom: 6px; }
.empty-hint { font-size: 11px; margin-top: 4px; color: var(--text-tertiary); }
</style>
