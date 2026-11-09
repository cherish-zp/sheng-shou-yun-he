<script setup lang="ts">
// MySQL/TiDB 表结构编辑弹窗:加载 information_schema 全量列元数据 + DDL,
// 支持「已有列内联编辑(类型/可空/默认值/注释)+ 新增列(含 AFTER 位置)+
// 移除列」的 diff 保存;主键列只读(改主键请用 DDL)。SQL 一律由后端拼装,
// 这里只传结构化 MysqlColumnDef。
import { computed, ref, watch } from 'vue'
import { KeyRound, X } from 'lucide-vue-next'
import { getApi } from '@/api/client'
import type { MysqlColumnDef, MysqlTableColumn } from '@/api/types'
import { Button } from '@/components/ui/button'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'

const props = defineProps<{ show: boolean; connectionId: string; database: string; table: string }>()
const emit = defineEmits<{ (e: 'close'): void }>()

// 行编辑模型:isNew = 本次新增未保存;toDrop = 已有列标记为将 DROP;
// dirty = 用户是否真实编辑过该行(零编辑行不进入 modify_columns)。
interface ColumnRow {
  name: string
  columnType: string
  nullable: boolean
  defaultValue: string // 输入框文本;仅 noDefault=false 时生效,空串即空字符串默认值
  noDefault: boolean // 勾选 = 无默认值(DEFAULT NULL);取消勾选后输入框内容(含空串)即默认值字面量
  comment: string
  autoIncrement: boolean
  isPrimaryKey: boolean
  isNew: boolean
  toDrop: boolean
  dirty: boolean
  after: string | null // 仅新增列生效:'' 之外为 AFTER 列名
}

const rows = ref<ColumnRow[]>([])
// 原始列元数据快照,供 diff 判断「该行是否被修改过」。
const originals = ref<Map<string, MysqlTableColumn>>(new Map())
const ddl = ref('')
const loading = ref(false)
const loadError = ref('')
const saving = ref(false)
const saveError = ref('')
const savedTip = ref(false)
const showDdl = ref(false)
// 新增列表单:null = 未展开。
const addForm = ref<null | { name: string; columnType: string; nullable: boolean; defaultValue: string; noDefault: boolean; comment: string; autoIncrement: boolean; after: string }>(null)

const existingNames = computed(() => rows.value.filter((r) => !r.isNew).map((r) => r.name))

function toRow(c: MysqlTableColumn): ColumnRow {
  return {
    name: c.name,
    columnType: c.column_type,
    nullable: c.nullable,
    // 保留原始 default_value:'' 是空字符串默认值,null 才是无默认值,不做归一。
    defaultValue: c.default_value ?? '',
    noDefault: c.default_value === null,
    comment: c.comment,
    autoIncrement: /auto_increment/i.test(c.extra),
    isPrimaryKey: c.is_primary_key,
    isNew: false,
    toDrop: false,
    dirty: false,
    after: null,
  }
}

async function load(): Promise<void> {
  loading.value = true
  loadError.value = ''
  try {
    const res = await getApi().mysqlTableColumns?.({
      connection_id: props.connectionId,
      database: props.database,
      table: props.table,
    })
    const cols = res?.columns ?? []
    rows.value = cols.map(toRow)
    originals.value = new Map(cols.map((c) => [c.name, c]))
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
  addForm.value = { name: '', columnType: '', nullable: true, defaultValue: '', noDefault: true, comment: '', autoIncrement: false, after: '' }
}

function confirmAdd(): void {
  const f = addForm.value
  if (!f || !f.name.trim() || !f.columnType.trim()) return
  rows.value.push({
    name: f.name.trim(),
    columnType: f.columnType.trim(),
    nullable: f.nullable,
    defaultValue: f.defaultValue,
    noDefault: f.noDefault,
    comment: f.comment,
    autoIncrement: f.autoIncrement,
    isPrimaryKey: false,
    isNew: true,
    toDrop: false,
    dirty: false,
    after: f.after || null,
  })
  addForm.value = null
}

// removeRow:主键列直接不响应(按钮已禁用,此处双保险,主键请用 DDL 调整);
// 已有列标记为将 DROP(可撤销);本次新增行直接移除。
function removeRow(row: ColumnRow): void {
  if (row.isPrimaryKey) return
  if (row.isNew) {
    rows.value = rows.value.filter((r) => r !== row)
  } else {
    row.toDrop = !row.toDrop
  }
}

// 默认值三态:勾选「无默认值」= DEFAULT NULL;否则输入框原文(含空串)即字面量,
// 与原始值直接比较,'' 与 null 语义不同。
function effectiveDefault(noDefault: boolean, text: string): string | null {
  return noDefault ? null : text
}

const addColumns = computed<MysqlColumnDef[]>(() =>
  rows.value
    .filter((r) => r.isNew)
    .map((r) => ({
      name: r.name,
      column_type: r.columnType,
      nullable: r.nullable,
      default_value: effectiveDefault(r.noDefault, r.defaultValue),
      comment: r.comment,
      auto_increment: r.autoIncrement,
      after: r.after,
    })),
)

const modifyColumns = computed<MysqlColumnDef[]>(() => {
  const out: MysqlColumnDef[] = []
  for (const r of rows.value) {
    if (r.isNew || r.toDrop || r.isPrimaryKey) continue
    // 行级 dirty:仅用户真实编辑过的行才参与 diff,零编辑保存不产生 modify。
    if (!r.dirty) continue
    const orig = originals.value.get(r.name)
    if (!orig) continue
    const def = effectiveDefault(r.noDefault, r.defaultValue)
    // 原始值直接比较,不归一:information_schema 的 DEFAULT '' 与 DEFAULT NULL 是两种定义。
    const changed =
      r.columnType !== orig.column_type ||
      r.nullable !== orig.nullable ||
      def !== orig.default_value ||
      r.comment !== orig.comment
    if (!changed) continue
    // 修改列不允许改列名,after 不生效(契约:仅新增列)。
    out.push({
      name: r.name,
      column_type: r.columnType,
      nullable: r.nullable,
      default_value: def,
      comment: r.comment,
      auto_increment: r.autoIncrement,
      after: null,
    })
  }
  return out
})

const dropColumns = computed(() => rows.value.filter((r) => !r.isNew && r.toDrop).map((r) => r.name))

const hasChanges = computed(() => addColumns.value.length > 0 || modifyColumns.value.length > 0 || dropColumns.value.length > 0)

async function save(): Promise<void> {
  if (!hasChanges.value || saving.value) return
  saving.value = true
  saveError.value = ''
  savedTip.value = false
  try {
    await getApi().mysqlAlterTable?.({
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
    <div v-if="show" class="fixed inset-0 z-[100] flex items-center justify-center bg-black/30 backdrop-blur-[2px]" data-test="mysql-structure-dialog" @click.self="close">
      <div class="flex max-h-[calc(100vh-96px)] w-[min(880px,calc(100vw-64px))] flex-col gap-2.5 overflow-hidden rounded-xl border border-border bg-card p-4 text-foreground shadow-xl">
        <div class="flex items-center gap-2">
          <span class="text-sm font-semibold">编辑表字段</span>
          <span class="min-w-0 flex-1 truncate text-xs whitespace-nowrap text-muted-foreground" :title="`${database}.${table}`">{{ database }}.{{ table }}</span>
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
            <span class="text-[11px] text-muted-foreground/80">主键列只读,主键请用 DDL 调整;修改列不支持改名。默认值留空时请按需选择:勾选 NULL = 无默认值(NULL),取消勾选后留空 = 空字符串('')。</span>
          </div>

          <!-- 新增列表单:确认后作为一行未保存列进入表格。 -->
          <div v-if="addForm" class="flex flex-wrap items-center gap-1.5 rounded-md border border-border bg-muted/50 p-2" data-test="structure-add-form">
            <input v-model="addForm.name" class="h-7 w-[130px] rounded-md border border-input bg-transparent px-2 text-xs text-foreground outline-none transition-[color,box-shadow] placeholder:text-muted-foreground focus:border-ring focus:ring-[3px] focus:ring-ring/30 disabled:cursor-not-allowed disabled:opacity-50" type="text" placeholder="列名" data-test="add-name" />
            <input v-model="addForm.columnType" class="h-7 w-[130px] rounded-md border border-input bg-transparent px-2 text-xs text-foreground outline-none transition-[color,box-shadow] placeholder:text-muted-foreground focus:border-ring focus:ring-[3px] focus:ring-ring/30 disabled:cursor-not-allowed disabled:opacity-50" type="text" placeholder="类型,如 varchar(64)" data-test="add-type" />
            <input v-model="addForm.defaultValue" class="h-7 w-[130px] rounded-md border border-input bg-transparent px-2 text-xs text-foreground outline-none transition-[color,box-shadow] placeholder:text-muted-foreground focus:border-ring focus:ring-[3px] focus:ring-ring/30 disabled:cursor-not-allowed disabled:opacity-50" type="text" placeholder="默认值" :disabled="addForm.noDefault" data-test="add-default" />
            <label class="flex cursor-pointer items-center gap-1 text-[11px] text-muted-foreground" title="勾选 = 无默认值(NULL);取消勾选后留空 = 空字符串('')">
              <input v-model="addForm.noDefault" type="checkbox" data-test="add-no-default" class="accent-primary" />无默认值
            </label>
            <input v-model="addForm.comment" class="h-7 w-[130px] rounded-md border border-input bg-transparent px-2 text-xs text-foreground outline-none transition-[color,box-shadow] placeholder:text-muted-foreground focus:border-ring focus:ring-[3px] focus:ring-ring/30 disabled:cursor-not-allowed disabled:opacity-50" type="text" placeholder="注释" data-test="add-comment" />
            <label class="flex flex-col gap-0.5 text-[11px] text-muted-foreground">
              位置
              <select v-model="addForm.after" class="h-7 w-[150px] rounded-md border border-input bg-background px-2 text-xs text-foreground outline-none focus:border-ring focus:ring-[3px] focus:ring-ring/30" data-test="add-after">
                <option value="">(表尾)</option>
                <option v-for="n in existingNames" :key="n" :value="n">AFTER {{ n }}</option>
              </select>
            </label>
            <label class="flex cursor-pointer items-center gap-1 text-[11px] text-muted-foreground"><input v-model="addForm.nullable" type="checkbox" data-test="add-nullable" class="accent-primary" />可空</label>
            <label class="flex cursor-pointer items-center gap-1 text-[11px] text-muted-foreground"><input v-model="addForm.autoIncrement" type="checkbox" data-test="add-auto-increment" class="accent-primary" />自增</label>
            <Button variant="default" size="sm" class="h-7 text-xs" type="button" data-test="btn-confirm-add-column" :disabled="!addForm.name.trim() || !addForm.columnType.trim()" @click="confirmAdd">添加</Button>
            <Button variant="ghost" size="sm" class="h-7 text-xs" type="button" data-test="btn-cancel-add-column" @click="addForm = null">取消</Button>
          </div>

          <!-- 列信息表:ui/table 系列承载(粘性表头用 bg-card 保列底不透)。 -->
          <div class="max-h-[45vh] overflow-auto rounded-md border border-border">
            <Table class="text-xs">
              <TableHeader>
                <TableRow class="hover:bg-transparent">
                  <TableHead class="sticky top-0 z-[1] h-8 bg-card px-2 text-xs font-medium text-muted-foreground">列名</TableHead>
                  <TableHead class="sticky top-0 z-[1] h-8 bg-card px-2 text-xs font-medium text-muted-foreground">类型</TableHead>
                  <TableHead class="sticky top-0 z-[1] h-8 bg-card px-2 text-xs font-medium text-muted-foreground">可空</TableHead>
                  <TableHead class="sticky top-0 z-[1] h-8 bg-card px-2 text-xs font-medium text-muted-foreground">默认值</TableHead>
                  <TableHead class="sticky top-0 z-[1] h-8 bg-card px-2 text-xs font-medium text-muted-foreground">注释</TableHead>
                  <TableHead class="sticky top-0 z-[1] h-8 bg-card px-2 text-xs font-medium text-muted-foreground">自增</TableHead>
                  <TableHead class="sticky top-0 z-[1] h-8 bg-card px-2 text-xs font-medium text-muted-foreground">主键</TableHead>
                  <TableHead class="sticky top-0 z-[1] h-8 bg-card px-2 text-xs font-medium text-muted-foreground">操作</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                <TableRow
                  v-for="row in rows"
                  :key="`${row.isNew ? 'new:' : ''}${row.name}`"
                  class="[&>td]:py-1 [&>td:first-child]:whitespace-nowrap"
                  :class="[
                    row.toDrop ? 'row-drop opacity-55 [&>td:first-child]:line-through' : '',
                    row.isNew ? 'row-new bg-primary/5 hover:bg-primary/5' : '',
                  ]"
                  data-test="structure-column-row"
                >
                  <TableCell class="px-2 align-middle">
                    <input v-if="row.isNew" v-model="row.name" class="h-7 w-full min-w-[110px] rounded-md border border-input bg-transparent px-2 text-xs text-foreground outline-none transition-[color,box-shadow] focus:border-ring focus:ring-[3px] focus:ring-ring/30 disabled:cursor-not-allowed disabled:opacity-50" type="text" data-test="column-name" />
                    <template v-else><span class="font-mono text-xs" data-test="column-name">{{ row.name }}</span></template>
                  </TableCell>
                  <TableCell class="px-2 align-middle">
                    <input v-model="row.columnType" class="h-7 w-full min-w-[110px] rounded-md border border-input bg-transparent px-2 font-mono text-xs text-foreground outline-none transition-[color,box-shadow] focus:border-ring focus:ring-[3px] focus:ring-ring/30 disabled:cursor-not-allowed disabled:opacity-50" type="text" :disabled="!row.isNew && row.isPrimaryKey" data-test="column-type" @input="row.dirty = true" />
                  </TableCell>
                  <TableCell class="px-2 align-middle">
                    <input v-model="row.nullable" type="checkbox" class="accent-primary disabled:cursor-not-allowed disabled:opacity-50" :disabled="(!row.isNew && row.isPrimaryKey) || row.toDrop" data-test="column-nullable" @change="row.dirty = true" />
                  </TableCell>
                  <TableCell class="px-2 align-middle">
                    <div class="flex items-center gap-1.5">
                      <input
                        v-model="row.defaultValue"
                        class="h-7 min-w-0 flex-1 rounded-md border border-input bg-transparent px-2 font-mono text-xs text-foreground outline-none transition-[color,box-shadow] placeholder:text-muted-foreground/70 focus:border-ring focus:ring-[3px] focus:ring-ring/30 disabled:cursor-not-allowed disabled:opacity-50"
                        type="text"
                        :placeholder="row.noDefault ? 'NULL' : '(留空 = 空字符串)'"
                        :disabled="(!row.isNew && row.isPrimaryKey) || row.toDrop || row.noDefault"
                        data-test="column-default"
                        @input="row.dirty = true"
                      />
                      <label class="flex cursor-pointer items-center gap-1 whitespace-nowrap text-[10px] text-muted-foreground" title="勾选 = 无默认值(NULL);取消勾选后输入框留空 = 空字符串('')">
                        <input
                          v-model="row.noDefault"
                          type="checkbox"
                          class="accent-primary disabled:cursor-not-allowed disabled:opacity-50"
                          :disabled="(!row.isNew && row.isPrimaryKey) || row.toDrop"
                          data-test="column-no-default"
                          @change="row.dirty = true"
                        />NULL
                      </label>
                    </div>
                  </TableCell>
                  <TableCell class="px-2 align-middle">
                    <input v-model="row.comment" class="h-7 w-full min-w-[110px] rounded-md border border-input bg-transparent px-2 text-xs text-foreground outline-none transition-[color,box-shadow] focus:border-ring focus:ring-[3px] focus:ring-ring/30 disabled:cursor-not-allowed disabled:opacity-50" type="text" :disabled="(!row.isNew && row.isPrimaryKey) || row.toDrop" data-test="column-comment" @input="row.dirty = true" />
                  </TableCell>
                  <TableCell class="px-2 align-middle">
                    <input v-model="row.autoIncrement" type="checkbox" class="accent-primary disabled:cursor-not-allowed disabled:opacity-50" :disabled="!row.isNew" data-test="column-auto-increment" />
                  </TableCell>
                  <TableCell class="px-2 align-middle">
                    <KeyRound v-if="row.isPrimaryKey" class="size-3 text-warning" title="主键列" />
                  </TableCell>
                  <TableCell class="px-2 align-middle">
                    <Button v-if="!row.isNew" variant="ghost" size="sm" class="h-6 rounded-sm border border-border px-2 text-[11px] text-muted-foreground hover:bg-destructive/10 hover:text-destructive" type="button" data-test="btn-drop-column" :disabled="row.isPrimaryKey" :title="row.isPrimaryKey ? '主键列不支持在此删除' : undefined" @click="removeRow(row)">
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

