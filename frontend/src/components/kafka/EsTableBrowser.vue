<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import TableDataGrid from '@/components/common/TableDataGrid.vue'
import { useToastStore } from '@/store/toast'
import { buildDocSkeleton } from '@/utils/esDocSkeleton'
import { usePagedTable } from '@/composables/usePagedTable'
import { useCellEdit } from '@/composables/useCellEdit'
import { getApi } from '@/api/client'

const props = defineProps<{ connectionId: string; index: string }>()

// 新增文档成功后的全局轻提示。
const toast = useToastStore()

// 每页行数与后端 ESPageRows 的 limit 对齐(后端换算为 from/size)。
const PAGE_SIZE = 200
// 超长单元格客户端截断展示(完整值保留在 title 提示中)。
const CELL_TRUNCATE = 200

interface EsColumn {
  name: string
  type: string
  comment?: string
}

interface EsPageRowsResult {
  columns: EsColumn[]
  rows: (string | null)[][]
  total_rows: number
  primary_key: string[]
  engine: string
}

// Es* 系列绑定调用统一收口到 api/client.ts 适配层(WailsApi 按 wailsjs
// 绑定名转发,req 为契约形状原样;测试在 wailsjs 模块层 mock,命中同一转发)。
// 绑定在 Api 接口上为可选成员,调用方用可选链访问;缺绑定按请求失败处理。

const columns = ref<EsColumn[]>([])
const rows = ref<(string | null)[][]>([])
const engine = ref('')
const totalRows = ref(0)
const loading = ref(false)
const error = ref<string | null>(null)

// 首列 _id(columns[0].type === '_id')承载文档标识:只读、不可排序,
// 行首操作按钮(JSON 编辑/删除)也挂在该列;缺失时整表只读。
const hasIdColumn = computed(() => columns.value[0]?.type === '_id')

// --- 分页/过滤/排序:共用组合式;refresh 仍走 ES 特有的显式 _refresh 后取数。 ---
// 过滤语义两级——含冒号视为 query_string 高级语法(字段:值、AND/OR),否则
// 按普通关键词全字段匹配(后端 multi_match lenient),避免 query_string 把
// `2026-08-21`、`abc-def` 这类值按操作符拆解导致结果错乱(后端语义,前端
// 原样下发 appliedWhere)。
const {
  where,
  appliedWhere,
  orderBy,
  asc,
  offset,
  page,
  applyWhere,
  sortBy,
  nextPage,
  prevPage,
  resetFilters,
} = usePagedTable({ pageSize: PAGE_SIZE, refetch: fetchPage })

// --- 行内编辑态:共用组合式(_id 首列只读经 canStart 拦截)。 ---
const {
  editing,
  editValue,
  isEditing,
  startEdit,
  cancelEdit,
  focusEditor,
  discard: discardEdit,
} = useCellEdit({
  loading,
  rows,
  columns,
  canStart: (_ri, ci) => hasIdColumn.value && ci !== 0,
})

// 编辑框行数:与展示态一致——单元格按 420px 宽 break-all 换行,按展示
// 长度(截断后约 200 字符)估算行数;超过 8 行编辑框内滚动。
const EDITOR_CHARS_PER_LINE = 48
const EDITOR_MAX_ROWS = 8
const editorRows = computed(() => {
  if (!editing.value) return 1
  const shown = cellDisplay(editValue.value).length
  return Math.min(EDITOR_MAX_ROWS, Math.max(1, Math.ceil(shown / EDITOR_CHARS_PER_LINE)))
})

// mappingFields 映射字段(剔除 _id 列):新增文档骨架的生成来源。
const mappingFields = computed(() =>
  columns.value.filter((c) => c.type !== '_id').map((c) => ({ name: c.name, type: c.type })),
)

function errMsg(e: unknown): string {
  return e instanceof Error ? e.message : String(e)
}

async function fetchPage(): Promise<void> {
  loading.value = true
  error.value = null
  // 换页/刷新后行集合将重建,未完成的行内编辑一并丢弃。
  discardEdit()
  try {
    const res = await getApi().esPageRows?.({
      connection_id: props.connectionId,
      index: props.index,
      where: appliedWhere.value || undefined,
      order_by: orderBy.value || undefined,
      asc: asc.value,
      limit: PAGE_SIZE,
      offset: offset.value,
    })
    if (!res) throw new Error('当前环境不支持 ES 索引浏览')
    // wire 形状防御:后端异常/老版本可能给出 null 或缺字段;模板对
    // columns/rows 按数组、totalRows 按数字直接使用,任何 null/undefined
    // 都会在渲染期抛 TypeError 并中断整个调度队列(WKWebView 表现为白屏)。
    columns.value = res.columns ?? []
    rows.value = res.rows ?? []
    engine.value = res.engine ?? ''
    const total = Number(res.total_rows)
    totalRows.value = Number.isFinite(total) ? total : 0
  } catch (e) {
    error.value = errMsg(e)
  } finally {
    loading.value = false
  }
}

onMounted(() => {
  void fetchPage()
})

// 外部(树/Layout)切换索引时重置过滤/排序/分页状态并重拉第一页。
watch(
  () => props.index,
  () => {
    resetFilters()
    void fetchPage()
  },
)

// refreshThenFetch 手动刷新与写后自动刷新共用:先 best-effort 显式 _refresh
// (POST /{index}/_refresh;失败忽略——最坏情况只是数据可见性延迟到 ES 自身
// 刷新周期),再立即重取当前页,使写入/删除无需等待 refresh_interval 即可见。
async function refreshThenFetch(): Promise<void> {
  try {
    await getApi().esRefreshIndex?.({ connection_id: props.connectionId, index: props.index })
  } catch {
    // best-effort:刷新失败不阻断取数,也不进错误区。
  }
  await fetchPage()
}

function refresh(): void {
  void refreshThenFetch()
}

// sortByCol 点击列头排序的入口:首列 _id 不可排序直接忽略;其余列交给
// 组合式的 sortBy(同一列翻转方向,新列默认升序;回第一页)。
function sortByCol(col: string, ci: number): void {
  if (ci === 0 && hasIdColumn.value) return
  sortBy(col)
}

// (nextPage/prevPage 来自 usePagedTable 组合式。)

// --- 单元格渲染:nil → NULL(浅色);超长值截断(浅色标注 + title 全文)。 ---

function isTruncated(v: string | null): boolean {
  return v !== null && v.length > CELL_TRUNCATE
}

function cellDisplay(v: string | null): string {
  if (v === null) return 'NULL'
  return isTruncated(v) ? `${v.slice(0, CELL_TRUNCATE)}…` : v
}

// cellTitle 数据单元格悬停提示:_id 列固定提示;缺 _id 列整表只读;
// 其余展示完整原值。
function cellTitle(v: string | null, ci: number): string {
  if (ci === 0 && hasIdColumn.value) return '文档 _id'
  if (!hasIdColumn.value) return '缺少文档 _id,不支持编辑'
  return v ?? ''
}

// colTitle 列头悬停提示:_id 列只读说明;其余为排序说明 + 类型,
// comment 非空时追加描述。
function colTitle(c: EsColumn, ci: number): string {
  if (ci === 0 && hasIdColumn.value) return '文档 _id'
  const base = `按 ${c.name} 排序 · ${c.type}`
  return c.comment ? `${base}\n${c.comment}` : base
}

// --- 单元格行内编辑:双击 → 输入 → 回车 → 确认 → 执行并刷新。 ---
// (isEditing/startEdit/cancelEdit 来自 useCellEdit 组合式;_id 首列只读。)

// rowId 取行文档 _id(首列值);无 _id 列时返回 null。
function rowId(ri: number): string | null {
  if (!hasIdColumn.value) return null
  return rows.value[ri]?.[0] ?? null
}

// --- 字段级更新预览/确认/执行状态机:ES 无独立预览接口,确认文案由前端
// 按 _update 语义构造;因本次只允许新建组件与测试两个文件,状态机内联。 ---

// 预览通过后暂存的待执行目标,确认时原样发给执行。
let pendingEdit: { id: string; column: string; value: string | null } | null = null
const confirmEdit = ref(false)
const editError = ref<string | null>(null)
const editPreview = ref('')

// submitEdit 回车提交:先固化新值快照再退出编辑态,随后按 _update 语义
// 生成预览文案并打开确认弹窗(空串=写 null)。
function submitEdit(): void {
  const ed = editing.value
  if (!ed) return
  const col = columns.value[ed.col]
  const id = rowId(ed.row)
  const value = editValue.value === '' ? null : editValue.value
  cancelEdit()
  if (!col || id === null) return
  pendingEdit = { id, column: col.name, value }
  editPreview.value = `POST /${props.index}/_update/${id} ${JSON.stringify({ doc: { [col.name]: value } })}`
  editError.value = null
  confirmEdit.value = true
}

// onEditConfirm 确认执行:成功后关闭弹窗并刷新当前页;失败弹窗保持
// 打开(MySQL 行为一致),错误显示在弹窗内(用户点确认后立刻可见),
// 并同步进页面错误区——即使取消关闭弹窗,服务端原因也不会消失。
async function onEditConfirm(): Promise<void> {
  const target = pendingEdit
  if (!target) {
    confirmEdit.value = false
    return
  }
  try {
    await getApi().esUpdateCell?.({ connection_id: props.connectionId, index: props.index, id: target.id, column: target.column, value: target.value })
    pendingEdit = null
    confirmEdit.value = false
    editError.value = null
    void refreshThenFetch()
  } catch (e) {
    editError.value = errMsg(e)
    error.value = errMsg(e)
  }
}

// onEditCancel 取消:关闭弹窗并清空全部状态(含待执行目标)。
function onEditCancel(): void {
  pendingEdit = null
  confirmEdit.value = false
  editPreview.value = ''
  editError.value = null
}

// --- 整文档 JSON 弹窗:编辑(查看/替换已有文档)与新增(骨架草稿创建,
//     可指定 _id 覆盖)双模式,共用 jsonOpen/jsonDraft 状态。 ---

const jsonOpen = ref(false)
const jsonSaving = ref(false)
const jsonId = ref('')
const jsonDraft = ref('')
// 弹窗模式:edit = 编辑已有文档(ESPutDoc);create = 新增(EsCreateDoc,
// _id 留空自动生成、非空指定写入/覆盖)。
const jsonMode = ref<'edit' | 'create'>('edit')
// 新增模式下的 _id 输入草稿(与编辑模式的 jsonId 分离,避免混淆)。
const jsonNewId = ref('')
// 弹窗内错误:前端 JSON 校验失败与服务端错误统一展示在此,并同步页面错误区。
const jsonError = ref<string | null>(null)

// prettyJson 将后端返回的 doc_json 格式化为两空格缩进;解析失败原样展示。
function prettyJson(s: string): string {
  try {
    return JSON.stringify(JSON.parse(s), null, 2)
  } catch {
    return s
  }
}

async function openJson(ri: number): Promise<void> {
  const id = rowId(ri)
  if (id === null || loading.value || jsonOpen.value) return
  try {
    const res = (await getApi().esGetDoc?.({ connection_id: props.connectionId, index: props.index, id })) as { doc_json?: string } | undefined
    if (!res) throw new Error('当前环境不支持读取文档')
    jsonMode.value = 'edit'
    jsonId.value = id
    jsonDraft.value = prettyJson(res.doc_json ?? '')
    jsonNewId.value = ''
    jsonError.value = null
    jsonOpen.value = true
  } catch (e) {
    error.value = errMsg(e)
  }
}

// openCreateDoc 进入新增模式:draft 由映射字段骨架生成(数值 0/布尔
// false/其余 null,object 由点路径子字段成形),_id 留给用户选择。
function openCreateDoc(): void {
  if (jsonOpen.value) return
  jsonMode.value = 'create'
  jsonId.value = ''
  jsonNewId.value = ''
  jsonDraft.value = buildDocSkeleton(mappingFields.value)
  jsonError.value = null
  jsonOpen.value = true
}

// duplicateDoc 另存为新文档:保留当前弹窗内容(格式化 JSON),清空 _id
// 进入新增模式,后续保存走 EsCreateDoc(留空自动生成 / 填写指定新 _id)。
function duplicateDoc(): void {
  if (jsonMode.value !== 'edit') return
  jsonMode.value = 'create'
  jsonNewId.value = ''
  jsonError.value = null
}

// 弹窗标题:编辑态含文档 _id;新增态固定「新增文档」。
const jsonTitle = computed(() => (jsonMode.value === 'create' ? '新增文档' : '编辑文档 JSON'))

// 保存按钮文案/提示:编辑态「保存」;新增态 _id 留空「创建」、填写
// 「创建/覆盖」(title 注明同 _id 已存在会被整体覆盖)。
const saveBtnText = computed(() => {
  if (jsonSaving.value) return '保存中…'
  if (jsonMode.value === 'create') return jsonNewId.value.trim() ? '创建/覆盖' : '创建'
  return '保存'
})

const saveBtnTitle = computed(() => {
  if (jsonMode.value !== 'create') return undefined
  const id = jsonNewId.value.trim()
  return id
    ? `将以 PUT /${props.index}/_doc/${id} 写入:同 _id 文档已存在时被整体覆盖`
    : `POST /${props.index}/_doc,_id 由 Elasticsearch 自动生成`
})

// jsonHint 弹窗底部提示:编辑态沿用整文档替换说明;新增态说明 _id 语义。
const jsonHint = computed(() => {
  if (jsonMode.value === 'edit') {
    return `保存将以该 JSON 整体替换文档(PUT /${props.index}/_doc/${jsonId.value})`
  }
  return `留空 _id 自动生成(POST /${props.index}/_doc);填写 _id 时同 ID 文档将被整体覆盖(PUT /${props.index}/_doc/<_id>)`
})

// saveJson 双模式保存:edit = 整文档替换(ESPutDoc);create = 先前端校验
// JSON 语法(json.Valid 等价,非法不发请求),_id 留空走 EsCreateDoc
// (POST 自动生成),非空带 id(后端 PUT,同 _id 已存在则覆盖)。
// 失败弹窗保持,错误显示在弹窗内并同步页面错误区。
async function saveJson(): Promise<void> {
  if (!jsonOpen.value || jsonSaving.value) return
  if (jsonMode.value === 'edit' && !jsonId.value) return
  jsonSaving.value = true
  jsonError.value = null
  try {
    if (jsonMode.value === 'create') {
      try {
        JSON.parse(jsonDraft.value)
      } catch (pe) {
        throw new Error(`JSON 语法错误:${errMsg(pe)}`)
      }
      const id = jsonNewId.value.trim()
      if (id) {
        await getApi().esCreateDoc?.({ connection_id: props.connectionId, index: props.index, id, doc_json: jsonDraft.value })
      } else {
        await getApi().esCreateDoc?.({ connection_id: props.connectionId, index: props.index, doc_json: jsonDraft.value })
      }
    } else {
      await getApi().esPutDoc?.({ connection_id: props.connectionId, index: props.index, id: jsonId.value, doc: jsonDraft.value })
    }
    jsonOpen.value = false
    if (jsonMode.value === 'create') toast.show('文档已创建')
    void refreshThenFetch()
  } catch (e) {
    jsonError.value = errMsg(e)
    error.value = errMsg(e)
  } finally {
    jsonSaving.value = false
  }
}

function closeJson(): void {
  jsonOpen.value = false
  jsonMode.value = 'edit'
  jsonId.value = ''
  jsonNewId.value = ''
  jsonDraft.value = ''
  jsonError.value = null
}

// --- 删除文档:行首 ✕ → 危险确认(含 _id)→ 执行并刷新当前页。 ---

const confirmDelete = ref(false)
const deleteId = ref('')

const deleteMessage = computed(() => `确认删除文档 ${deleteId.value}?此操作不可恢复。`)

function askDelete(ri: number): void {
  const id = rowId(ri)
  if (id === null || loading.value) return
  deleteId.value = id
  confirmDelete.value = true
}

async function doDelete(): Promise<void> {
  confirmDelete.value = false
  const id = deleteId.value
  deleteId.value = ''
  if (!id) return
  try {
    await getApi().esDeleteDoc?.({ connection_id: props.connectionId, index: props.index, id })
    void refreshThenFetch()
  } catch (e) {
    error.value = errMsg(e)
  }
}

// --- 清空索引:危险确认(_delete_by_query)→ 执行并回第一页刷新。 ---

const confirmClear = ref(false)

const clearMessage = computed(
  () => `将删除索引 ${props.index} 的全部文档(_delete_by_query),不可恢复`,
)

async function doClear(): Promise<void> {
  confirmClear.value = false
  try {
    await getApi().esDeleteByQuery?.({ connection_id: props.connectionId, index: props.index })
    offset.value = 0
    await refreshThenFetch()
  } catch (e) {
    error.value = errMsg(e)
  }
}
</script>

<template>
  <div class="es-browser" data-test="es-table-browser">
    <div class="summary" data-test="es-summary">
      <span class="mono index-name" data-test="es-summary-index">{{ props.index }}</span>
      <span class="sep">·</span>
      <span data-test="es-summary-engine">{{ engine || '—' }}</span>
      <span class="sep">·</span>
      <span data-test="es-summary-rows">≈ {{ totalRows.toLocaleString() }} 行</span>
    </div>

    <div class="toolbar">
      <input
        v-model="where"
        class="where-input"
        type="search"
        data-test="es-where"
        placeholder="关键词,如 20260821;高级语法用 字段:值(如 level:ERROR AND app:pay)"
        autocapitalize="off"
        autocorrect="off"
        autocomplete="off"
        spellcheck="false"
        title="不含冒号 = 全字段关键词匹配;含冒号 = query_string 高级语法(字段:值,支持 AND/OR/通配符)"
        @keydown.enter="applyWhere"
      />
      <button class="btn ghost" type="button" data-test="btn-es-apply" :disabled="loading" @click="applyWhere">过滤</button>
      <button class="btn ghost" type="button" data-test="es-refresh" :disabled="loading" @click="refresh">
        {{ loading ? '加载中…' : '刷新' }}
      </button>
      <button class="btn ghost" type="button" data-test="es-doc-create" title="新增文档(按映射字段生成 JSON 骨架)" @click="openCreateDoc">➕ 新增文档</button>
      <button class="btn ghost danger" type="button" data-test="btn-es-clear" @click="confirmClear = true">清空文档</button>
    </div>

    <div v-if="error || editError" class="msg err" data-test="es-error">{{ error || editError }}</div>

    <!-- 兜底:无列信息(老后端/异常 wire 形状)时仅渲染空态。正常情况
         后端分页恒返回 _id + 映射列,走下方数据表分支;不再渲染竖排字段
         面板(映射字段已作为表头可见,与 MySQL 浏览器行为一致)。 -->
    <div v-if="!loading && columns.length === 0" class="table-wrap">
      <div class="empty" data-test="es-grid-empty">
        <div class="empty-icon">◌</div>
        <div>该索引暂无文档</div>
        <div class="empty-hint">数据写入后点击「刷新」查看</div>
      </div>
    </div>

    <div v-else class="table-wrap">
      <!-- 共享表格网格:ES 特有部分——_id 首列经 leading-cell 插槽挂 JSON/删除
           按钮,textarea 编辑器随内容多行,_id 列头不可排序,空态文案用插槽。 -->
      <TableDataGrid
        v-model:edit-value="editValue"
        test-prefix="es"
        :columns="columns"
        :rows="rows"
        :loading="loading"
        :order-by="orderBy"
        :asc="asc"
        :leading-column="hasIdColumn"
        nosort-first-column
        editor-tag="textarea"
        :editor-rows="editorRows"
        :editing="editing"
        :cell-readonly="(ci) => ci === 0 && hasIdColumn"
        :col-title="colTitle"
        :cell-title="cellTitle"
        @sort="sortByCol"
        @start-edit="startEdit"
        @submit-edit="submitEdit"
        @cancel-edit="cancelEdit"
      >
        <template #leading-cell="{ cell, rowIndex }">
          <div class="id-cell">
            <button
              class="mini-btn"
              type="button"
              :data-test="`es-doc-json-${rowIndex}`"
              title="编辑整文档 JSON"
              @click="openJson(rowIndex)"
            >
              JSON
            </button>
            <button
              class="mini-btn danger"
              type="button"
              :data-test="`es-doc-delete-${rowIndex}`"
              title="删除文档"
              @click="askDelete(rowIndex)"
            >
              ✕
            </button>
            <span class="id-text" data-test="es-id-value">{{ cell ?? '' }}</span>
          </div>
        </template>
        <template #empty>
          <td :colspan="columns.length">
            <div class="empty" data-test="es-grid-empty">
              <div class="empty-icon">◌</div>
              <div>无匹配文档</div>
              <div class="empty-hint">数据写入后点击「刷新」查看</div>
            </div>
          </td>
        </template>
      </TableDataGrid>
    </div>

    <div class="pager" data-test="es-pager">
      <button class="btn ghost" type="button" data-test="btn-es-prev" :disabled="offset === 0 || loading" @click="prevPage">上一页</button>
      <span class="page-label" data-test="es-page-label">第 {{ page }} 页</span>
      <button class="btn ghost" type="button" data-test="btn-es-next" :disabled="rows.length < PAGE_SIZE || loading" @click="nextPage">下一页</button>
    </div>

    <!-- 清空索引:危险操作,文案含索引名与 _delete_by_query。 -->
    <ConfirmDialog
      :show="confirmClear"
      :message="clearMessage"
      confirm-text="清空"
      danger
      @confirm="doClear"
      @cancel="confirmClear = false"
    />

    <!-- 删除文档:危险操作,文案含文档 _id。 -->
    <ConfirmDialog
      :show="confirmDelete"
      :message="deleteMessage"
      confirm-text="删除"
      danger
      @confirm="doDelete"
      @cancel="confirmDelete = false"
    />

    <!-- 字段更新确认:展示将执行的 _update 语义。 -->
    <ConfirmDialog
      :show="confirmEdit"
      :message="editError ? `将执行以下更新:\n${editPreview}\n\n执行失败:\n${editError}` : `将执行以下更新:\n${editPreview}`"
      confirm-text="更新"
      :danger="false"
      @confirm="onEditConfirm"
      @cancel="onEditCancel"
    />

    <!-- 整文档 JSON 弹窗:编辑模式保存即整文档替换(PUT _doc);新增模式
         由映射字段骨架起步,_id 留空自动生成 / 填写指定写入(覆盖)。 -->
    <Teleport to="body">
      <div v-if="jsonOpen" class="modal-backdrop" data-test="es-doc-json-modal" @click.self="closeJson">
        <div class="modal json-modal">
          <div class="modal-header">
            <span class="modal-title" data-test="es-doc-json-title">
              <template v-if="jsonMode === 'create'">{{ jsonTitle }}</template>
              <template v-else>{{ jsonTitle }} <span class="mono doc-id">{{ jsonId }}</span></template>
            </span>
            <button class="modal-close" type="button" data-test="es-doc-json-close" @click="closeJson">✕</button>
          </div>
          <div class="modal-body">
            <div v-if="jsonError" class="msg err json-error" data-test="es-json-error">{{ jsonError }}</div>
            <div v-if="jsonMode === 'create'" class="doc-id-row">
              <label class="doc-id-label" for="es-doc-create-id">_id</label>
              <input
                id="es-doc-create-id"
                v-model="jsonNewId"
                class="doc-id-input"
                type="text"
                data-test="es-doc-create-id"
                placeholder="留空自动生成"
                autocapitalize="off"
                autocorrect="off"
                autocomplete="off"
                spellcheck="false"
                title="留空 = POST 自动生成;填写 = PUT 指定写入,同 _id 已存在则覆盖"
              />
            </div>
            <textarea
              v-model="jsonDraft"
              class="json-editor"
              data-test="es-doc-json-editor"
              rows="14"
              autocapitalize="off"
              autocorrect="off"
              autocomplete="off"
              spellcheck="false"
            ></textarea>
            <div class="json-hint">{{ jsonHint }}</div>
          </div>
          <div class="modal-footer">
            <button
              v-if="jsonMode === 'edit'"
              class="btn ghost"
              type="button"
              data-test="es-doc-duplicate"
              title="以当前内容另存为新文档(_id 留空自动生成)"
              @click="duplicateDoc"
            >
              另存为新文档
            </button>
            <button class="btn ghost" type="button" data-test="es-doc-json-cancel" @click="closeJson">取消</button>
            <button class="btn primary" type="button" data-test="es-doc-json-save" :disabled="jsonSaving" :title="saveBtnTitle" @click="saveJson">
              {{ saveBtnText }}
            </button>
          </div>
        </div>
      </div>
    </Teleport>
  </div>
</template>

<style scoped>
.es-browser { padding: 16px; color: var(--text); font-family: var(--font); }
.summary {
  display: flex; gap: 8px; flex-wrap: wrap; align-items: center;
  padding: 8px 12px;
  background: var(--bg-subtle); border: 1px solid var(--border); border-radius: 8px;
  font-size: 12px; color: var(--text-secondary);
}
.index-name { font-weight: 600; color: var(--text); }
.sep { color: var(--text-tertiary); }
.toolbar { display: flex; gap: 8px; align-items: center; flex-wrap: wrap; margin-top: 10px; }
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
/* _id 单元格:行首操作按钮 + 文档标识(leading-cell 插槽内容,样式留在入口)。 */
.id-cell { display: flex; align-items: center; gap: 5px; }
.id-text { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; max-width: 220px; }
.mini-btn {
  flex: none; font-size: 10px; line-height: 1;
  padding: 2px 5px; border-radius: 4px; cursor: pointer;
  background: transparent; color: var(--text-secondary);
  border: 1px solid var(--border-strong);
}
.mini-btn:hover { background: var(--bg-hover); color: var(--text); }
.mini-btn.danger { color: var(--danger); border-color: var(--danger); }
.mini-btn.danger:hover { background: var(--danger-soft); }
.mono { font-family: var(--mono); }
.empty { text-align: center; color: var(--text-tertiary); padding: 28px 24px; }
.empty-icon { font-size: 26px; margin-bottom: 6px; }
.empty-hint { font-size: 11px; margin-top: 4px; color: var(--text-tertiary); }
.pager { display: flex; align-items: center; gap: 10px; margin-top: 10px; }
.page-label { font-size: 12px; color: var(--text-secondary); }
/* JSON 编辑弹窗:基座走全局 .modal-backdrop/.modal(层级/模糊对齐 ConfirmDialog)。 */
.modal-backdrop {
  z-index: 100;
  -webkit-backdrop-filter: blur(2px);
  backdrop-filter: blur(2px);
}
.modal {
  width: 380px; max-width: calc(100vw - 48px);
}
.json-modal { width: 640px; }
.modal-header {
  display: flex; align-items: center; justify-content: space-between;
  padding: 14px 18px; border-bottom: 1px solid var(--border);
}
.modal-title { font-size: 14px; font-weight: 600; }
.doc-id { font-size: 12px; color: var(--text-secondary); font-weight: 400; }
.modal-close { background: none; border: none; color: var(--text-tertiary); cursor: pointer; font-size: 14px; padding: 2px 4px; border-radius: 6px; }
.modal-close:hover { color: var(--text); background: var(--bg-hover); }
.modal-body { padding: 16px 18px; }
.modal-footer { display: flex; justify-content: flex-end; gap: 8px; padding: 0 18px 16px; }
.json-editor {
  box-sizing: border-box; width: 100%; min-height: 280px; resize: vertical;
  background: var(--bg-subtle); border: 1px solid var(--border); color: var(--text);
  border-radius: 7px; padding: 8px 10px; font-size: 12px; line-height: 1.5;
  font-family: var(--mono);
}
.json-editor:focus { outline: none; border-color: var(--accent); box-shadow: var(--focus-ring); }
.json-hint { margin-top: 8px; font-size: 11px; color: var(--text-tertiary); }
/* 弹窗内错误:与页面错误区同一视觉语言,置于弹窗体顶部(错误即点可见)。 */
.json-error { margin: 0 0 8px; }
/* 新增模式 _id 行:单行紧凑,等宽字体与文档标识一致。 */
.doc-id-row { display: flex; align-items: center; gap: 8px; margin-bottom: 8px; }
.doc-id-label { font-size: 12px; font-family: var(--mono); color: var(--text-secondary); }
.doc-id-input {
  flex: 1; box-sizing: border-box;
  background: var(--bg-subtle); border: 1px solid var(--border); color: var(--text);
  border-radius: 7px; padding: 5px 9px; font-size: 13px; font-family: var(--mono);
}
.doc-id-input:focus { outline: none; border-color: var(--accent); box-shadow: var(--focus-ring); }
</style>
