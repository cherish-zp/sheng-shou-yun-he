<script setup lang="ts">
import { computed, type ComponentPublicInstance } from 'vue'
import { ArrowDown, ArrowUp, KeyRound, X } from 'lucide-vue-next'
import { Badge } from '@/components/ui/badge'

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
  <!-- DBX 皮肤:粘性表头 bg-muted、行 hover bg-accent/50、分隔线 border-border;
       width:max-content 列多时按内容自然宽度横向滚动,min-w-full 列少时铺满。 -->
  <table class="w-max min-w-full border-collapse text-[13px]" :data-test="`${testPrefix}-grid`">
    <thead>
      <tr>
        <!-- 行首操作列:每行挂删除按钮(悬停行显现)。 -->
        <th
          v-if="rowActions"
          class="row-action sticky top-0 z-[1] w-[34px] min-w-[34px] border-b border-border bg-muted px-2 py-1.5 text-center align-middle font-medium select-none"
          aria-hidden="true"
        ></th>
        <th
          v-for="(c, ci) in columns"
          :key="c.name"
          :data-test="`${testPrefix}-col`"
          class="sticky top-0 z-[1] border-b border-border bg-muted px-2.5 py-1.5 text-left align-middle font-medium whitespace-nowrap select-none"
          :class="[
            sortable ? 'th-sort cursor-pointer transition-colors hover:bg-accent/60' : '',
            nosortFirstColumn && ci === 0 ? 'col-nosort cursor-default' : '',
          ]"
          :tabindex="keyboardSortable ? 0 : undefined"
          :aria-sort="keyboardSortable ? (orderBy === c.name ? (asc ? 'ascending' : 'descending') : 'none') : undefined"
          :title="colTitle(c, ci)"
          @click="onSortClick(c.name, ci)"
          @keydown="onThKeydown($event, c.name, ci)"
        >
          <!-- 弹性布局只在内层 div:th 必须保持 table-cell,否则脱离
               表格列布局,WebKit 会把整行列头堆进一个匿名单元格。 -->
          <div class="col-head flex flex-col gap-px whitespace-nowrap">
            <span class="col-name flex items-center gap-1 font-semibold text-foreground">
              <span :data-test="`${testPrefix}-col-name`">{{ c.name }}</span>
              <!-- 主键角标:后端 is_in_primary_key 标记,lucide 钥匙小徽章。 -->
              <Badge
                v-if="showPkBadge && c.is_in_primary_key"
                variant="outline"
                class="pk-badge border-warning/40 bg-warning/10 px-1 py-0 text-warning"
                :data-test="`${testPrefix}-pk-badge`"
                title="主键"
              >
                <KeyRound class="size-3" />
              </Badge>
              <span v-if="orderBy === c.name" class="col-arrow inline-flex text-primary">
                <ArrowUp v-if="asc" class="size-3" />
                <ArrowDown v-else class="size-3" />
              </span>
            </span>
            <span class="col-type font-mono text-[10px] font-normal text-muted-foreground" :data-test="`${testPrefix}-col-type`">{{ c.type }}</span>
            <span v-if="showColComment && c.comment" class="col-comment max-w-[200px] truncate text-[10px] text-muted-foreground/80" :data-test="`${testPrefix}-col-comment`">{{ c.comment }}</span>
          </div>
        </th>
      </tr>
    </thead>
    <tbody>
      <tr
        v-for="(row, ri) in rows"
        :key="ri"
        :data-test="`${testPrefix}-row`"
        class="group border-b border-border transition-colors last:border-b-0 hover:bg-accent/50 data-[state=selected]:bg-accent"
      >
        <!-- 行首删除:危险色小按钮,不可用时禁用并以 title 说明原因。 -->
        <td v-if="rowActions" class="row-action px-2 py-1 text-center align-middle">
          <button
            v-if="rowDeleteButton"
            class="row-del inline-flex size-5 items-center justify-center rounded-sm border border-destructive/50 bg-transparent text-destructive opacity-0 transition-opacity hover:bg-destructive/10 disabled:pointer-events-none disabled:opacity-40 group-hover:opacity-100"
            type="button"
            data-test="btn-row-delete"
            :disabled="!rowActionEnabled"
            :title="rowActionEnabled ? '删除此行' : '结果无主键,无法定位行'"
            @click="emit('row-delete', ri)"
          >
            <X class="size-3" />
          </button>
        </td>
        <td
          v-for="(cell, ci) in row"
          :key="ci"
          class="cell max-w-[420px] break-all px-2.5 py-1 align-middle font-mono"
          :class="[
            cell === null && !isEditing(ri, ci) ? 'cell-null italic text-muted-foreground' : '',
            isTruncated(cell) ? 'cell-trunc text-foreground/70' : '',
            cellReadonly?.(ci) ? 'cell-readonly cursor-default' : '',
          ]"
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
            class="cell-editor block w-full min-w-[80px] resize-none overflow-y-auto rounded-[4px] border border-primary/40 bg-background px-1.5 py-0.5 font-mono text-[13px] leading-normal text-foreground outline-none ring-2 ring-primary/15"
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
            class="cell-editor w-full min-w-[80px] rounded-[4px] border border-primary/40 bg-background px-1.5 py-0.5 font-mono text-[13px] text-foreground outline-none ring-2 ring-primary/15"
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
          <td :colspan="emptyColspan" class="p-7 text-center text-muted-foreground" :data-test="`${testPrefix}-grid-empty`">
            <div class="empty-icon mb-1.5 text-[26px] leading-none">◌</div>
            <div>该表暂无数据</div>
            <div class="empty-hint mt-1 text-[11px] text-muted-foreground/80">数据写入后点击「刷新」查看</div>
          </td>
        </slot>
      </tr>
    </tbody>
  </table>
</template>

