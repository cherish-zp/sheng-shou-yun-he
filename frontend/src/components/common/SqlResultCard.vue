<script setup lang="ts">
import { computed, ref, watch, onBeforeUnmount, onMounted, type ComponentPublicInstance } from 'vue'
import { ChevronDown, X } from 'lucide-vue-next'
import { Button } from '@/components/ui/button'
import { useToastStore } from '@/store/toast'
import { buildInsertStatement, type InsertTarget } from '@/utils/insertSql'
import { CSV_MIME, JSONL_MIME, exportCsv, exportJsonl, saveFile } from '@/utils/export'

export interface ResultColumn {
  name: string
  type?: string
}

// 单元格编辑态由父组件持有:draft 为进入编辑时的初值,输入框不受控,
// 回车时把 input.value 原样通过 edit-commit 交回父组件。
export interface EditingCell {
  row: number
  col: number
  draft: string
}

const props = withDefaults(
  defineProps<{
    statement: string
    durationMs?: number | null
    columns: ResultColumn[]
    rows: (string | null)[][]
    /** 单表查询结果才可生成 INSERT;null 时按钮禁用。 */
    insertTarget?: InsertTarget | null
    exportName: string
    error?: string | null
    editing?: EditingCell | null
    /** true → 行首渲染复选框列 + 表头全选(仅当调用方有 insertTarget 时传 true)。 */
    selectable?: boolean
    /** 表的主键列名列表(空/缺省 = 未识别)。 */
    primaryKey?: string[]
    /** 服务端分页的精确总行数(≥0 时头部展示「共 N 条」;null/-1/缺省不展示)。 */
    totalRows?: number | null
    /** 行首「删除该行」能力:hint 返回 null = 可删(title「删除该行」),非空 =
     *  禁用且 title 展示原因;delete 在点击时回调(确认弹窗等逻辑在调用方)。
     *  缺省不渲染删除按钮。 */
    rowDelete?: { hint: (rowIndex: number) => string | null; delete: (rowIndex: number) => void }
  }>(),
  { durationMs: null, insertTarget: null, error: null, editing: null, selectable: false, primaryKey: () => [], totalRows: null, rowDelete: undefined },
)

const emit = defineEmits<{
  (e: 'cell-dblclick', row: number, col: number): void
  (e: 'edit-commit', value: string | null): void
  (e: 'edit-cancel'): void
}>()

const toast = useToastStore()

// —— 头部状态 ——

// 状态点:ok/fail/none 保留为测试锚定类名,配色走语义 token
// (成功 bg-success / 失败 bg-destructive / 无结果 bg-muted-foreground)。
const dotClass = computed(() => {
  if (props.error) return 'fail bg-destructive'
  if (props.durationMs != null) return 'ok bg-success'
  return 'none bg-muted-foreground/50'
})

const metaText = computed(() => {
  const parts: string[] = []
  if (props.durationMs != null) parts.push(`${Math.round(props.durationMs)} ms`)
  // 服务端分页的精确总数(total_rows ≥ 0):千分位展示;无法计数(-1)或
  // 未启用分页(缺省)时只显示本页行数。
  if (props.totalRows != null && props.totalRows >= 0) {
    parts.push(`共 ${props.totalRows.toLocaleString('en-US')} 条`)
  }
  parts.push(`${props.rows.length} 行`)
  return parts.join(' · ')
})

const expanded = ref(false)

// —— 剪贴板(navigator.clipboard 失败/缺失时降级 execCommand)——

async function copyText(text: string, okMsg: string): Promise<void> {
  let ok = false
  try {
    if (navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(text)
      ok = true
    }
  } catch {
    ok = false
  }
  if (!ok) {
    const textarea = document.createElement('textarea')
    textarea.value = text
    textarea.setAttribute('readonly', '')
    textarea.style.position = 'fixed'
    textarea.style.opacity = '0'
    document.body.appendChild(textarea)
    textarea.select()
    try {
      ok = document.execCommand('copy')
    } catch {
      ok = false
    }
    textarea.remove()
  }
  if (ok) toast.show(okMsg)
}

// —— 行选择(勾选行只影响复制 INSERT 的行范围,不参与导出)——

const selected = ref<Set<number>>(new Set())

// 行集变化(重新执行查询/翻页)后旧下标失效,清空选择。
watch(
  () => props.rows,
  () => {
    selected.value = new Set()
  },
)

function toggleRow(index: number): void {
  const next = new Set(selected.value)
  if (next.has(index)) next.delete(index)
  else next.add(index)
  selected.value = next
}

const allSelected = computed(() => props.rows.length > 0 && selected.value.size === props.rows.length)

function toggleAll(): void {
  selected.value = allSelected.value ? new Set() : new Set(props.rows.map((_, i) => i))
}

// —— INSERT 选项(下拉开关,持久化 localStorage)——

const INSERT_OPTS_KEY = 'dbclient-insert-opts'

function loadInsertOpts(): { includePK: boolean; perRow: boolean } {
  const fallback = { includePK: false, perRow: false }
  try {
    const raw = localStorage.getItem(INSERT_OPTS_KEY)
    if (!raw) return fallback
    const parsed = JSON.parse(raw) as Partial<{ includePK: boolean; perRow: boolean }>
    return { includePK: parsed.includePK === true, perRow: parsed.perRow === true }
  } catch {
    return fallback
  }
}

const initialOpts = loadInsertOpts()
const includePK = ref(initialOpts.includePK)
const perRowOpt = ref(initialOpts.perRow)
const insertOptsOpen = ref(false)

// primaryKey 未识别时「包含主键列」强制视为关闭。
const pkIncluded = computed(() => includePK.value && props.primaryKey.length > 0)
const pkToggleTitle = computed(() => (props.primaryKey.length === 0 ? '未识别到主键列' : undefined))

watch([includePK, perRowOpt], ([inc, per]) => {
  try {
    localStorage.setItem(
      INSERT_OPTS_KEY,
      JSON.stringify({ includePK: inc && props.primaryKey.length > 0, perRow: per }),
    )
  } catch {
    // 持久化失败不影响复制功能。
  }
})

function onCopyInsert(): void {
  if (!props.insertTarget) return
  // 行范围:勾选行优先,未勾选取全部。
  const picked = selected.value.size > 0 ? props.rows.filter((_, i) => selected.value.has(i)) : props.rows
  // 列范围:「包含主键列」关闭时剔除主键列,值按行同步裁剪。
  let cols = props.columns
  let outRows = picked
  if (!pkIncluded.value) {
    const pkSet = new Set(props.primaryKey)
    const keep = props.columns.map((col, i) => ({ col, i })).filter((x) => !pkSet.has(x.col.name))
    if (keep.length !== props.columns.length) {
      cols = keep.map((x) => x.col)
      outRows = picked.map((row) => keep.map((x) => row[x.i] ?? null))
    }
  }
  void copyText(
    buildInsertStatement(props.insertTarget, cols, outRows, 1000, { perRow: perRowOpt.value }),
    '已复制 INSERT 语句',
  )
}

// 点面板外收起 INSERT 选项(与 ExportDropdown 的外部点击关闭一致)。
const insertRoot = ref<HTMLElement | null>(null)

function onDocClick(e: MouseEvent): void {
  if (insertOptsOpen.value && insertRoot.value && !insertRoot.value.contains(e.target as Node)) {
    insertOptsOpen.value = false
  }
}

onMounted(() => document.addEventListener('click', onDocClick))
onBeforeUnmount(() => document.removeEventListener('click', onDocClick))

// 行号列不导出/不复制;null 导出为空单元格。
function exportColumns() {
  return props.columns.map((col, i) => ({
    label: col.name,
    value: (row: (string | null)[]) => row[i] ?? '',
  }))
}

function onCopyCsv(): void {
  // exportCsv 的 BOM 供文件导出让 Excel 识别编码;剪贴板场景去掉。
  void copyText(exportCsv(props.rows, exportColumns()).replace(/^\uFEFF/, ''), '已复制 CSV')
}

function onCopyError(): void {
  void copyText(props.error ?? '', '已复制错误信息')
}

async function onExportCsv(): Promise<void> {
  await saveFile(props.exportName, exportCsv(props.rows, exportColumns()), CSV_MIME)
}

async function onExportJsonl(): Promise<void> {
  const jsonlRows = props.rows.map((row) =>
    Object.fromEntries(props.columns.map((col, i) => [col.name, row[i] ?? null])),
  )
  await saveFile(props.exportName, exportJsonl(jsonlRows), JSONL_MIME)
}

// —— 单元格编辑 ——

function isEditing(row: number, col: number): boolean {
  return props.editing != null && props.editing.row === row && props.editing.col === col
}

// —— 编辑框尺寸锁定 ——
// 列宽/行高由单元格内容撑出,换成输入框后不再参与布局计算,尺寸会变。双击
// 瞬间记录 td 的实际像素宽高,textarea 以绝对定位浮层覆盖在原单元格上,
// 尺寸与原数据区域完全一致(行高列宽纹丝不动),全部内容在框内滚动查看。
const cellEditSize = ref<{ width: number; height: number } | null>(null)

function onCellDblclick(row: number, col: number, event: MouseEvent): void {
  const td = event.currentTarget as HTMLElement | null
  cellEditSize.value = td ? { width: td.offsetWidth, height: td.offsetHeight } : null
  emit('cell-dblclick', row, col)
}

const editorStyle = computed(() => {
  const s = cellEditSize.value
  return s && s.width > 0 && s.height > 0
    ? { width: `${s.width}px`, height: `${s.height}px` }
    : undefined
})

// 提交后元素随 editing 置空而卸载,部分浏览器会补发 blur → 用标记吞掉,
// 避免提交后紧跟一次多余的 edit-cancel。
let ignoreBlur = false

function onEditCommit(event: Event): void {
  ignoreBlur = true
  emit('edit-commit', (event.target as HTMLTextAreaElement).value)
}

function onEditBlur(): void {
  if (ignoreBlur) {
    ignoreBlur = false
    return
  }
  emit('edit-cancel')
}

watch(
  () => props.editing,
  () => {
    ignoreBlur = false
    if (props.editing == null) cellEditSize.value = null
  },
)

function focusEditor(el: Element | ComponentPublicInstance | null): void {
  if (el instanceof HTMLInputElement || el instanceof HTMLTextAreaElement) el.focus()
}

// —— 行首删除(能力由调用方注入;确认弹窗等逻辑在控制台层)——

// hint 非 null = 该行不可删,返回值即禁用原因(title)。
function rowDeleteHint(rowIndex: number): string {
  return props.rowDelete?.hint(rowIndex) ?? '删除该行'
}

function rowDeleteDisabled(rowIndex: number): boolean {
  return (props.rowDelete?.hint(rowIndex) ?? null) !== null
}

function onRowDelete(rowIndex: number): void {
  props.rowDelete?.delete(rowIndex)
}
</script>

<template>
  <!-- DBX 皮肤:卡片圆角描边 bg-card,头部摘要区 bg-muted/40;操作区全部
       Button(ghost 小号)。flex:none:结果卡内含 overflow:auto 的表格,禁
       收缩后超出部分交给外层容器滚动(见下方注释保留)。 -->
  <div class="flex flex-none flex-col overflow-hidden rounded-lg border border-border bg-card" data-test="result-card">
    <div class="flex items-center gap-2 border-b border-border bg-muted/40 px-3 py-1.5">
      <span class="size-2 shrink-0 rounded-full" :class="dotClass" data-test="result-status-dot"></span>
      <div
        class="stmt min-w-0 flex-1 cursor-pointer font-mono text-xs text-muted-foreground"
        data-test="result-toggle"
        :title="expanded ? '点击收起' : '点击展开全文'"
        @click="expanded = !expanded"
      >
        <span v-if="!expanded" class="stmt-summary block truncate">{{ statement }}</span>
        <pre v-else class="stmt-full m-0 whitespace-pre-wrap break-all text-foreground">{{ statement }}</pre>
      </div>
      <span class="shrink-0 font-mono text-xs tabular-nums whitespace-nowrap text-muted-foreground" data-test="result-meta">{{ metaText }}</span>
      <div class="actions flex shrink-0 items-center gap-1">
        <span v-if="selected.size > 0" class="shrink-0 text-xs tabular-nums whitespace-nowrap text-primary" data-test="selected-count">
          已选 {{ selected.size }} 行
        </span>
        <div ref="insertRoot" class="insert-group relative flex">
          <Button
            variant="ghost"
            size="sm"
            class="h-7 rounded-r-none px-2 text-xs"
            data-test="result-copy-insert"
            :disabled="!insertTarget"
            :title="insertTarget ? '复制为 INSERT 语句' : '仅单表查询结果可生成'"
            @click="onCopyInsert"
          >
            复制为 INSERT
          </Button>
          <Button
            variant="ghost"
            size="sm"
            class="h-7 -ml-px rounded-l-none border-l border-border px-1 text-xs"
            data-test="insert-opts"
            :disabled="!insertTarget"
            title="INSERT 选项"
            @click="insertOptsOpen = !insertOptsOpen"
          >
            <ChevronDown class="size-3" />
          </Button>
          <div v-if="insertOptsOpen" class="insert-pop absolute right-0 top-[calc(100%+6px)] z-20 flex min-w-[150px] flex-col gap-0.5 rounded-md border border-border bg-popover p-2 shadow-md" data-test="insert-pop">
            <label class="opt-row flex cursor-pointer items-center justify-between gap-3 px-0.5 py-0.5 text-xs whitespace-nowrap text-foreground">
              <span>包含主键列</span>
              <input
                v-model="includePK"
                type="checkbox"
                data-test="insert-opt-pk"
                :disabled="primaryKey.length === 0"
                :title="pkToggleTitle"
                class="accent-primary disabled:cursor-not-allowed disabled:opacity-45"
              />
            </label>
            <label class="opt-row flex cursor-pointer items-center justify-between gap-3 px-0.5 py-0.5 text-xs whitespace-nowrap text-foreground">
              <span>每行单独 INSERT</span>
              <input v-model="perRowOpt" type="checkbox" data-test="insert-opt-perrow" class="accent-primary" />
            </label>
          </div>
        </div>
        <Button variant="ghost" size="sm" class="h-7 px-2 text-xs" data-test="result-copy-csv" title="复制为 CSV(含表头)" @click="onCopyCsv">
          复制为 CSV
        </Button>
        <Button variant="ghost" size="sm" class="h-7 px-2 text-xs" data-test="result-export-csv" title="导出 CSV 文件" @click="onExportCsv">
          导出 CSV
        </Button>
        <Button variant="ghost" size="sm" class="h-7 px-2 text-xs" data-test="result-export-jsonl" title="导出 JSONL 文件" @click="onExportJsonl">
          导出 JSONL
        </Button>
      </div>
    </div>

    <div v-if="error" class="flex items-start gap-2.5 bg-destructive/10 px-3 py-2.5 font-mono text-xs text-destructive" data-test="result-error">
      <span class="min-w-0 flex-1 whitespace-pre-wrap break-all">{{ error }}</span>
      <Button
        variant="ghost"
        size="sm"
        class="h-6 shrink-0 rounded-sm border border-destructive/40 px-2 text-xs text-destructive hover:text-destructive"
        data-test="result-error-copy"
        @click="onCopyError"
      >
        复制
      </Button>
    </div>
    <div v-else class="max-h-[420px] overflow-auto">
      <table class="w-full border-collapse text-xs" data-test="result-table">
        <thead>
          <tr>
            <th class="rownum sticky top-0 z-[1] w-[34px] min-w-[34px] border-b border-border bg-muted px-2 py-1 text-right align-middle font-medium text-muted-foreground">#</th>
            <th v-if="rowDelete" class="row-action sticky top-0 z-[1] w-[34px] min-w-[34px] border-b border-border bg-muted px-2 py-1 text-center align-middle font-medium" title="删除该行(按主键定位)"></th>
            <th v-if="selectable" class="checkcell sticky top-0 z-[1] w-[30px] min-w-[30px] border-b border-border bg-muted px-2 py-1 text-center align-middle font-medium">
              <input
                type="checkbox"
                data-test="row-check-all"
                :checked="allSelected"
                title="全选/清空"
                class="cursor-pointer align-middle accent-primary"
                @change="toggleAll"
              />
            </th>
            <th
              v-for="(col, ci) in columns"
              :key="ci"
              class="sticky top-0 z-[1] border-b border-border bg-muted px-2 py-1 text-left align-middle font-medium whitespace-nowrap text-muted-foreground"
              :title="col.type ? `${col.name}(${col.type})` : col.name"
            >
              <!-- 字段名一行,类型小字第二行,避免类型只在悬浮提示里不可见。 -->
              <span class="col-name block">{{ col.name }}</span>
              <span v-if="col.type" class="col-type block max-w-[340px] truncate font-mono text-[11px] font-normal text-muted-foreground/70" data-test="col-type">{{ col.type }}</span>
            </th>
          </tr>
        </thead>
        <tbody>
          <tr
            v-for="(row, ri) in rows"
            :key="ri"
            class="group border-b border-border transition-colors last:border-b-0 hover:bg-accent/50"
            :class="{ 'bg-accent/40': selected.has(ri) }"
            data-test="result-row"
          >
            <td class="rownum bg-muted/40 px-2 py-1 text-right align-middle text-muted-foreground">{{ ri + 1 }}</td>
            <!-- 行首删除:危险色小按钮,悬停所在行时显现;禁用原因由调用方 hint 给出。 -->
            <td v-if="rowDelete" class="row-action px-2 py-1 text-center align-middle">
              <button
                type="button"
                class="row-del inline-flex size-5 items-center justify-center rounded-sm border border-destructive/50 bg-transparent text-destructive opacity-0 transition-opacity hover:bg-destructive/10 disabled:pointer-events-none disabled:opacity-40 group-hover:opacity-100"
                data-test="btn-row-delete"
                :disabled="rowDeleteDisabled(ri)"
                :title="rowDeleteHint(ri)"
                @click="onRowDelete(ri)"
              >
                <X class="size-3" />
              </button>
            </td>
            <td v-if="selectable" class="checkcell px-2 py-1 text-center align-middle">
              <input
                type="checkbox"
                data-test="row-check"
                :checked="selected.has(ri)"
                class="cursor-pointer align-middle accent-primary"
                @change="toggleRow(ri)"
              />
            </td>
            <td
              v-for="(cell, ci) in row"
              :key="ci"
              class="cell max-w-[360px] truncate px-2 py-1 text-left align-middle whitespace-nowrap"
              :class="{ 'cell-editing relative': isEditing(ri, ci) }"
              :title="cell == null ? 'NULL' : cell"
              @dblclick="onCellDblclick(ri, ci, $event)"
            >
              <!-- 编辑输入框:绝对定位浮层,尺寸由双击时记录的 td 像素宽高内联
                   锁定(见 onCellDblclick)——与原数据区域完全一致,行高列宽
                   纹丝不动,全部内容在框内滚动查看。 -->
              <textarea
                v-if="isEditing(ri, ci)"
                :ref="focusEditor"
                class="cell-editor absolute left-0 top-0 m-0 box-border resize-none overflow-auto rounded-[4px] border border-primary/40 bg-popover px-2 py-1 font-mono text-xs leading-normal text-foreground outline-none ring-2 ring-primary/15"
                data-test="result-cell-editor"
                spellcheck="false"
                autocapitalize="off"
                autocorrect="off"
                autocomplete="off"
                :style="editorStyle"
                :value="editing?.draft ?? ''"
                @keydown.enter.prevent="onEditCommit"
                @keydown.esc.prevent="emit('edit-cancel')"
                @blur="onEditBlur"
              />
              <template v-else>{{ cell ?? '' }}</template>
            </td>
          </tr>
          <tr v-if="rows.length === 0">
            <td class="p-3 text-center text-muted-foreground" :colspan="columns.length + (rowDelete ? 1 : 0) + (selectable ? 2 : 1)" data-test="result-empty">0 行</td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>

