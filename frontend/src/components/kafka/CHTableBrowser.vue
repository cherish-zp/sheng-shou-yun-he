<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { getApi } from '@/api/client'
import type { CHColumn, CHTableInfo, CHDeleteRowPreview, CHDeleteRowRequest } from '@/api/types'
import { formatBytes } from '@/utils/bytes'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import TableDataGrid from '@/components/common/TableDataGrid.vue'
import { useCHCellUpdate } from '@/composables/chCellUpdate'
import { usePagedTable } from '@/composables/usePagedTable'
import { useCellEdit } from '@/composables/useCellEdit'
import { useRowDelete } from '@/composables/useRowDelete'

const props = defineProps<{ connectionId: string; database: string; table: string }>()

// 每页行数与后端 CHPageRows 的 limit 对齐。
const PAGE_SIZE = 200
// 超长单元格客户端截断展示(完整值保留在 title 提示中)。
const CELL_TRUNCATE = 200

// currentTable 为内部生效表名:初始取 props.table,工具栏切换器与外部
// props 变化都会改它,后续请求/清空/摘要均以它为准(免回树换表)。
const currentTable = ref(props.table)
const columns = ref<CHColumn[]>([])
const rows = ref<(string | null)[][]>([])
const engine = ref('')
const totalRows = ref(0)
// 该库全部表清单,供工具栏表切换器使用;加载失败仅退化为单选项,不打断浏览。
const dbTables = ref<CHTableInfo[]>([])
const loading = ref(false)
const error = ref<string | null>(null)

// --- 数据单元格行内编辑 ---
// cu:单元格 UPDATE 的预览/确认/执行状态机(SQL 与弹窗开关由其管理)。
const cu = useCHCellUpdate()
// 后端 PageRows 返回的主键列名(按 position 序;空数组=无主键)。
const primaryKey = ref<string[]>([])

const isDistributed = computed(() => engine.value.trim().toLowerCase() === 'distributed')
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

// --- 行内编辑态:共用组合式(CH 不设全表只读限制,canStart 走缺省)。 ---
const {
  editing,
  editValue,
  isEditing,
  startEdit,
  cancelEdit,
  focusEditor,
  discard: discardEdit,
} = useCellEdit({ loading, rows, columns })

// 切换器选项:接口清单缺当前表(未加载/失败)时兜底补一个当前表。
const tableOptions = computed<CHTableInfo[]>(() => {
  if (dbTables.value.some((t) => t.name === currentTable.value)) return dbTables.value
  return [{ name: currentTable.value, engine: engine.value, total_rows: totalRows.value }, ...dbTables.value]
})

async function fetchPage(): Promise<void> {
  loading.value = true
  error.value = null
  // 换页/刷新后行集合将重建,未完成的行内编辑一并丢弃。
  discardEdit()
  try {
    const res = await getApi().chPageRows({
      connection_id: props.connectionId,
      database: props.database,
      table: currentTable.value,
      where: appliedWhere.value || undefined,
      order_by: orderBy.value || undefined,
      asc: asc.value,
      limit: PAGE_SIZE,
      offset: offset.value,
    })
    // wire 形状防御:后端异常/老版本可能给出 null 或缺字段;模板对
    // columns/rows 按数组、totalRows 按数字直接使用,任何 null/undefined
    // 都会在渲染期抛 TypeError 并中断整个调度队列(WKWebView 表现为白屏)。
    columns.value = res.columns ?? []
    rows.value = res.rows ?? []
    engine.value = res.engine ?? ''
    primaryKey.value = res.primary_key ?? []
    const total = Number(res.total_rows)
    totalRows.value = Number.isFinite(total) ? total : 0
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e)
  } finally {
    loading.value = false
  }
}

// loadTableList 拉取该库全部表(不含 system)作为切换器选项。
async function loadTableList(): Promise<void> {
  try {
    dbTables.value = await getApi().listCHTables({
      connection_id: props.connectionId,
      database: props.database,
      show_system: false,
    })
  } catch {
    dbTables.value = []
  }
}

onMounted(() => {
  void fetchPage()
  void loadTableList()
})

// resetForTableSwitch 换表后丢弃上一表的过滤/排序/分页状态(不触发取数),
// 由调用方紧接着重拉第一页。
function resetForTableSwitch(): void {
  resetFilters()
}

// onSwitchTable 用户通过工具栏切换器换表。
function onSwitchTable(): void {
  resetForTableSwitch()
  void fetchPage()
}

// 外部(树)切换表时同步内部表名并重拉。
watch(
  () => props.table,
  (t) => {
    if (t === currentTable.value) return
    currentTable.value = t
    resetForTableSwitch()
    void fetchPage()
  },
)

// 清空数据:文案包含表名与引擎;Distributed 引擎追加 ON CLUSTER 提示。
const truncateMessage = computed(() => {
  const base = `确认清空表「${currentTable.value}」(引擎 ${engine.value || '未知'})的全部数据？此操作不可恢复。`
  return isDistributed.value ? `${base}将在集群所有节点执行(ON CLUSTER)。` : base
})

async function doTruncate(): Promise<void> {
  confirmTruncate.value = false
  try {
    await getApi().chTruncateTable({
      connection_id: props.connectionId,
      database: props.database,
      table: currentTable.value,
      on_cluster: isDistributed.value ? true : undefined,
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

function cellTitle(v: string | null): string {
  return v ?? ''
}

// colTitle 列头悬停提示:排序说明 + 类型;comment 非空时追加描述。
function colTitle(c: CHColumn): string {
  const base = `按 ${c.name} 排序 · ${c.type}`
  return c.comment ? `${base}\n${c.comment}` : base
}

// --- 单元格行内编辑:双击 → 输入 → 回车 → 预览确认 → 执行并刷新。 ---
// (isEditing/startEdit/cancelEdit/focusEditor 来自 useCellEdit 组合式。)

// buildTarget 组装更新请求:set 为编辑后的值(空串=写 NULL);where 一律用
// 编辑前的原值快照——有主键仅取主键列,否则整行所有列兜底定位。
function buildTarget(ed: { row: number; col: number }) {
  const cols = columns.value
  const originalRow = rows.value[ed.row] ?? []
  const edited = cols[ed.col]
  const whereNames = primaryKey.value.length > 0 ? primaryKey.value : cols.map((c) => c.name)
  return {
    connection_id: props.connectionId,
    database: props.database,
    table: currentTable.value,
    set: {
      column: edited.name,
      type: edited.type,
      value: editValue.value === '' ? null : editValue.value,
    },
    where: whereNames.map((name) => {
      const i = cols.findIndex((c) => c.name === name)
      return { column: name, type: cols[i]?.type ?? 'String', value: originalRow[i] ?? null }
    }),
  }
}

// submitEdit 回车提交:先固化 target 快照再退出编辑态,cu.request 负责
// 预览并打开确认弹窗;失败时 cu.error 已记录并显示进现有错误区。
async function submitEdit(): Promise<void> {
  const ed = editing.value
  if (!ed) return
  const target = buildTarget(ed)
  cancelEdit()
  try {
    await cu.request(target)
  } catch {
    // cu.error 已承载错误,交由错误区展示。
  }
}

// 确认弹窗开关/错误均来自 cu(嵌套 ref 不被模板自动解包,转一层 computed)。
const cellUpdateOpen = computed(() => cu.confirmOpen.value)
const cellUpdateError = computed(() => cu.error.value)

// --- 按行删除:行首危险按钮 → 按主键原值预览 DELETE → 确认执行并刷新。 ---
// 删除行 API:已在 Api 接口声明为可选成员,可选链调用;预览侧把「绑定缺失」
// 归一为统一错误(与既有文案一致),三段式状态机由 useRowDelete 组合式承担。
const {
  error: delError,
  confirmOpen: confirmDeleteOpen,
  message: deleteMessage,
  request: requestDeleteRow,
  confirm: onDeleteRowConfirm,
  cancel: onDeleteRowCancel,
} = useRowDelete<CHDeleteRowRequest>({
  preview: async (target) => {
    const res = (await getApi().chPreviewDeleteRow?.(target)) as CHDeleteRowPreview | undefined
    if (!res) throw new Error('当前环境不支持按行删除')
    return res
  },
  execute: async (target) => {
    await getApi().chDeleteRow?.(target)
  },
  refetch: fetchPage,
})

// 仅当有主键(排序键)时开放删除;无主键无法唯一定位行。
const canDelete = computed(() => primaryKey.value.length > 0)

// buildDeleteTarget 组装删除请求:where 只用主键列的当前行原值快照,列类型
// 元数据来源与单元格编辑 buildTarget 完全一致(null 保持 null,后端拼 IS NULL)。
function buildDeleteTarget(ri: number): CHDeleteRowRequest {
  const cols = columns.value
  const row = rows.value[ri] ?? []
  return {
    connection_id: props.connectionId,
    database: props.database,
    table: currentTable.value,
    where: primaryKey.value.map((name) => {
      const i = cols.findIndex((c) => c.name === name)
      return { column: name, type: cols[i]?.type ?? 'String', value: row[i] ?? null }
    }),
  }
}

// askDeleteRow 点击行首删除:无主键直接忽略;预览/确认/执行交给组合式。
async function askDeleteRow(ri: number): Promise<void> {
  if (!canDelete.value) return
  await requestDeleteRow(buildDeleteTarget(ri))
}

// 弹窗文案:ALTER 语句全文 + 匹配行数;将波及多行时追加警示。
const cellUpdateMessage = computed(() => {
  const n = Number(cu.matchedRows.value ?? 0)
  const lines = [`将执行以下语句:\n${cu.statement.value}`, `匹配 ${n} 行`]
  if (n > 1) lines.push(`将同时更新 ${n} 行,请确认`)
  return lines.join('\n')
})

// onCellUpdateConfirm 确认执行:成功后刷新当前页;失败由 cu.error 展示。
async function onCellUpdateConfirm(): Promise<void> {
  try {
    await cu.confirm()
  } catch {
    return
  }
  if (cu.error.value) return
  void fetchPage()
}
</script>

<template>
  <div class="ch-browser" data-test="ch-table-browser">
    <div class="summary" data-test="ch-summary">
      <span class="mono table-name" data-test="ch-summary-table">{{ props.database }}.{{ currentTable }}</span>
      <span class="sep">·</span>
      <span data-test="ch-summary-engine">{{ engine || '—' }}</span>
      <span class="sep">·</span>
      <span data-test="ch-summary-rows">≈ {{ totalRows.toLocaleString() }} 行</span>
    </div>

    <div class="toolbar">
      <select
        v-model="currentTable"
        class="table-switcher"
        data-test="ch-table-switcher"
        title="切换到同库其它表"
        @change="onSwitchTable"
      >
        <option v-for="t in tableOptions" :key="t.name" :value="t.name">{{ t.name }}</option>
      </select>
      <input
        v-model="where"
        class="where-input"
        type="search"
        data-test="ch-where"
        placeholder="WHERE 条件,如 col > 1"
        autocapitalize="off"
        autocorrect="off"
        autocomplete="off"
        spellcheck="false"
        @keydown.enter="applyWhere"
      />
      <button class="btn ghost" type="button" data-test="btn-ch-apply" :disabled="loading" @click="applyWhere">过滤</button>
      <button class="btn ghost" type="button" data-test="btn-ch-refresh" :disabled="loading" @click="refresh">
        {{ loading ? '加载中…' : '刷新' }}
      </button>
      <button class="btn ghost danger" type="button" data-test="btn-ch-truncate" @click="confirmTruncate = true">清空数据</button>
    </div>

    <div v-if="error || cellUpdateError || delError" class="msg err" data-test="ch-error">{{ error || cellUpdateError || delError }}</div>

    <div v-if="columns.length > 0 && rows.length === 0 && !loading" class="table-wrap">
      <div class="fields-panel" data-test="ch-fields-panel">
        <div class="fields-title" data-test="ch-fields-title">表结构(共 {{ columns.length }} 字段)</div>
        <div v-for="c in columns" :key="c.name" class="fields-row" data-test="ch-field-row">
          <span class="field-name" data-test="ch-field-name">{{ c.name }}</span>
          <span class="field-type" data-test="ch-field-type">{{ c.type }}</span>
          <span v-if="c.comment" class="field-comment" data-test="ch-field-comment">{{ c.comment }}</span>
        </div>
      </div>
      <div class="empty" data-test="ch-grid-empty">
        <div class="empty-icon">◌</div>
        <div>该表暂无数据</div>
        <div class="empty-hint">数据写入后点击「刷新」查看</div>
      </div>
    </div>

    <div v-else class="table-wrap">
      <!-- 共享表格网格:列头/行删除/行内编辑/空态由 TableDataGrid 承担
           (CH 无主键角标,列注释行保留)。 -->
      <TableDataGrid
        v-model:edit-value="editValue"
        test-prefix="ch"
        :columns="columns"
        :rows="rows"
        :loading="loading"
        :order-by="orderBy"
        :asc="asc"
        row-actions
        :row-action-enabled="canDelete"
        :editing="editing"
        :col-title="colTitle"
        :cell-title="cellTitle"
        @sort="sortBy"
        @start-edit="startEdit"
        @submit-edit="submitEdit"
        @cancel-edit="cancelEdit"
        @row-delete="askDeleteRow"
      />
    </div>

    <div class="pager" data-test="ch-pager">
      <button class="btn ghost" type="button" data-test="btn-ch-prev" :disabled="offset === 0 || loading" @click="prevPage">上一页</button>
      <span class="page-label" data-test="ch-page-label">第 {{ page }} 页</span>
      <button class="btn ghost" type="button" data-test="btn-ch-next" :disabled="rows.length < PAGE_SIZE || loading" @click="nextPage">下一页</button>
    </div>

    <ConfirmDialog
      :show="confirmTruncate"
      :message="truncateMessage"
      confirm-text="清空"
      @confirm="doTruncate"
      @cancel="confirmTruncate = false"
    />

    <!-- 单元格更新确认:展示将执行的 ALTER 语句全文与匹配行数。 -->
    <ConfirmDialog
      :show="cellUpdateOpen"
      :message="cellUpdateMessage"
      confirm-text="执行"
      @confirm="onCellUpdateConfirm"
      @cancel="cu.cancel()"
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

<style scoped>
.ch-browser { padding: 16px; color: var(--text); font-family: var(--font); }
.summary {
  display: flex; gap: 8px; flex-wrap: wrap; align-items: center;
  padding: 8px 12px;
  background: var(--bg-subtle); border: 1px solid var(--border); border-radius: 8px;
  font-size: 12px; color: var(--text-secondary);
}
.table-name { font-weight: 600; color: var(--text); }
.sep { color: var(--text-tertiary); }
.toolbar { display: flex; gap: 8px; align-items: center; flex-wrap: wrap; margin-top: 10px; }
.table-switcher {
  min-width: 160px; box-sizing: border-box;
  background: var(--bg-elevated); border: 1px solid var(--border); color: var(--text);
  border-radius: 7px; padding: 5px 9px; font-size: 12px;
  transition: border-color 0.15s ease, box-shadow 0.15s ease;
}
.table-switcher:focus { outline: none; border-color: var(--accent); box-shadow: 0 0 0 3px var(--accent-soft); }
.where-input {
  flex: 1; min-width: 220px; max-width: 420px;
  background: var(--bg-subtle); border: 1px solid var(--border); color: var(--text);
  border-radius: 7px; padding: 6px 9px; font-size: 13px;
}
.where-input:focus { outline: none; border-color: var(--accent); background: var(--bg-elevated); box-shadow: 0 0 0 3px var(--accent-soft); }
.msg { padding: 8px 10px; font-size: 13px; border-radius: 7px; margin-top: 8px; }
.msg.err { background: var(--danger-soft); color: var(--danger); }
.table-wrap {
  margin-top: 10px; overflow: auto; max-height: calc(100vh - 320px);
  border: 1px solid var(--border); border-radius: 10px; background: var(--bg-elevated);
}
.mono { font-family: var(--mono); }
.empty { text-align: center; color: var(--text-tertiary); padding: 28px 24px; }
.empty-icon { font-size: 26px; margin-bottom: 6px; }
.empty-hint { font-size: 11px; margin-top: 4px; color: var(--text-tertiary); }
/* 空表字段结构面板:每字段一行(名称 + 类型徽标 + 可选描述)。 */
.fields-panel { padding: 0 0 4px; }
.fields-title {
  padding: 10px 12px; font-size: 12px; font-weight: 600;
  color: var(--text-secondary); border-bottom: 1px solid var(--border);
}
.fields-row {
  display: flex; align-items: baseline; gap: 10px;
  padding: 8px 12px; border-bottom: 1px solid var(--border);
}
.fields-row:last-child { border-bottom: none; }
.field-name { font-weight: 600; font-family: var(--mono); font-size: 13px; color: var(--text); }
.field-type {
  flex: none; font-size: 10px; font-family: var(--mono);
  background: var(--bg-hover); border-radius: 4px; padding: 1px 5px;
  color: var(--text-secondary);
}
.field-comment {
  flex: 1; min-width: 0; font-size: 12px; color: var(--text-tertiary);
  overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
}
.pager { display: flex; align-items: center; gap: 10px; margin-top: 10px; }
.page-label { font-size: 12px; color: var(--text-secondary); }
.btn { border-radius: 7px; padding: 6px 13px; font-size: 13px; cursor: pointer; border: 1px solid transparent; }
.btn:disabled { opacity: 0.5; cursor: not-allowed; }
.btn.ghost { background: transparent; color: var(--text); border-color: var(--border-strong); }
.btn.ghost:hover:not(:disabled) { background: var(--bg-hover); }
.btn.danger { color: var(--danger); border-color: var(--danger); }
.btn.danger:hover:not(:disabled) { background: var(--danger-soft); }
</style>
