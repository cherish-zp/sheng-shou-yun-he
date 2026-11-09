<script setup lang="ts">
// Hive 表结构编辑弹窗:加载 hiveTableColumns 全量列元数据 + DDL,按 Hive
// 方言裁剪——「新增列(只能追加表尾,无 AFTER)+ 修改列(仅类型/注释,不
// 支持改名)+ 删除列(Hive 3 直接 DROP COLUMN,低版本由后端自动降级
// REPLACE COLUMNS 重建)」。分区列只读展示(分区列不可改);Hive 无主键
// 概念,不显示主键列。SQL 一律由后端拼装,这里只传结构化 HiveColumnDef。
import { computed, ref, watch } from 'vue'
import { X } from 'lucide-vue-next'
import { getApi } from '@/api/client'
import type { HiveColumn, HiveColumnDef, HiveTableColumnsResult } from '@/api/types'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'

const props = defineProps<{ show: boolean; connectionId: string; database: string; table: string }>()
const emit = defineEmits<{ (e: 'close'): void }>()

// 行编辑模型:isPartition = 分区列(只读展示);isNew = 本次新增未保存;
// toDrop = 已有列标记为将 DROP;dirty = 用户是否真实编辑过该行(零编辑行
// 不进入 modify_columns)。
interface ColumnRow {
  name: string
  type: string
  comment: string
  isPartition: boolean
  isNew: boolean
  toDrop: boolean
  dirty: boolean
}

const rows = ref<ColumnRow[]>([])
// 原始列元数据快照(含分区列),供 diff 判断「该行是否被修改过」。
const originals = ref<Map<string, HiveColumn>>(new Map())
const tableType = ref('')
const transactional = ref(false)
const ddl = ref('')
const loading = ref(false)
const loadError = ref('')
const saving = ref(false)
const saveError = ref('')
const savedTip = ref(false)
const showDdl = ref(false)
// 新增列表单:null = 未展开。
const addForm = ref<null | { name: string; type: string; comment: string }>(null)

function toRow(c: HiveColumn, isPartition: boolean): ColumnRow {
  return {
    name: c.name,
    type: c.type,
    comment: c.comment ?? '',
    isPartition,
    isNew: false,
    toDrop: false,
    dirty: false,
  }
}

async function load(): Promise<void> {
  loading.value = true
  loadError.value = ''
  try {
    const res = (await getApi().hiveTableColumns?.({
      connection_id: props.connectionId,
      database: props.database,
      table: props.table,
    })) as HiveTableColumnsResult | undefined
    const cols = res?.columns ?? []
    const parts = res?.partition_columns ?? []
    // 普通列在前,分区列随后只读展示(分区列不可改)。
    rows.value = [...cols.map((c) => toRow(c, false)), ...parts.map((c) => toRow(c, true))]
    originals.value = new Map([...cols, ...parts].map((c) => [c.name, c]))
    tableType.value = res?.table_type ?? ''
    transactional.value = res?.transactional ?? false
    ddl.value = res?.ddl ?? ''
    addForm.value = null
    showDdl.value = false
  } catch (e) {
    loadError.value = e instanceof Error ? e.message : String(e)
  } finally {
    loading.value = false
  }
}

watch(
  () => props.show,
  (show) => {
    if (show) {
      savedTip.value = false
      void load()
    }
  },
  { immediate: true },
)

function openAddForm(): void {
  addForm.value = { name: '', type: '', comment: '' }
}

function confirmAdd(): void {
  const f = addForm.value
  if (!f || !f.name.trim() || !f.type.trim()) return
  rows.value.push({
    name: f.name.trim(),
    type: f.type.trim(),
    comment: f.comment,
    isPartition: false,
    isNew: true,
    toDrop: false,
    dirty: false,
  })
  addForm.value = null
}

// removeRow:分区列直接不响应(按钮已禁用,分区列不可改);已有列标记为将
// DROP(可撤销);本次新增行直接移除。
function removeRow(row: ColumnRow): void {
  if (row.isPartition) return
  if (row.isNew) {
    rows.value = rows.value.filter((r) => r !== row)
  } else {
    row.toDrop = !row.toDrop
  }
}

// 新增列:只能追加表尾(Hive 无 AFTER 位置选项)。
const addColumns = computed<HiveColumnDef[]>(() =>
  rows.value
    .filter((r) => r.isNew)
    .map((r) => ({
      name: r.name,
      type: r.type,
      comment: r.comment,
    })),
)

// 修改列:仅类型/注释(不支持改列名);分区列只读不参与;行级 dirty 过滤
// 零编辑行,保存不产生空 modify。
const modifyColumns = computed<HiveColumnDef[]>(() => {
  const out: HiveColumnDef[] = []
  for (const r of rows.value) {
    if (r.isNew || r.toDrop || r.isPartition) continue
    if (!r.dirty) continue
    const orig = originals.value.get(r.name)
    if (!orig) continue
    const changed = r.type !== orig.type || r.comment !== (orig.comment ?? '')
    if (!changed) continue
    out.push({ name: r.name, type: r.type, comment: r.comment })
  }
  return out
})

const dropColumns = computed(() => rows.value.filter((r) => !r.isNew && !r.isPartition && r.toDrop).map((r) => r.name))

const hasChanges = computed(() => addColumns.value.length > 0 || modifyColumns.value.length > 0 || dropColumns.value.length > 0)

async function save(): Promise<void> {
  if (!hasChanges.value || saving.value) return
  saving.value = true
  saveError.value = ''
  savedTip.value = false
  try {
    await getApi().hiveAlterTable?.({
      connection_id: props.connectionId,
      database: props.database,
      table: props.table,
      add_columns: addColumns.value,
      modify_columns: modifyColumns.value,
      drop_columns: dropColumns.value,
    })
    savedTip.value = true
    await load()
  } catch (e) {
    saveError.value = e instanceof Error ? e.message : String(e)
  } finally {
    saving.value = false
  }
}

function close(): void {
  emit('close')
}
</script>

<template>
  <Teleport to="body">
    <div v-if="show" class="fixed inset-0 z-[100] flex items-center justify-center bg-black/30 backdrop-blur-[2px]" data-test="hive-structure-dialog" @click.self="close">
      <div class="flex max-h-[calc(100vh-96px)] w-[min(820px,calc(100vw-64px))] flex-col gap-2.5 overflow-hidden rounded-xl border border-border bg-card p-4 text-foreground shadow-xl">
        <div class="flex items-center gap-2">
          <span class="text-sm font-semibold">编辑表字段</span>
          <span class="min-w-0 flex-1 truncate text-xs whitespace-nowrap text-muted-foreground" :title="`${database}.${table}`">{{ database }}.{{ table }}</span>
          <Badge v-if="tableType" variant="outline" class="shrink-0 rounded-[4px] px-1.5 py-0 font-mono text-[10px] text-muted-foreground" :title="`表类型 ${tableType}`">{{ tableType }}</Badge>
          <Badge v-if="transactional" variant="outline" class="shrink-0 border-success/40 bg-success/10 rounded-[4px] px-1.5 py-0 font-mono text-[10px] text-success" title="ACID 事务表">ACID</Badge>
          <Button variant="ghost" size="sm" class="size-6 p-0 text-muted-foreground hover:text-foreground" type="button" data-test="btn-structure-close" @click="close">
            <X class="size-3.5" />
          </Button>
        </div>

        <div v-if="loading" class="py-3 text-xs text-muted-foreground" data-test="structure-loading">加载中…</div>
        <div v-else-if="loadError" class="flex items-center gap-2 text-xs text-destructive" data-test="structure-load-error">
          <span>{{ loadError }}</span>
          <Button variant="ghost" size="sm" class="h-7 text-xs" type="button" data-test="btn-structure-retry" @click="load">重试</Button>
        </div>
        <template v-else>
          <div class="flex items-center gap-2.5">
            <Button variant="ghost" size="sm" class="h-7 text-xs" type="button" data-test="btn-add-column" @click="openAddForm">＋ 新增列</Button>
            <span class="text-[11px] text-muted-foreground/80">Hive 方言:新增列只能追加表尾;修改列仅支持类型/注释(不支持改名);删除列在 Hive 3 直接生效,低版本由后端自动以 REPLACE COLUMNS 降级重建;分区列只读。</span>
          </div>

          <!-- 新增列表单:确认后作为一行未保存列进入表格(追加表尾,无位置选项)。 -->
          <div v-if="addForm" class="flex flex-wrap items-center gap-1.5 rounded-md border border-border bg-muted/50 p-2" data-test="structure-add-form">
            <input v-model="addForm.name" class="h-7 w-[130px] rounded-md border border-input bg-transparent px-2 text-xs text-foreground outline-none transition-[color,box-shadow] placeholder:text-muted-foreground focus:border-ring focus:ring-[3px] focus:ring-ring/30 disabled:cursor-not-allowed disabled:opacity-50" type="text" placeholder="列名" data-test="add-name" />
            <input v-model="addForm.type" class="h-7 w-[130px] rounded-md border border-input bg-transparent px-2 font-mono text-xs text-foreground outline-none transition-[color,box-shadow] placeholder:text-muted-foreground focus:border-ring focus:ring-[3px] focus:ring-ring/30 disabled:cursor-not-allowed disabled:opacity-50" type="text" placeholder="类型,如 string/bigint" data-test="add-type" />
            <input v-model="addForm.comment" class="h-7 w-[130px] rounded-md border border-input bg-transparent px-2 text-xs text-foreground outline-none transition-[color,box-shadow] placeholder:text-muted-foreground focus:border-ring focus:ring-[3px] focus:ring-ring/30 disabled:cursor-not-allowed disabled:opacity-50" type="text" placeholder="注释" data-test="add-comment" />
            <span class="text-[11px] text-muted-foreground/80">(追加表尾)</span>
            <Button variant="default" size="sm" class="h-7 text-xs" type="button" data-test="btn-confirm-add-column" :disabled="!addForm.name.trim() || !addForm.type.trim()" @click="confirmAdd">添加</Button>
            <Button variant="ghost" size="sm" class="h-7 text-xs" type="button" data-test="btn-cancel-add-column" @click="addForm = null">取消</Button>
          </div>

          <!-- 列信息表:ui/table 系列承载(粘性表头用 bg-card 保列底不透)。 -->
          <div class="max-h-[45vh] overflow-auto rounded-md border border-border">
            <Table class="text-xs">
              <TableHeader>
                <TableRow class="hover:bg-transparent">
                  <TableHead class="sticky top-0 z-[1] h-8 bg-card px-2 text-xs font-medium text-muted-foreground">列名</TableHead>
                  <TableHead class="sticky top-0 z-[1] h-8 bg-card px-2 text-xs font-medium text-muted-foreground">类型</TableHead>
                  <TableHead class="sticky top-0 z-[1] h-8 bg-card px-2 text-xs font-medium text-muted-foreground">注释</TableHead>
                  <TableHead class="sticky top-0 z-[1] h-8 bg-card px-2 text-xs font-medium text-muted-foreground">分区</TableHead>
                  <TableHead class="sticky top-0 z-[1] h-8 bg-card px-2 text-xs font-medium text-muted-foreground">操作</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                <TableRow
                  v-for="row in rows"
                  :key="`${row.isNew ? 'new:' : ''}${row.name}`"
                  class="[&>td]:py-1"
                  :class="[
                    row.toDrop ? 'row-drop opacity-55 [&>td:first-child]:line-through' : '',
                    row.isNew ? 'row-new bg-primary/5 hover:bg-primary/5' : '',
                    row.isPartition ? 'row-partition bg-muted/40 hover:bg-muted/40' : '',
                  ]"
                  data-test="structure-column-row"
                >
                  <TableCell class="px-2 align-middle">
                    <input v-if="row.isNew" v-model="row.name" class="h-7 w-full min-w-[110px] rounded-md border border-input bg-transparent px-2 text-xs text-foreground outline-none transition-[color,box-shadow] focus:border-ring focus:ring-[3px] focus:ring-ring/30 disabled:cursor-not-allowed disabled:opacity-50" type="text" data-test="column-name" />
                    <template v-else><span class="font-mono text-xs" data-test="column-name">{{ row.name }}</span></template>
                  </TableCell>
                  <!-- 修改列支持类型变更(兼容性由 Hive 校验,失败原样透出);
                       分区列只读。 -->
                  <TableCell class="px-2 align-middle">
                    <input v-model="row.type" class="h-7 w-full min-w-[110px] rounded-md border border-input bg-transparent px-2 font-mono text-xs text-foreground outline-none transition-[color,box-shadow] focus:border-ring focus:ring-[3px] focus:ring-ring/30 disabled:cursor-not-allowed disabled:opacity-50" type="text" :disabled="row.isPartition || row.toDrop" data-test="column-type" @input="row.dirty = true" />
                  </TableCell>
                  <TableCell class="px-2 align-middle">
                    <input v-model="row.comment" class="h-7 w-full min-w-[110px] rounded-md border border-input bg-transparent px-2 text-xs text-foreground outline-none transition-[color,box-shadow] focus:border-ring focus:ring-[3px] focus:ring-ring/30 disabled:cursor-not-allowed disabled:opacity-50" type="text" :disabled="row.isPartition || row.toDrop" data-test="column-comment" @input="row.dirty = true" />
                  </TableCell>
                  <TableCell class="px-2 align-middle">
                    <Badge v-if="row.isPartition" variant="outline" class="rounded-[4px] px-1.5 py-0 text-[10px] text-muted-foreground" data-test="partition-badge" title="分区列不可修改">分区</Badge>
                  </TableCell>
                  <TableCell class="px-2 align-middle">
                    <Button v-if="!row.isNew" variant="ghost" size="sm" class="h-6 rounded-sm border border-border px-2 text-[11px] text-muted-foreground hover:bg-destructive/10 hover:text-destructive" type="button" data-test="btn-drop-column" :disabled="row.isPartition" :title="row.isPartition ? '分区列不支持在此删除' : undefined" @click="removeRow(row)">
                      {{ row.toDrop ? '撤销' : '移除' }}
                    </Button>
                    <Button v-else variant="ghost" size="sm" class="h-6 rounded-sm border border-border px-2 text-[11px] text-muted-foreground hover:bg-destructive/10 hover:text-destructive" type="button" data-test="btn-remove-new-column" @click="removeRow(row)">移除</Button>
                  </TableCell>
                </TableRow>
              </TableBody>
            </Table>
          </div>

          <div v-if="saveError" class="flex items-center gap-2 text-xs text-destructive" data-test="structure-save-error">{{ saveError }}</div>
          <div v-if="savedTip" class="text-xs text-success" data-test="structure-saved">表结构已更新</div>

          <div class="flex items-center gap-2">
            <Button variant="ghost" size="sm" class="h-7 text-xs" type="button" data-test="btn-toggle-ddl" @click="showDdl = !showDdl">查看 DDL</Button>
            <span class="flex-1"></span>
            <Button variant="default" size="sm" class="h-7 text-xs" type="button" data-test="btn-structure-save" :disabled="!hasChanges || saving" @click="save">
              {{ saving ? '保存中…' : '保存修改' }}
            </Button>
          </div>
          <!-- DDL 块:bg-muted 圆角 + pre 滚动。 -->
          <pre v-if="showDdl" class="m-0 max-h-[200px] overflow-auto whitespace-pre rounded-md bg-muted p-2.5 font-mono text-[11px] leading-normal text-foreground" data-test="structure-ddl">{{ ddl || '(无 DDL)' }}</pre>
        </template>
      </div>
    </div>
  </Teleport>
</template>

