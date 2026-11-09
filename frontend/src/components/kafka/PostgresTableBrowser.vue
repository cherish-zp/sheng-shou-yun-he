<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import TableDataGrid from '@/components/common/TableDataGrid.vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { usePagedTable } from '@/composables/usePagedTable'
import { useCellEdit } from '@/composables/useCellEdit'
import { useRowDelete } from '@/composables/useRowDelete'
import { getApi } from '@/api/client'

const props = defineProps<{ connectionId: string; database: string; schema: string; relation: string; relationType?: 'table' | 'view' | 'materialized_view' }>()

// 每页行数与后端 PostgresPageRows 的 limit 对齐。
const PAGE_SIZE = 200
// 超长单元格客户端截断展示(完整值保留在 title 提示中)。
const CELL_TRUNCATE = 200

interface PgColumn {
  name: string
  type: string
  is_in_primary_key?: boolean
}

interface PostgresPageRowsResult {
  columns: PgColumn[]
  rows: (string | null)[][]
  total_rows: number
  primary_key: string[]
}

// 单元格更新的定位/写入描述;set 为新值(null = 写 NULL),where 为行定位
// 条件(PostgreSQL 语义下后端契约:仅主键列,前端禁止拼 SQL 文本)。
interface PgCellRef {
  column: string
  value: string | null
}

interface PgCellUpdateTarget {
  connection_id: string
  database: string
  schema: string
  relation: string
  relation_kind: 'table' | 'view' | 'materialized_view'
  set: PgCellRef
  where: PgCellRef[]
}

// 按行删除请求:where 只允许主键列,视图/物化视图后端拒绝删除(前端
// 对非 table 关系不渲染删除入口)。
interface PgDeleteRowTarget {
  connection_id: string
  database: string
  schema: string
  relation: string
  relation_kind: 'table' | 'view' | 'materialized_view'
  where: PgCellRef[]
}

// Postgres* 系列绑定调用统一收口到 api/client.ts 适配层(WailsApi 按 wailsjs
// 绑定名转发,req 为契约形状原样;测试在 wailsjs 模块层 mock,命中同一转发)。
// 绑定在 Api 接口上为可选成员,调用方用可选链访问;缺绑定按请求失败处理。

const columns = ref<PgColumn[]>([])
const rows = ref<(string | null)[][]>([])
const totalRows = ref(0)
// 后端返回的主键列名列表(空数组=无主键,整表只读)。
const primaryKey = ref<string[]>([])
const loading = ref(false)
const error = ref<string | null>(null)

// (分页/过滤/排序与行内编辑态由下方 usePagedTable / useCellEdit 组合式承担。)

// 仅当表有主键时开放行内编辑;无主键表所有数据单元格只读。
const canEdit = computed(() => primaryKey.value.length > 0)
// 视图与物化视图不允许 TRUNCATE:清空数据入口仅对 table 显示。
const isTable = computed(() => (props.relationType ?? 'table') === 'table')
const confirmTruncate = ref(false)

// --- 分页/过滤/排序:共用组合式承担状态与翻页动作,取数回调整体注入。 ---
const {
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
} = usePagedTable({ pageSize: PAGE_SIZE, refetch: fetchPage })

// --- 行内编辑态:共用组合式(无主键表通过 canStart 全表只读)。 ---
const {
  editing,
  editValue,
  isEditing,
  startEdit,
  cancelEdit,
  focusEditor,
  discard: discardEdit,
} = useCellEdit({ loading, rows, columns, canStart: () => canEdit.value })

async function fetchPage(): Promise<void> {
  loading.value = true
  error.value = null
  // 换页/刷新后行集合将重建,未完成的行内编辑一并丢弃。
  discardEdit()
  try {
    const res = await getApi().postgresPageRows?.({
      connection_id: props.connectionId,
      database: props.database,
      schema: props.schema,
      relation: props.relation,
      where: appliedWhere.value || undefined,
      order_by: orderBy.value || undefined,
      asc: asc.value,
      limit: PAGE_SIZE,
      offset: offset.value,
    })
    if (!res) throw new Error('当前环境不支持 PostgreSQL 表浏览')
    // wire 形状防御:后端异常/老版本可能给出 null 或缺字段;模板对
    // columns/rows 按数组、totalRows 按数字直接使用,任何 null/undefined
    // 都会在渲染期抛 TypeError 并中断整个调度队列(WKWebView 表现为白屏)。
    columns.value = res.columns ?? []
    rows.value = res.rows ?? []
    primaryKey.value = res.primary_key ?? []
    const total = Number(res.total_rows)
    totalRows.value = Number.isFinite(total) ? total : 0
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e)
  } finally {
    loading.value = false
  }
}

onMounted(() => {
  void fetchPage()
})

// 外部(树/Layout)切换表时重置过滤/排序/分页状态并重拉第一页。
watch(
  () => props.relation,
  () => {
    resetFilters()
    void fetchPage()
  },
)

// 清空数据:文案包含表名与引擎,确认弹窗走危险色。
const truncateMessage = computed(
  () => `确认清空表「${props.schema}.${props.relation}」的全部数据？此操作不可恢复。`,
)

async function doTruncate(): Promise<void> {
  confirmTruncate.value = false
  try {
    await getApi().postgresTruncateTable?.({
      connection_id: props.connectionId,
      database: props.database,
      schema: props.schema,
      relation: props.relation,
      relation_kind: props.relationType ?? 'table',
    })
    offset.value = 0
    await fetchPage()
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e)
  }
}

// --- 单元格渲染:nil → NULL(浅色);超长值截断(浅色标注 + title 全文)。 ---

function isTruncated(v: string | null): boolean {
  return v !== null && v.length > CELL_TRUNCATE
}

function cellDisplay(v: string | null): string {
  if (v === null) return 'NULL'
  return isTruncated(v) ? `${v.slice(0, CELL_TRUNCATE)}…` : v
}

// cellTitle 数据单元格悬停提示:无主键表为只读原因;有主键时展示完整原值。
function cellTitle(v: string | null): string {
  if (!canEdit.value) return '表无主键,不支持编辑'
  return v ?? ''
}

// colTitle 列头悬停提示:排序说明 + 类型。
function colTitle(c: PgColumn): string {
  return `按 ${c.name} 排序 · ${c.type}`
}

// --- 单元格行内编辑:双击 → 输入 → 回车 → 预览确认 → 执行并刷新。 ---
// (isEditing/startEdit/cancelEdit/focusEditor 来自 useCellEdit 组合式。)

// buildTarget 组装更新请求:set 为编辑后的值(空串=写 NULL);where 一律
// 只用主键列的编辑前原值快照(后端契约,前端不拼 SQL 文本)。
function buildTarget(ed: { row: number; col: number }): PgCellUpdateTarget {
  const cols = columns.value
  const originalRow = rows.value[ed.row] ?? []
  const edited = cols[ed.col]
  return {
    connection_id: props.connectionId,
    database: props.database,
    schema: props.schema,
    relation: props.relation,
    relation_kind: props.relationType ?? 'table',
    set: {
      column: edited.name,
      value: editValue.value === '' ? null : editValue.value,
    },
    where: primaryKey.value.map((name) => {
      const i = cols.findIndex((c) => c.name === name)
      return { column: name, value: originalRow[i] ?? null }
    }),
  }
}

// --- 单元格更新预览/确认/执行状态机(与 useCHCellUpdate 契约一致;因本次
// 只允许新建组件与测试两个文件,状态机内联于此组件)。 ---
const cuError = ref<string | null>(null)
const confirmOpen = ref(false)
const statement = ref('')
const matchedRows = ref(0)
// 预览通过后暂存的待执行目标,确认时原样发给执行。
let pending: PgCellUpdateTarget | null = null

// submitEdit 回车提交:先固化 target 快照再退出编辑态,随后预览并打开
// 确认弹窗;失败时 cuError 已记录并显示进现有错误区。
async function submitEdit(): Promise<void> {
  const ed = editing.value
  if (!ed) return
  const target = buildTarget(ed)
  cancelEdit()
  cuError.value = null
  pending = target
  try {
    const res = await getApi().postgresPreviewCellUpdate?.(target)
    if (!res) throw new Error('当前环境不支持单元格更新')
    statement.value = res.statement
    matchedRows.value = res.matched_rows
    confirmOpen.value = true
  } catch (e) {
    cuError.value = e instanceof Error ? e.message : String(e)
    pending = null
  }
}

// 弹窗文案:UPDATE 语句全文 + 匹配行数;将波及多行时追加警示。
const cellUpdateMessage = computed(() => {
  const n = Number(matchedRows.value ?? 0)
  const lines = [`将执行以下语句:\n${statement.value}`, `匹配 ${n} 行`]
  if (n > 1) lines.push(`将同时更新 ${n} 行,请确认`)
  return lines.join('\n')
})

// onCellUpdateConfirm 确认执行:成功后关闭弹窗并刷新当前页;失败弹窗保持
// 打开(CH 行为一致),错误同时进错误区。
async function onCellUpdateConfirm(): Promise<void> {
  const target = pending
  if (!target) {
    confirmOpen.value = false
    return
  }
  try {
    await getApi().postgresUpdateCell?.(target)
    pending = null
    confirmOpen.value = false
    statement.value = ''
    matchedRows.value = 0
    void fetchPage()
  } catch (e) {
    cuError.value = e instanceof Error ? e.message : String(e)
  }
}

// onCellUpdateCancel 取消:关闭弹窗并清空全部状态(含待执行目标)。
function onCellUpdateCancel(): void {
  pending = null
  confirmOpen.value = false
  statement.value = ''
  matchedRows.value = 0
  cuError.value = null
}

// --- 按行删除:行首危险按钮 → 按主键原值预览 DELETE → 确认执行并刷新。 ---
// (三段式预览/确认/执行状态机由 useRowDelete 组合式承担,文案与守卫
// 与既有实现逐字一致;视图/物化视图在 askDeleteRow 层拦截。)
const {
  error: delError,
  confirmOpen: confirmDeleteOpen,
  message: deleteMessage,
  request: requestDeleteRow,
  confirm: onDeleteRowConfirm,
  cancel: onDeleteRowCancel,
} = useRowDelete<PgDeleteRowTarget>({
  preview: async (target) => {
    const res = await getApi().postgresPreviewDeleteRow?.(target)
    if (!res) throw new Error('当前环境不支持按行删除')
    return res
  },
  execute: async (target) => {
    await getApi().postgresDeleteRow?.(target)
  },
  refetch: fetchPage,
})

// buildDeleteTarget 组装删除请求:where 只用主键列的当前行原值快照
// (NULL 原值保持 null,由后端拼 IS NULL;前端不拼 SQL 文本)。
function buildDeleteTarget(ri: number): PgDeleteRowTarget {
  const cols = columns.value
  const row = rows.value[ri] ?? []
  return {
    connection_id: props.connectionId,
    database: props.database,
    schema: props.schema,
    relation: props.relation,
    relation_kind: props.relationType ?? 'table',
    where: primaryKey.value.map((name) => {
      const i = cols.findIndex((c) => c.name === name)
      return { column: name, value: row[i] ?? null }
    }),
  }
}

// askDeleteRow 点击行首删除:仅实体表且有主键;预览/确认/执行交给组合式。
async function askDeleteRow(ri: number): Promise<void> {
  if (!canEdit.value || !isTable.value) return
  await requestDeleteRow(buildDeleteTarget(ri))
}
</script>

<template>
  <div class="p-4" data-test="pg-table-browser">
    <div class="flex flex-wrap items-center gap-2 rounded-md border border-border bg-muted/50 px-3 py-2 text-xs text-muted-foreground" data-test="pg-summary">
      <span class="font-mono font-semibold text-foreground" data-test="pg-summary-table">{{ props.database }}.{{ props.schema }}.{{ props.relation }}</span>
      <span class="text-muted-foreground/60">·</span>
      <span data-test="pg-summary-type">{{ props.relationType || 'table' }}</span>
      <span class="text-muted-foreground/60">·</span>
      <span class="tabular-nums" data-test="pg-summary-rows">≈ {{ totalRows.toLocaleString() }} 行</span>
    </div>

    <div class="mt-2.5 flex flex-wrap items-center gap-2">
      <Input
        :value="where"
        class="h-8 min-w-[220px] max-w-[420px] flex-1 bg-background text-[13px]"
        type="search"
        data-test="pg-where"
        placeholder="WHERE 条件,如 age > 18"
        autocapitalize="off"
        autocorrect="off"
        autocomplete="off"
        spellcheck="false"
        @input="where = ($event.target as HTMLInputElement).value"
        @keydown.enter="applyWhere"
      />
      <Button variant="ghost" size="sm" class="text-xs" data-test="btn-pg-apply" :disabled="loading" @click="applyWhere">过滤</Button>
      <Button variant="ghost" size="sm" class="text-xs" data-test="btn-pg-refresh" :disabled="loading" @click="refresh">
        {{ loading ? '加载中…' : '刷新' }}
      </Button>
      <Button
        v-if="isTable"
        variant="ghost"
        size="sm"
        class="text-xs text-destructive hover:text-destructive"
        data-test="btn-pg-truncate"
        @click="confirmTruncate = true"
      >清空数据</Button>
    </div>

    <div v-if="error || cuError || delError" class="mt-2 rounded-md bg-destructive/10 px-2.5 py-1.5 text-[13px] text-destructive" data-test="pg-error">{{ error || cuError || delError }}</div>

    <div v-if="columns.length > 0 && rows.length === 0 && !loading" class="mt-2.5 max-h-[calc(100vh-320px)] overflow-auto rounded-lg border border-border bg-card">
      <div class="px-0 pb-1" data-test="pg-fields-panel">
        <div class="border-b border-border px-3 py-2.5 text-xs font-semibold text-muted-foreground" data-test="pg-fields-title">表结构(共 {{ columns.length }} 字段)</div>
        <div v-for="c in columns" :key="c.name" class="flex items-baseline gap-2.5 border-b border-border px-3 py-2 last:border-b-0" data-test="pg-field-row">
          <span class="font-mono text-[13px] font-semibold text-foreground" data-test="pg-field-name">{{ c.name }}</span>
          <span class="shrink-0 rounded-[4px] bg-muted px-1.5 py-px font-mono text-[10px] text-muted-foreground" data-test="pg-field-type">{{ c.type }}</span>
        </div>
      </div>
      <div class="p-7 text-center text-muted-foreground" data-test="pg-grid-empty">
        <div class="mb-1.5 text-[26px] leading-none">◌</div>
        <div>该表暂无数据</div>
        <div class="mt-1 text-[11px] text-muted-foreground/80">数据写入后点击「刷新」查看</div>
      </div>
    </div>

    <div v-else class="mt-2.5 max-h-[calc(100vh-320px)] overflow-auto rounded-lg border border-border bg-card">
      <!-- 共享表格网格:列头/主键角标/行删除/行内编辑/空态由 TableDataGrid 承担;
           PG 特有无障碍契约(th/单元格键盘操作 + aria-sort)经 keyboard props 表达。 -->
      <TableDataGrid
        v-model:edit-value="editValue"
        test-prefix="pg"
        :columns="columns"
        :rows="rows"
        :loading="loading"
        :order-by="orderBy"
        :asc="asc"
        show-pk-badge
        :show-col-comment="false"
        row-actions
        :row-delete-button="isTable"
        :row-action-enabled="canEdit"
        keyboard-sortable
        keyboard-editable
        :editing="editing"
        :cell-readonly="() => !canEdit"
        :col-title="colTitle"
        :cell-title="cellTitle"
        @sort="sortBy"
        @start-edit="startEdit"
        @submit-edit="submitEdit"
        @cancel-edit="cancelEdit"
        @row-delete="askDeleteRow"
      />
    </div>

    <div class="mt-2.5 flex items-center gap-2.5" data-test="pg-pager">
      <Button variant="ghost" size="sm" class="text-xs" data-test="btn-pg-prev" :disabled="offset === 0 || loading" @click="prevPage">上一页</Button>
      <span class="text-xs tabular-nums text-muted-foreground" data-test="pg-page-label">第 {{ page }} 页</span>
      <Button variant="ghost" size="sm" class="text-xs" data-test="btn-pg-next" :disabled="rows.length < PAGE_SIZE || loading" @click="nextPage">下一页</Button>
    </div>

    <!-- 清空数据:危险操作,文案含表名与引擎。 -->
    <ConfirmDialog
      :show="confirmTruncate"
      :message="truncateMessage"
      confirm-text="清空"
      danger
      @confirm="doTruncate"
      @cancel="confirmTruncate = false"
    />

    <!-- 单元格更新确认:展示将执行的 UPDATE 语句全文与匹配行数。 -->
    <ConfirmDialog
      :show="confirmOpen"
      :message="cellUpdateMessage"
      confirm-text="执行"
      @confirm="onCellUpdateConfirm"
      @cancel="onCellUpdateCancel"
    />

    <!-- 按行删除确认:展示将执行的 DELETE 语句全文、命中行数与不可恢复警示。 -->
    <ConfirmDialog
      :show="confirmDeleteOpen"
      :message="deleteMessage"
      confirm-text="删除"
      danger
      @confirm="onDeleteRowConfirm"
      @cancel="onDeleteRowCancel"
    />
  </div>
</template>
