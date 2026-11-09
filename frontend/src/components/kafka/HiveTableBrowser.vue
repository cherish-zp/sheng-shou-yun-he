<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import { Button } from '@/components/ui/button'
import { getApi } from '@/api/client'
import type { HiveColumn, HivePageRowsResult, HiveTableColumnsResult } from '@/api/types'
import TableDataGrid from '@/components/common/TableDataGrid.vue'
import { usePagedTable } from '@/composables/usePagedTable'

const props = defineProps<{ connectionId: string; database: string; table: string }>()

// 每页行数与后端 hivePageRows 的 limit 对齐(Hive 不支持 OFFSET,后端用
// ROW_NUMBER() OVER() 窗口包装实现翻页)。
const PAGE_SIZE = 500
// 超长单元格客户端截断展示(完整值保留在 title 提示中)。
const CELL_TRUNCATE = 200

// Hive 表浏览器只读浏览:无过滤/排序/行内编辑/行删除(裁剪自
// MysqlTableBrowser,死代码不留);清空数据仅内部表(MANAGED_TABLE)可用,
// 表类型经 hiveTableColumns 探测。

const columns = ref<HiveColumn[]>([])
const rows = ref<(string | null)[][]>([])
const totalRows = ref(0)
const tableType = ref('')
const loading = ref(false)
const error = ref<string | null>(null)
const confirmTruncate = ref(false)

// --- 分页:共用组合式(Hive 只读浏览,无过滤/排序/编辑)。 ---
const { offset, page, nextPage, prevPage } = usePagedTable({ pageSize: PAGE_SIZE, refetch: fetchPage })

// 仅内部表(事务相关数据落在 MANAGED_TABLE)支持 TRUNCATE;外部表/视图禁用。
const isManagedTable = computed(() => tableType.value === 'MANAGED_TABLE')
const truncateDisabledTitle = '仅内部表(MANAGED_TABLE)支持清空'

async function fetchPage(): Promise<void> {
  loading.value = true
  error.value = null
  try {
    const res = (await getApi().hivePageRows?.({
      connection_id: props.connectionId,
      database: props.database,
      table: props.table,
      limit: PAGE_SIZE,
      offset: offset.value,
    })) as HivePageRowsResult | undefined
    // wire 形状防御:后端异常/老版本可能给出 null 或缺字段;模板对
    // columns/rows 按数组、totalRows 按数字直接使用,任何 null/undefined
    // 都会在渲染期抛 TypeError 并中断整个调度队列(WKWebView 表现为白屏)。
    columns.value = res?.columns ?? []
    rows.value = res?.rows ?? []
    const total = Number(res?.total_rows)
    totalRows.value = Number.isFinite(total) ? total : 0
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e)
  } finally {
    loading.value = false
  }
}

// 表类型探测(清空数据守卫 + 汇总展示);失败静默为空(按未知类型处理,
// 清空按钮禁用,避免误清外部表)。
async function loadTableType(): Promise<void> {
  try {
    const res = (await getApi().hiveTableColumns?.({
      connection_id: props.connectionId,
      database: props.database,
      table: props.table,
    })) as HiveTableColumnsResult | undefined
    tableType.value = res?.table_type ?? ''
  } catch {
    tableType.value = ''
  }
}

onMounted(() => {
  void fetchPage()
  void loadTableType()
})

// 外部(树/Layout)切换表时重置分页状态并重拉第一页与表类型。
watch(
  () => props.table,
  () => {
    offset.value = 0
    tableType.value = ''
    void fetchPage()
    void loadTableType()
  },
)

function refresh(): void {
  void fetchPage()
}

// 清空数据:文案包含表名与表类型,确认弹窗走危险色。
const truncateMessage = computed(
  () => `确认清空表「${props.table}」(类型 ${tableType.value || '未知'})的全部数据？此操作不可恢复。`,
)

async function doTruncate(): Promise<void> {
  confirmTruncate.value = false
  try {
    await getApi().hiveTruncateTable?.({
      connection_id: props.connectionId,
      database: props.database,
      table: props.table,
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

// cellTitle 数据单元格悬停提示:完整原值(Hive 只读,无编辑提示)。
function cellTitle(v: string | null): string {
  return v ?? ''
}

// colTitle 列头悬停提示:类型小字;comment 非空时追加描述(不可排序说明)。
function colTitle(c: HiveColumn): string {
  const base = `${c.type} · Hive 表浏览器不排序`
  return c.comment ? `${base}\n${c.comment}` : base
}
</script>

<template>
  <div class="p-4" data-test="hive-table-browser">
    <div class="flex flex-wrap items-center gap-2 rounded-md border border-border bg-muted/50 px-3 py-2 text-xs text-muted-foreground" data-test="hive-summary">
      <span class="font-mono font-semibold text-foreground" data-test="hive-summary-table">{{ props.database }}.{{ props.table }}</span>
      <span class="text-muted-foreground/60">·</span>
      <span data-test="hive-summary-type">{{ tableType || '—' }}</span>
      <span class="text-muted-foreground/60">·</span>
      <span class="tabular-nums" data-test="hive-summary-rows">≈ {{ totalRows.toLocaleString() }} 行</span>
    </div>

    <div class="mt-2.5 flex flex-wrap items-center gap-2">
      <span class="text-xs text-muted-foreground/80" data-test="hive-readonly-hint">Hive 表浏览器只读:行级写入请在 SQL 控制台执行</span>
      <span class="flex-1"></span>
      <Button variant="ghost" size="sm" class="text-xs" data-test="btn-hive-refresh" :disabled="loading" @click="refresh">
        {{ loading ? '加载中…' : '刷新' }}
      </Button>
      <Button
        variant="ghost"
        size="sm"
        class="text-xs text-destructive hover:text-destructive"
        data-test="btn-hive-truncate"
        :disabled="!isManagedTable"
        :title="isManagedTable ? '清空表数据' : truncateDisabledTitle"
        @click="confirmTruncate = true"
      >清空数据</Button>
    </div>

    <div v-if="error" class="mt-2 rounded-md bg-destructive/10 px-2.5 py-1.5 text-[13px] text-destructive" data-test="hive-error">{{ error }}</div>

    <div v-if="columns.length > 0 && rows.length === 0 && !loading" class="mt-2.5 max-h-[calc(100vh-320px)] overflow-auto rounded-lg border border-border bg-card">
      <div class="px-0 pb-1" data-test="hive-fields-panel">
        <div class="border-b border-border px-3 py-2.5 text-xs font-semibold text-muted-foreground" data-test="hive-fields-title">表结构(共 {{ columns.length }} 字段)</div>
        <div v-for="c in columns" :key="c.name" class="flex items-baseline gap-2.5 border-b border-border px-3 py-2 last:border-b-0" data-test="hive-field-row">
          <span class="font-mono text-[13px] font-semibold text-foreground" data-test="hive-field-name">{{ c.name }}</span>
          <span class="shrink-0 rounded-[4px] bg-muted px-1.5 py-px font-mono text-[10px] text-muted-foreground" data-test="hive-field-type">{{ c.type }}</span>
          <span v-if="c.comment" class="min-w-0 flex-1 truncate text-xs text-muted-foreground/80" data-test="hive-field-comment">{{ c.comment }}</span>
        </div>
      </div>
      <div class="p-7 text-center text-muted-foreground" data-test="hive-grid-empty">
        <div class="mb-1.5 text-[26px] leading-none">◌</div>
        <div>该表暂无数据</div>
        <div class="mt-1 text-[11px] text-muted-foreground/80">数据写入后点击「刷新」查看</div>
      </div>
    </div>

    <div v-else class="mt-2.5 max-h-[calc(100vh-320px)] overflow-auto rounded-lg border border-border bg-card">
      <!-- 共享表格网格:Hive 只读浏览(sortable=false,无行删除/编辑/主键角标)。 -->
      <TableDataGrid
        test-prefix="hive"
        :columns="columns"
        :rows="rows"
        :loading="loading"
        :sortable="false"
        :col-title="colTitle"
        :cell-title="cellTitle"
      />
    </div>

    <div class="mt-2.5 flex items-center gap-2.5" data-test="hive-pager">
      <Button variant="ghost" size="sm" class="text-xs" data-test="btn-hive-prev" :disabled="offset === 0 || loading" @click="prevPage">上一页</Button>
      <span class="text-xs tabular-nums text-muted-foreground" data-test="hive-page-label">第 {{ page }} 页</span>
      <Button variant="ghost" size="sm" class="text-xs" data-test="btn-hive-next" :disabled="rows.length < PAGE_SIZE || loading" @click="nextPage">下一页</Button>
    </div>

    <!-- 清空数据:危险操作,文案含表名与表类型;仅内部表可点。 -->
    <ConfirmDialog
      :show="confirmTruncate"
      :message="truncateMessage"
      confirm-text="清空"
      danger
      @confirm="doTruncate"
      @cancel="confirmTruncate = false"
    />
  </div>
</template>
