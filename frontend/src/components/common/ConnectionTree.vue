<script setup lang="ts">
import { computed, onUnmounted, reactive, ref, watch, type Component } from 'vue'
import { getApi } from '@/api/client'
import type { Connection, Topic, ConsumerGroup, TopicMessageCounts, RedisDBInfo, CHTableInfo, MysqlTableInfo, PostgresRelationInfo, EsIndexInfo, EsTemplateInfo, HiveTableInfo } from '@/api/types'
import {
  ArrowDown,
  ArrowUp,
  BarChart3,
  ChevronRight,
  Database,
  FileCog,
  FileText,
  Folder,
  Hash,
  Info,
  Plus,
  RefreshCw,
  Search,
  Table2,
  Trash2,
  Users,
} from 'lucide-vue-next'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { fuzzyFilter } from '@/utils/fuzzy'
import { formatCount } from '@/utils/format'
import { formatBytes } from '@/utils/bytes'
import { CSV_MIME, exportCsv, saveFile, type ExportColumn } from '@/utils/export'
// keyedComputed/useDebouncedRef 为过滤性能基建(与三棵二级树共用),原实现
// 在本文件,随二级树收敛迁至 useLazySubtree.ts,语义不变。
import { keyedComputed, useDebouncedRef, useLazySubtree } from '@/composables/useLazySubtree'
import { moveId, reorderIds } from './connReorder'
import { useConnectionsStore, type ConnectionStatus } from '@/store/connections'
import { useToastStore } from '@/store/toast'
import ConfirmDialog from './ConfirmDialog.vue'
import ContextMenu, { type ContextMenuItem } from './ContextMenu.vue'
import PromptDialog from './PromptDialog.vue'
import TopicDetailDrawer from '@/components/kafka/TopicDetailDrawer.vue'
import ResetOffsetDialog from '@/components/kafka/ResetOffsetDialog.vue'
import MysqlTableStructure from '@/components/kafka/MysqlTableStructure.vue'
import MysqlExportDialog from '@/components/kafka/MysqlExportDialog.vue'
import HiveTableStructure from '@/components/kafka/HiveTableStructure.vue'

const props = defineProps<{ connections: Connection[] }>()
const emit = defineEmits<{
  (e: 'open-topic', connectionId: string, topic: string, partitions: number[]): void
  (e: 'open-group', connectionId: string, group: string): void
  (e: 'open-lag', connectionId: string): void
  (e: 'open-redis-keys', connectionId: string, db: number): void
  (e: 'open-health', connectionId: string): void
  (e: 'open-ch-table', connectionId: string, database: string, table: string): void
  (e: 'open-mysql-table', connectionId: string, database: string, table: string): void
  (e: 'open-hive-table', connectionId: string, database: string, table: string): void
  (e: 'open-postgres-table', connectionId: string, database: string, schema: string, relation: string, relationType: 'table' | 'view' | 'materialized_view'): void
  (e: 'open-es-index', connectionId: string, index: string): void
  (e: 'open-es-template', connectionId: string, template: string): void
  (e: 'open-es-template-create', connectionId: string): void
  (e: 'open-es-monitor', connectionId: string): void
  (e: 'delete', connectionId: string): void
  (e: 'edit-connection', conn: Connection): void
  // 连接排序:hover ↑↓ / 右键菜单上移下移 / 拖拽共用同一出口,负载为
  // 重排后的完整 id 顺序,由外层(Layout)转调 store.reorderConnections 持久化。
  (e: 'reorder', ids: string[]): void
  (e: 'new'): void
}>()

const connStore = useConnectionsStore()

// Per data-source type metadata so the tree can grow to ES later.
// letter 驱动行尾「圆角小方块 + 品牌色首字母」徽标;品牌主色/柔和底色不经
// JS,统一走本组件 scoped CSS 的 --type-* 局部色表(亮暗主题各自取值,见
// <style scoped> 头部),由 .type-badge-<type> class 关联,保证对比度双主题都成立。
const TYPE_META: Record<string, { label: string; letter: string }> = {
  kafka: { label: 'Kafka', letter: 'K' },
  mysql: { label: 'MySQL', letter: 'M' },
  redis: { label: 'Redis', letter: 'R' },
  clickhouse: { label: 'ClickHouse', letter: 'C' },
  tidb: { label: 'TiDB', letter: 'T' },
  es: { label: 'ES', letter: 'E' },
  postgres: { label: 'PostgreSQL', letter: 'P' },
  hive: { label: 'Hive', letter: 'H' },
}

function typeMeta(conn: Connection): { label: string; letter: string } {
  return TYPE_META[conn.type] ?? { label: conn.type, letter: conn.type.charAt(0).toUpperCase() || '?' }
}

const STATUS_LABEL: Record<ConnectionStatus, string> = {
  unknown: '未连接',
  connecting: '连接中…',
  connected: '已连接',
  error: '连接失败',
  disconnected: '未连接',
}

function statusOf(id: string): ConnectionStatus {
  return connStore.statusById[id] ?? 'unknown'
}

function isConnected(id: string): boolean {
  return statusOf(id) === 'connected'
}

// statusClass maps a connection's state to the status-dot colouring. 保留
// `conn-status-<type>` 历史 class(既有用例断言),同时追加语义状态 class
// (conn-status-connected/connecting/...),着色统一走 scoped CSS 的语义 token
// (success/destructive/muted),不再按数据源类型取色。
function statusClass(conn: Connection): string[] {
  const s = statusOf(conn.id)
  if (s === 'connected') return [`conn-status-${conn.type}`, 'conn-status-connected']
  if (s === 'connecting') return [`conn-status-${conn.type}`, 'conn-status-connecting']
  return [`conn-status-${s}`]
}

async function onToggleConnect(conn: Connection): Promise<void> {
  if (isConnected(conn.id)) {
    await connStore.disconnect(conn.id)
  } else {
    await connStore.connect(conn.id)
  }
}

// An "object collection" groups the objects a data source exposes (topics,
// consumers, tables, indices...). Each collection can optionally offer create
// actions, so adding MySQL tables later is a matter of declaring a new entry
// here and a create/delete branch in the dispatch functions below. ES 的
// es-index / es-template 不作为 collection 分区,但复用同一批交互状态
// (右键菜单、删除确认),因此也纳入 ObjectKind。'connection' 承载连接节点
// 自身的右键菜单(打开/断开、集群健康/监控、上移/下移、编辑、删除);
// 行内另保留 hover 显现的 ↑↓ 排序微调按钮与原生拖拽(见 conn-row)。
type ObjectKind = 'topic' | 'table' | 'group' | 'redis-db' | 'es-index' | 'es-template' | 'mysql-table' | 'hive-table' | 'connection'
interface ObjectCollection {
  key: string
  label: string
  kind: ObjectKind
  creatable: boolean
  emptyText: string
}

// 分区(分段按钮/分组标题)与树行统一走 lucide 图标,按 ObjectKind 取件;
// emoji 字符串随 UI 对齐 DBX 一并退役。
const KIND_ICONS: Record<ObjectKind, Component> = {
  topic: Folder,
  table: Table2,
  group: Users,
  'redis-db': Database,
  'es-index': FileText,
  'es-template': FileCog,
  'mysql-table': Table2,
  'hive-table': Table2,
  connection: Database,
}

const COLLECTIONS_BY_TYPE: Record<string, ObjectCollection[]> = {
  kafka: [
    { key: 'topics', label: 'Topics', kind: 'topic', creatable: true, emptyText: '（无主题）' },
    { key: 'consumers', label: 'Consumers', kind: 'group', creatable: false, emptyText: '（无消费组）' },
  ],
  redis: [
    { key: 'dbs', label: 'Databases', kind: 'redis-db' as ObjectKind, creatable: false, emptyText: '（无数据库）' },
  ],
  // Future data sources plug in here, e.g.:
  // mysql: [{ key: 'tables', label: 'Tables', kind: 'table', creatable: true, emptyText: '（无表）' }],
  // es: [{ key: 'indices', label: 'Indices', kind: 'index', creatable: true, emptyText: '（无索引）' }],
}

function collectionsOf(type: string): ObjectCollection[] {
  return COLLECTIONS_BY_TYPE[type] ?? []
}

const expanded = ref<Record<string, boolean>>({})
const topicsByConn = ref<Record<string, Topic[]>>({})
const redisDBs = ref<Record<string, RedisDBInfo[]>>({})
// --- MySQL / TiDB、Hive 二级树:与 ClickHouse 同构(库 → 表),状态由
// useLazySubtree 实例持有,见下方各自实例化处 ---
// --- PostgreSQL 三级树:数据库 → schema → relation(逐级懒加载并缓存) ---
const pgDBs = ref<Record<string, string[]>>({})
// 键 `${connId}/${db}`:每个数据库节点独立的 schema 清单与展开状态。
const pgSchemasByDb = ref<Record<string, string[]>>({})
const pgSchemaLoadingByDb = ref<Record<string, boolean>>({})
const pgExpandedByDb = ref<Record<string, boolean>>({})
// 键 `${connId}/${db}/${schema}`:每个 schema 节点独立的 relation 清单与展开状态。
const pgRelationsBySchema = ref<Record<string, PostgresRelationInfo[]>>({})
const pgRelationLoadingBySchema = ref<Record<string, boolean>>({})
const pgExpandedBySchema = ref<Record<string, boolean>>({})
const pgRelationFilterBySchema = ref<Record<string, string>>({})
// --- Elasticsearch 一级树(连接 → 索引;系统索引由后端过滤,无二级展开) ---
const esIndices = ref<Record<string, EsIndexInfo[]>>({})
// ES 一级树的索引名模糊过滤词(按连接缓存)。
const esIndexFilterByConn = ref<Record<string, string>>({})
const groupsByConn = ref<Record<string, ConsumerGroup[]>>({})
const loadingByConn = ref<Record<string, boolean>>({})
const errorByConn = ref<Record<string, string>>({})
const searchByConn = ref<Record<string, string>>({})
const groupSearchByConn = ref<Record<string, string>>({})
const activeSectionByConn = ref<Record<string, string>>({})

// --- 搜索防抖:输入框即时回显,过滤结果延迟重算 ------------------------------
// 上方 7 个搜索词 Record 仍由各搜索框 v-model 直写(输入即时回显),过滤侧
// 统一读下方防抖副本,击键停止后才触发一次「过滤 + 排序」,避免每击键全量
// map/filter/sort。vitest(jsdom)不推进真实定时器,而既有用例在 setValue
// 后同刻断言过滤结果,因此测试构建(MODE === 'test')退化为 0ms 同步直通,
// 生产构建保持 150ms 防抖,渲染输出不受影响。
const SEARCH_DEBOUNCE_MS = import.meta.env.MODE === 'test' ? 0 : 150

// useDebouncedRef / keyedComputed 已迁至 @/composables/useLazySubtree.ts 并
// 原样导出:三棵二级树(库 → 表)与下方搜索框共用同一实现。

const debouncedSearchByConn = useDebouncedRef(searchByConn, SEARCH_DEBOUNCE_MS)
const debouncedGroupSearchByConn = useDebouncedRef(groupSearchByConn, SEARCH_DEBOUNCE_MS)
const debouncedPgRelationFilterBySchema = useDebouncedRef(pgRelationFilterBySchema, SEARCH_DEBOUNCE_MS)
const debouncedEsIndexFilterByConn = useDebouncedRef(esIndexFilterByConn, SEARCH_DEBOUNCE_MS)

function isExpanded(id: string): boolean {
  return !!expanded.value[id]
}

async function toggle(conn: Connection): Promise<void> {
  const id = conn.id
  const nowExpanded = !expanded.value[id]
  expanded.value[id] = nowExpanded
  if (!nowExpanded) {
    // Multi-select state is ephemeral and scoped to the visible tree node, so
    // collapsing the connection discards it.
    clearBatchState(id)
    return
  }
  // 分区状态跨折叠保留:重新展开时「索引模板」分区重新挂载,按约定重拉模板。
  if (conn.type === 'es' && esSectionOf(id) === 'es-templates') {
    void loadEsTemplates(id)
  }
  if (
    !topicsByConn.value[id] && !redisDBs.value[id] && !chSub.dbsOf(id) && !mysqlSub.dbsOf(id) && !esIndices.value[id] && !pgDBs.value[id] && !hiveSub.dbsOf(id)
    && (conn.type === 'kafka' || conn.type === 'redis' || conn.type === 'clickhouse'
      || conn.type === 'mysql' || conn.type === 'tidb' || conn.type === 'es' || conn.type === 'postgres' || conn.type === 'hive')
  ) {
    await load(id)
  }
}

async function load(connId: string): Promise<void> {
  loadingByConn.value[connId] = true
  errorByConn.value[connId] = ''
  connStore.setStatus(connId, 'connecting')
  try {
    const type = props.connections.find((c) => c.id === connId)?.type
    if (type === 'redis') {
      redisDBs.value[connId] = await getApi().listRedisDBs(connId)
      connStore.setStatus(connId, 'connected')
    } else if (type === 'clickhouse') {
      // 后端默认已过滤系统库。
      chSub.setDbs(connId, await getApi().listCHDatabases(connId))
      connStore.setStatus(connId, 'connected')
    } else if (type === 'mysql' || type === 'tidb') {
      // 后端默认已过滤系统库(information_schema/performance_schema 等)。
      mysqlSub.setDbs(connId, (await getApi().listMysqlDatabases?.(connId)) ?? [])
      connStore.setStatus(connId, 'connected')
    } else if (type === 'hive') {
      // 后端默认已过滤系统库(default 保留)。
      hiveSub.setDbs(connId, (await getApi().listHiveDatabases?.(connId)) ?? [])
      connStore.setStatus(connId, 'connected')
    } else if (type === 'es') {
      // 后端默认已过滤系统索引(.kibana* 等)。
      esIndices.value[connId] = (await getApi().listEsIndices?.(connId)) ?? []
      connStore.setStatus(connId, 'connected')
    } else if (type === 'postgres') {
      // 后端默认已过滤模板库等系统数据库。
      pgDBs.value[connId] = (await getApi().listPostgresDatabases?.(connId)) ?? []
      connStore.setStatus(connId, 'connected')
    } else {
      const [topics, groups] = await Promise.all([
        getApi().listTopics(connId),
        getApi().listConsumerGroups(connId),
      ])
      topicsByConn.value[connId] = topics
      groupsByConn.value[connId] = groups
      connStore.setStatus(connId, 'connected')
    }
  } catch (e) {
    errorByConn.value[connId] = e instanceof Error ? e.message : String(e)
    connStore.setStatus(connId, 'error')
  } finally {
    loadingByConn.value[connId] = false
  }
  // Message counts load in the background: they cost one batched offset
  // lookup, but must never delay or fail the tree itself.
  void loadCounts(connId)
}

// --- ClickHouse 二级树:数据库 → 表(懒加载/过滤/计数徽标统一走 useLazySubtree) ---

const chSub = useLazySubtree<CHTableInfo>({
  // 后端不含 system 库表;CH 的表清单接口必在,无需可选链兜底。
  listTables: (connId, db) => getApi().listCHTables({
    connection_id: connId,
    database: db,
    show_system: false,
  }),
  onError: (connId, message) => { errorByConn.value[connId] = message },
  debounceMs: SEARCH_DEBOUNCE_MS,
})
// 过滤词 ref 解构回父级顶层命名,模板 v-model 继续直接写它(自动解包)。
const { filterByDb: chTableFilterByDb } = chSub

// --- MySQL / TiDB 二级树:数据库 → 表(展开库节点时懒加载表清单,统一走 useLazySubtree) ---

const mysqlSub = useLazySubtree<MysqlTableInfo>({
  // 系统库表由后端过滤;接口为可选成员,缺失时兜底空清单。
  listTables: async (connId, db) => (await getApi().listMysqlTables?.({
    connection_id: connId,
    database: db,
  })) ?? [],
  onError: (connId, message) => { errorByConn.value[connId] = message },
  debounceMs: SEARCH_DEBOUNCE_MS,
})
// 过滤词 ref 解构回父级顶层命名,模板 v-model 继续直接写它(自动解包)。
const { filterByDb: mysqlTableFilterByDb } = mysqlSub

// --- Hive 二级树:数据库 → 表(展开库节点时懒加载表清单,统一走 useLazySubtree) ---

const hiveSub = useLazySubtree<HiveTableInfo>({
  // 系统库表由后端过滤;接口为可选成员,缺失时兜底空清单。
  listTables: async (connId, db) => (await getApi().listHiveTables?.({
    connection_id: connId,
    database: db,
  })) ?? [],
  onError: (connId, message) => { errorByConn.value[connId] = message },
  debounceMs: SEARCH_DEBOUNCE_MS,
})
// 过滤词 ref 解构回父级顶层命名,模板 v-model 继续直接写它(自动解包)。
const { filterByDb: hiveTableFilterByDb } = hiveSub

// --- PostgreSQL 三级树:数据库 → schema → relation ---

function pgDbKey(connId: string, db: string): string {
  return `${connId}/${db}`
}

function pgSchemaKey(connId: string, db: string, schema: string): string {
  return `${connId}/${db}/${schema}`
}

function isPgDbExpanded(connId: string, db: string): boolean {
  return !!pgExpandedByDb.value[pgDbKey(connId, db)]
}

// togglePgDb 展开/收起数据库节点;首次展开懒加载 schema 清单。
async function togglePgDb(connId: string, db: string): Promise<void> {
  const key = pgDbKey(connId, db)
  pgExpandedByDb.value[key] = !pgExpandedByDb.value[key]
  if (!pgExpandedByDb.value[key] || pgSchemasByDb.value[key]) return
  pgSchemaLoadingByDb.value[key] = true
  try {
    pgSchemasByDb.value[key] = (await getApi().listPostgresSchemas?.({
      connection_id: connId,
      database: db,
    })) ?? []
  } catch (e) {
    errorByConn.value[connId] = e instanceof Error ? e.message : String(e)
  } finally {
    pgSchemaLoadingByDb.value[key] = false
  }
}

function isPgSchemaExpanded(connId: string, db: string, schema: string): boolean {
  return !!pgExpandedBySchema.value[pgSchemaKey(connId, db, schema)]
}

// togglePgSchema 展开/收起 schema 节点;首次展开懒加载 relation 清单(系统
// schema 由后端过滤)。
async function togglePgSchema(connId: string, db: string, schema: string): Promise<void> {
  const key = pgSchemaKey(connId, db, schema)
  pgExpandedBySchema.value[key] = !pgExpandedBySchema.value[key]
  if (!pgExpandedBySchema.value[key] || pgRelationsBySchema.value[key]) return
  pgRelationLoadingBySchema.value[key] = true
  try {
    pgRelationsBySchema.value[key] = (await getApi().listPostgresTables?.({
      connection_id: connId,
      database: db,
      schema,
    })) ?? []
  } catch (e) {
    errorByConn.value[connId] = e instanceof Error ? e.message : String(e)
  } finally {
    pgRelationLoadingBySchema.value[key] = false
  }
}

function pgRelationFilterOf(connId: string, db: string, schema: string): string {
  // 读防抖副本,与 filteredPgRelations/徽标共用同一份生效过滤词。
  return (debouncedPgRelationFilterBySchema.value[pgSchemaKey(connId, db, schema)] ?? '').trim()
}

// openPgRelation 打开 PG relation 详情页;双击与键盘(Enter/Space)共用入口。
function openPgRelation(connId: string, db: string, r: PostgresRelationInfo): void {
  emit('open-postgres-table', connId, db, r.schema, r.relation, r.relation_type)
}

// filteredPgRelations 对 relation 名做本地模糊过滤(fuzzyFilter),按相关度
// 排序。结果按 schema key 缓存为 computed(与 useLazySubtree 内 filtered 同构)。
const filteredPgRelationsByKey = keyedComputed((key: string): PostgresRelationInfo[] =>
  fuzzyFilter(pgRelationsBySchema.value[key] ?? [], debouncedPgRelationFilterBySchema.value[key] ?? '', (r) => r.relation),
)
function filteredPgRelations(connId: string, db: string, schema: string): PostgresRelationInfo[] {
  return filteredPgRelationsByKey(pgSchemaKey(connId, db, schema))
}

// pgSchemaCountLabel schema 节点的 relation 计数徽标:未过滤显示总数,
// 过滤中显示「可见/总数」(与 MySQL 库节点徽标同构)。
function pgSchemaCountLabel(connId: string, db: string, schema: string): string {
  const total = (pgRelationsBySchema.value[pgSchemaKey(connId, db, schema)] ?? []).length
  const q = pgRelationFilterOf(connId, db, schema)
  if (!q) return String(total)
  return `${filteredPgRelations(connId, db, schema).length}/${total}`
}

// --- ES 索引过滤(一级树,过滤词按连接缓存,与 MySQL 表过滤同构) ---

function esIndexFilterOf(connId: string): string {
  // 读防抖副本,与 filteredEsIndices/徽标共用同一份生效过滤词。
  return (debouncedEsIndexFilterByConn.value[connId] ?? '').trim()
}

// filteredEsIndices 对索引名做本地模糊过滤(fuzzyFilter),按相关度排序。
// 结果按连接 key 缓存为 computed(与 useLazySubtree 内 filtered 同构)。
const filteredEsIndicesByKey = keyedComputed((key: string): EsIndexInfo[] =>
  fuzzyFilter(esIndices.value[key] ?? [], debouncedEsIndexFilterByConn.value[key] ?? '', (i) => i.name),
)
function filteredEsIndices(connId: string): EsIndexInfo[] {
  return filteredEsIndicesByKey(connId)
}

// esIndexCountLabel 索引计数徽标:未过滤显示总数,过滤中显示「可见/总数」。
function esIndexCountLabel(connId: string): string {
  const total = (esIndices.value[connId] ?? []).length
  const q = esIndexFilterOf(connId)
  if (!q) return String(total)
  return `${filteredEsIndices(connId).length}/${total}`
}

// --- ES 分区(索引 / 索引模板):与 Kafka 的分段按钮同构,互斥切换 ---

// 索引模板清单项,与 types.ts 的 EsTemplateInfo(wailsjs 生成的
// model.EsTemplateInfo)一致。

const esTemplatesByConn = ref<Record<string, EsTemplateInfo[]>>({})
const esTemplatesLoadingByConn = ref<Record<string, boolean>>({})
const esTemplatesErrorByConn = ref<Record<string, string>>({})

// esSectionOf 返回 ES 连接当前激活的分区,默认「索引」(es-indices)。
function esSectionOf(connId: string): string {
  return activeSectionByConn.value[connId] ?? 'es-indices'
}

function switchEsSection(connId: string, section: string): void {
  activeSectionByConn.value[connId] = section
  // 模板清单按连接缓存,每次激活「索引模板」分区都重拉一次以刷新。
  if (section === 'es-templates') void loadEsTemplates(connId)
}

async function loadEsTemplates(connId: string): Promise<void> {
  esTemplatesLoadingByConn.value[connId] = true
  esTemplatesErrorByConn.value[connId] = ''
  try {
    // listEsTemplates 已在 client.ts 的 Api 声明为可选成员(绑定已生成);
    // 经 WailsApi 调用 ListEsTemplates,缺失时可选链兜底为空清单。
    esTemplatesByConn.value[connId] = (await getApi().listEsTemplates?.(connId)) ?? []
  } catch (e) {
    esTemplatesErrorByConn.value[connId] = e instanceof Error ? e.message : String(e)
  } finally {
    esTemplatesLoadingByConn.value[connId] = false
  }
}

// --- ES 分区内的集合编辑(新建/删除/修改):与 Kafka 的 create-form、
// context menu、ConfirmDialog 链路同构 ---

// ES 索引新建表单:由索引分区标题行(＋ 角标)展开。成功后清空表单(保持
// 打开,便于连续创建)并重拉索引列表;失败原因显示在表单内。
const esCreateMeta = ref<{ connId: string } | null>(null)
const esCreateForm = reactive({ name: '', shards: 1, replicas: 1 })
const esCreating = ref(false)
const esCreateError = ref<string | null>(null)

function isEsCreatingFor(connId: string): boolean {
  return esCreateMeta.value?.connId === connId
}

function toggleEsCreate(conn: Connection): void {
  if (isEsCreatingFor(conn.id)) {
    esCreateMeta.value = null
    return
  }
  esCreateMeta.value = { connId: conn.id }
  esCreateForm.name = ''
  esCreateForm.shards = 1
  esCreateForm.replicas = 1
  esCreateError.value = null
}

function closeEsCreate(): void {
  esCreateMeta.value = null
}

async function submitEsCreate(conn: Connection): Promise<void> {
  const name = esCreateForm.name.trim()
  if (!name) {
    esCreateError.value = '索引名不能为空'
    return
  }
  esCreating.value = true
  esCreateError.value = null
  try {
    await getApi().esCreateIndex?.({
      connection_id: conn.id,
      index: name,
      shards: Math.max(1, Math.trunc(esCreateForm.shards) || 1),
      replicas: Math.max(1, Math.trunc(esCreateForm.replicas) || 1),
    })
    // 成功:清空表单(保留打开)并重拉索引列表;失败:错误留在表单内。
    esCreateForm.name = ''
    esCreateForm.shards = 1
    esCreateForm.replicas = 1
    await load(conn.id)
  } catch (e) {
    esCreateError.value = e instanceof Error ? e.message : String(e)
  } finally {
    esCreating.value = false
  }
}

// 修改索引设置弹窗:当前仅暴露副本数(number_of_replicas);payload 由前端
// 组装为设置项 JSON 文本交后端解析转发,前端禁止拼 DSL。
const esSettingsMeta = ref<{ connId: string; index: string } | null>(null)
const esSettingsReplicas = ref(1)
const esSettingsSaving = ref(false)
const esSettingsError = ref<string | null>(null)

async function submitEsSettings(): Promise<void> {
  const m = esSettingsMeta.value
  if (!m || esSettingsSaving.value) return
  const replicas = esSettingsReplicas.value
  if (!Number.isInteger(replicas) || replicas < 0) {
    esSettingsError.value = '请输入非负整数副本数'
    return
  }
  esSettingsSaving.value = true
  esSettingsError.value = null
  try {
    await getApi().esUpdateIndexSettings?.({
      connection_id: m.connId,
      index: m.index,
      settings_json: JSON.stringify({ number_of_replicas: replicas }),
    })
    esSettingsMeta.value = null
    await load(m.connId)
  } catch (e) {
    esSettingsError.value = e instanceof Error ? e.message : String(e)
  } finally {
    esSettingsSaving.value = false
  }
}

// countsByConn caches per-topic message counts per connection (retained =
// records within retention). Unknown topics (fetch failed / not yet loaded)
// simply have no badge.
const countsByConn = ref<Record<string, Record<string, TopicMessageCounts>>>({})

async function loadCounts(connId: string): Promise<void> {
  const topics = topicsByConn.value[connId] ?? []
  if (topics.length === 0) return
  try {
    const counts = await getApi().getTopicMessageCounts(connId, topics.map((t) => t.name))
    countsByConn.value[connId] = counts
  } catch {
    countsByConn.value[connId] = {}
  }
}

function retainedOf(connId: string, topic: string): number | null {
  return countsByConn.value[connId]?.[topic]?.retained ?? null
}

function hasSearch(connId: string): boolean {
  // 与 filteredTopics 同源:读防抖后的生效过滤词,保证空态文案与列表一致。
  return (debouncedSearchByConn.value[connId] ?? '').trim().length > 0
}

function hasGroupSearch(connId: string): boolean {
  // 与 filteredGroups 同源:读防抖后的生效过滤词,保证空态文案与列表一致。
  return (debouncedGroupSearchByConn.value[connId] ?? '').trim().length > 0
}

// filteredTopics 对 topic 名做本地模糊过滤(fuzzyFilter),按相关度排序。
// 结果按连接 key 缓存为 computed:v-for、空态与全选判断共享同一份。
const filteredTopicsByKey = keyedComputed((key: string): Topic[] =>
  fuzzyFilter(topicsByConn.value[key] ?? [], debouncedSearchByConn.value[key] ?? '', (t) => t.name),
)
function filteredTopics(connId: string): Topic[] {
  return filteredTopicsByKey(connId)
}

// filteredGroups 对消费组名做本地模糊过滤(fuzzyFilter),按相关度排序。
// 结果按连接 key 缓存为 computed(与 filteredTopics 同构)。
const filteredGroupsByKey = keyedComputed((key: string): ConsumerGroup[] =>
  fuzzyFilter(groupsByConn.value[key] ?? [], debouncedGroupSearchByConn.value[key] ?? '', (g) => g.name),
)
function filteredGroups(connId: string): ConsumerGroup[] {
  return filteredGroupsByKey(connId)
}

// activeSection returns the currently selected object collection for a
// connection, defaulting to its first collection (Topics for Kafka).
function activeSection(connId: string, cols: ObjectCollection[]): string {
  return activeSectionByConn.value[connId] ?? cols[0]?.key ?? ''
}

// objectCount returns the total item count for a collection, used by the
// segmented control badges so users can see how full each section is.
function objectCount(connId: string, col: ObjectCollection): number {
  switch (col.kind) {
    case 'topic':
      return (topicsByConn.value[connId] ?? []).length
    case 'group':
      return (groupsByConn.value[connId] ?? []).length
    default:
      return 0
  }
}

const createMeta = ref<{ connId: string; kind: ObjectKind } | null>(null)
const createForm = reactive({ name: '', partitions: 1, replication: 1 })
const creating = ref(false)
const createError = ref<string | null>(null)

function isCreatingFor(connId: string, kind: ObjectKind): boolean {
  return createMeta.value?.connId === connId && createMeta.value?.kind === kind
}

function openCreate(conn: Connection, col: ObjectCollection): void {
  createMeta.value = { connId: conn.id, kind: col.kind }
  createForm.name = ''
  createForm.partitions = 1
  createForm.replication = 1
  createError.value = null
}

function toggleCreate(conn: Connection, col: ObjectCollection): void {
  if (isCreatingFor(conn.id, col.kind)) {
    closeCreate()
  } else {
    openCreate(conn, col)
  }
}

function closeCreate(): void {
  createMeta.value = null
}

async function submitCreate(conn: Connection): Promise<void> {
  const name = createForm.name.trim()
  if (!name) {
    createError.value = '名称不能为空'
    return
  }
  creating.value = true
  createError.value = null
  try {
    const kind = createMeta.value?.kind
    if (kind === 'topic') {
      await getApi().createTopic({
        connection_id: conn.id,
        topic: name,
        partitions: Math.max(1, createForm.partitions || 1),
        replication_factor: Math.max(1, createForm.replication || 1),
      })
      // future: if (kind === 'table') await getApi().createTable({ ... })
    }
    await load(conn.id)
    closeCreate()
  } catch (e) {
    createError.value = e instanceof Error ? e.message : String(e)
  } finally {
    creating.value = false
  }
}

// confirm holds the pending destructive action. The dialog is rendered by
// Wails (window.confirm is silently unsupported in WKWebView and always
// returns false), so deletion is confirmed in-app instead. `names` marks a
// batch delete of several topics at once; mysql-table 节点额外带 action
// (drop/truncate)与 db,用于删除表与截断表两种确认。
const confirm = ref<{ connId: string; kind: ObjectKind; name: string; names?: string[]; action?: 'drop' | 'truncate'; db?: string } | null>(null)

const confirmMessage = computed(() => {
  const pending = confirm.value
  if (!pending) return ''
  if (pending.names) return `确认删除 ${pending.names.length} 个 Topic？此操作不可恢复。`
  if (pending.kind === 'mysql-table' || pending.kind === 'hive-table') {
    const target = `${pending.db ?? ''}.${pending.name}`
    if (pending.action === 'truncate') return `确认截断表「${target}」？表数据将被清空，此操作不可恢复。`
    return `确认删除表「${target}」？表结构与数据将被删除，此操作不可恢复。`
  }
  if (pending.kind === 'es-index') return `确认删除索引「${pending.name}」？此操作不可恢复。`
  if (pending.kind === 'es-template') return `确认删除索引模板「${pending.name}」？此操作不可恢复。`
  return `确认删除 ${pending.kind === 'topic' ? 'Topic' : 'Consumer Group'}「${pending.name}」？此操作不可恢复。`
})

const confirmText = computed(() => {
  const pending = confirm.value
  if (!pending) return '删除'
  if (pending.names) return `删除 ${pending.names.length} 个 Topic`
  if (pending.kind === 'mysql-table' || pending.kind === 'hive-table') return pending.action === 'truncate' ? '截断表' : '删除表'
  if (pending.kind === 'es-index') return '删除索引'
  if (pending.kind === 'es-template') return '删除模板'
  return `删除 ${pending.kind === 'topic' ? 'Topic' : '消费组'}`
})

// detailMeta holds the topic whose detail drawer is open; null hides it.
// edit opens the drawer straight into config-editing mode (编辑配置 entry).
const detailMeta = ref<{ connId: string; topic: string; edit: boolean } | null>(null)

// --- 连接排序:hover ↑↓ 微调 / 右键菜单上移下移 / 原生拖拽 -------------------

// props.connections 即侧栏的展示顺序(store 列表),所有排序入口都基于它
// 计算完整 id 顺序,经 emit('reorder', ids) 交外层持久化。
const connIds = computed(() => props.connections.map((c) => c.id))

function isFirstConn(id: string): boolean {
  return connIds.value[0] === id
}

function isLastConn(id: string): boolean {
  return connIds.value.length > 0 && connIds.value[connIds.value.length - 1] === id
}

// moveConn 上移/下移一位;边界由按钮 disabled 与菜单省略项兜底,这里再守一道。
function moveConn(conn: Connection, delta: -1 | 1): void {
  if ((delta === -1 && isFirstConn(conn.id)) || (delta === 1 && isLastConn(conn.id))) return
  emit('reorder', moveId(connIds.value, conn.id, delta))
}

// 原生 HTML5 拖拽:仅顶层连接行 draggable;dragstart 记录源 id(Firefox 需要
// setData 才能发起拖拽),dragover 按鼠标在目标行的上/下半部计算插入位,
// drop 用最后一次 dragover 的插入位重排。拖拽与点击由浏览器天然区分
// (拖后不触发 click),无需手动抑制。
const dragFromId = ref<string | null>(null)
const dropHint = ref<{ targetId: string; place: 'before' | 'after' } | null>(null)

function onConnDragStart(conn: Connection, e: DragEvent): void {
  dragFromId.value = conn.id
  dropHint.value = null
  if (e.dataTransfer) {
    e.dataTransfer.effectAllowed = 'move'
    e.dataTransfer.setData('text/plain', conn.id)
  }
}

function onConnDragOver(conn: Connection, e: DragEvent): void {
  if (!dragFromId.value) return
  e.preventDefault()
  if (e.dataTransfer) e.dataTransfer.dropEffect = 'move'
  // 拖回自身行不显示插入位,drop 也会被忽略。
  if (conn.id === dragFromId.value) {
    dropHint.value = null
    return
  }
  // jsdom 无几何信息(rect 全 0),clientY 未传时按上半部处理。
  const rect = (e.currentTarget as HTMLElement | null)?.getBoundingClientRect()
  const after = rect ? e.clientY > rect.top + rect.height / 2 : false
  dropHint.value = { targetId: conn.id, place: after ? 'after' : 'before' }
}

function onConnDrop(conn: Connection, e: DragEvent): void {
  e.preventDefault()
  // 源 id 优先取 dragstart 记录的值,跨组件拖入时回退 dataTransfer。
  const fromId = dragFromId.value || e.dataTransfer?.getData('text/plain') || ''
  const place = dropHint.value?.place ?? 'before'
  clearConnDragState()
  if (!fromId || fromId === conn.id) return
  const next = reorderIds(connIds.value, fromId, conn.id, place)
  if (next.join('\u0000') !== connIds.value.join('\u0000')) emit('reorder', next)
}

function onConnDragEnd(): void {
  clearConnDragState()
}

function clearConnDragState(): void {
  dragFromId.value = null
  dropHint.value = null
}

// --- Right-click context menus ----------------------------------------------

// ctxMenu targets the right-clicked tree node; items differ per object kind.
// db 仅 mysql-table 节点使用(表所属的数据库);conn 仅 connection 节点使用
// (右键菜单需要按 type 与连接状态动态出项,并透传给编辑连接 emit)。
const ctxMenu = ref<{ x: number; y: number; kind: ObjectKind; connId: string; name: string; partitions: number; db?: string; conn?: Connection } | null>(null)

const ctxItems = computed<ContextMenuItem[]>(() => {
  if (!ctxMenu.value) return []
  if (ctxMenu.value.kind === 'connection') {
    const c = ctxMenu.value.conn
    if (!c) return []
    // 连接节点的破坏性/低频操作收敛在右键菜单:首项随连接状态切换文案,
    // 类型专属项(kafka 健康诊断 / es 集群监控)居中,分隔线隔开排序/编辑与
    // 破坏性的删除。行内 hover 按钮仅保留 ↑↓ 排序微调(见 conn-row)。
    const items: ContextMenuItem[] = [{ key: 'conn-toggle', label: isConnected(c.id) ? '断开连接' : '打开连接' }]
    if (c.type === 'kafka') items.push({ key: 'conn-health', label: '集群健康' })
    if (c.type === 'es') items.push({ key: 'conn-es-monitor', label: '集群监控' })
    items.push(
      { key: 'conn-sep', label: '', separator: true },
      { key: 'conn-edit', label: '编辑连接' },
      { key: 'conn-delete', label: '删除连接', danger: true },
    )
    // 上移/下移与行内 hover 按钮同一语义;ContextMenu 不支持禁用项,
    // 边界(首行上移/末行下移/仅一个连接)时直接省略该项。
    const editIdx = items.findIndex((it) => it.key === 'conn-edit')
    if (!isFirstConn(c.id)) items.splice(editIdx, 0, { key: 'move-up', label: '上移' })
    if (!isLastConn(c.id)) items.splice(items.findIndex((it) => it.key === 'conn-edit'), 0, { key: 'move-down', label: '下移' })
    return items
  }
  if (ctxMenu.value.kind === 'mysql-table') {
    return [
      { key: 'open-mysql-table', label: '打开表' },
      { key: 'mysql-edit-columns', label: '编辑表字段…' },
      { key: 'mysql-export', label: '导出表…' },
      { key: 'mysql-truncate', label: '截断表', danger: true },
      { key: 'mysql-drop', label: '删除表', danger: true },
    ]
  }
  if (ctxMenu.value.kind === 'hive-table') {
    return [
      { key: 'open-hive-table', label: '打开表' },
      { key: 'hive-edit-columns', label: '编辑表字段…' },
      { key: 'hive-export', label: '导出表结构' },
      { key: 'hive-truncate', label: '截断表', danger: true },
      { key: 'hive-drop', label: '删除表', danger: true },
    ]
  }
  if (ctxMenu.value.kind === 'topic') {
    return [
      { key: 'browse', label: '打开消息浏览' },
      { key: 'detail', label: 'Topic 详情' },
      { key: 'edit-config', label: '编辑配置…' },
      { key: 'expand', label: '扩充分区…' },
      { key: 'reset-offset', label: '重置消费位点…' },
      { key: 'delete', label: '删除 Topic', danger: true },
    ]
  }
  if (ctxMenu.value.kind === 'es-index') {
    return [
      { key: 'open-es-index', label: '打开索引' },
      { key: 'es-settings', label: '修改设置…' },
      { key: 'delete-es-index', label: '删除索引', danger: true },
    ]
  }
  if (ctxMenu.value.kind === 'es-template') {
    return [
      { key: 'es-edit-template', label: '编辑模板' },
      { key: 'delete-es-template', label: '删除模板', danger: true },
    ]
  }
  return [
    { key: 'open-group', label: '打开消费组' },
    { key: 'reset-offset-group', label: '重置消费位点…' },
    { key: 'delete-group', label: '删除消费组', danger: true },
  ]
})

function openTopicMenu(e: MouseEvent, connId: string, name: string, partitions: number): void {
  ctxMenu.value = { x: e.clientX, y: e.clientY, kind: 'topic', connId, name, partitions }
}

function openGroupMenu(e: MouseEvent, connId: string, name: string): void {
  ctxMenu.value = { x: e.clientX, y: e.clientY, kind: 'group', connId, name, partitions: 0 }
}

function openEsIndexMenu(e: MouseEvent, connId: string, name: string): void {
  ctxMenu.value = { x: e.clientX, y: e.clientY, kind: 'es-index', connId, name, partitions: 0 }
}

function openEsTemplateMenu(e: MouseEvent, connId: string, name: string): void {
  ctxMenu.value = { x: e.clientX, y: e.clientY, kind: 'es-template', connId, name, partitions: 0 }
}

function openMysqlTableMenu(e: MouseEvent, connId: string, db: string, name: string): void {
  ctxMenu.value = { x: e.clientX, y: e.clientY, kind: 'mysql-table', connId, name, partitions: 0, db }
}

function openHiveTableMenu(e: MouseEvent, connId: string, db: string, name: string): void {
  ctxMenu.value = { x: e.clientX, y: e.clientY, kind: 'hive-table', connId, name, partitions: 0, db }
}

// openConnMenu 打开连接节点自身的右键菜单;行内低频操作(编辑/删除/健康等)
// 仍收敛于此,行内仅保留 ↑↓ 排序微调按钮。
function openConnMenu(conn: Connection, e: MouseEvent): void {
  ctxMenu.value = { x: e.clientX, y: e.clientY, kind: 'connection', connId: conn.id, name: conn.name, partitions: 0, conn }
}

function closeCtxMenu(): void {
  ctxMenu.value = null
}

function onCtxSelect(key: string): void {
  const m = ctxMenu.value
  if (!m) return
  switch (key) {
    case 'conn-toggle':
      // 打开/断开连接(与原行内按钮同一入口);菜单本身在 pick 后已关闭。
      if (m.conn) void onToggleConnect(m.conn)
      break
    case 'move-up':
      // 与行内 ↑ 按钮同一入口(moveConn 内部再做一次边界守卫)。
      if (m.conn) moveConn(m.conn, -1)
      break
    case 'move-down':
      if (m.conn) moveConn(m.conn, 1)
      break
    case 'conn-health':
      emit('open-health', m.connId)
      break
    case 'conn-es-monitor':
      emit('open-es-monitor', m.connId)
      break
    case 'conn-edit':
      if (m.conn) emit('edit-connection', m.conn)
      break
    case 'conn-delete':
      // 删除连接仍由 App.vue 的确认弹窗链路兜底,这里只透传 id。
      emit('delete', m.connId)
      break
    case 'browse':
      emit('open-topic', m.connId, m.name, (topicsByConn.value[m.connId] ?? []).find((t) => t.name === m.name)?.partitions.map((p) => p.id) ?? [])
      break
    case 'detail':
      detailMeta.value = { connId: m.connId, topic: m.name, edit: false }
      break
    case 'edit-config':
      detailMeta.value = { connId: m.connId, topic: m.name, edit: true }
      break
    case 'expand':
      expandMeta.value = { connId: m.connId, topic: m.name, target: m.partitions }
      expandError.value = null
      break
    case 'reset-offset':
      resetMeta.value = { connId: m.connId, topic: m.name, group: null }
      break
    case 'reset-offset-group':
      resetMeta.value = { connId: m.connId, topic: null, group: m.name }
      break
    case 'delete':
      askDelete(props.connections.find((c) => c.id === m.connId)!, 'topic', m.name)
      break
    case 'open-group':
      emit('open-group', m.connId, m.name)
      break
    case 'delete-group':
      askDelete(props.connections.find((c) => c.id === m.connId)!, 'group', m.name)
      break
    case 'open-es-index':
      emit('open-es-index', m.connId, m.name)
      break
    case 'es-settings':
      esSettingsMeta.value = { connId: m.connId, index: m.name }
      esSettingsReplicas.value = 1
      esSettingsError.value = null
      break
    case 'delete-es-index':
      askDelete(props.connections.find((c) => c.id === m.connId)!, 'es-index', m.name)
      break
    case 'es-edit-template':
      emit('open-es-template', m.connId, m.name)
      break
    case 'delete-es-template':
      askDelete(props.connections.find((c) => c.id === m.connId)!, 'es-template', m.name)
      break
    case 'open-mysql-table':
      emit('open-mysql-table', m.connId, m.db ?? '', m.name)
      break
    case 'mysql-edit-columns':
      if (m.db !== undefined) structureMeta.value = { connId: m.connId, db: m.db, table: m.name }
      break
    case 'mysql-export':
      if (m.db !== undefined) exportMeta.value = { connId: m.connId, db: m.db, table: m.name }
      break
    case 'mysql-truncate':
      askMysqlTableConfirm(m.connId, m.db ?? '', m.name, 'truncate')
      break
    case 'mysql-drop':
      askMysqlTableConfirm(m.connId, m.db ?? '', m.name, 'drop')
      break
    case 'open-hive-table':
      emit('open-hive-table', m.connId, m.db ?? '', m.name)
      break
    case 'hive-edit-columns':
      if (m.db !== undefined) hiveStructureMeta.value = { connId: m.connId, db: m.db, table: m.name }
      break
    case 'hive-export':
      if (m.db !== undefined) void exportHiveTable(m.connId, m.db, m.name)
      break
    case 'hive-truncate':
      // 仅内部表(MANAGED_TABLE)可清空:先探测表类型,非内部表 toast 提示。
      if (m.db !== undefined) void askHiveTableConfirm(m.connId, m.db, m.name, 'truncate')
      break
    case 'hive-drop':
      if (m.db !== undefined) confirm.value = { connId: m.connId, kind: 'hive-table', name: m.name, action: 'drop', db: m.db }
      break
  }
}

// structureMeta holds the mysql table whose structure-editing dialog is open.
const structureMeta = ref<{ connId: string; db: string; table: string } | null>(null)

// exportMeta holds the mysql table whose export-options dialog is open; the
// actual export (mysqlExportTable + saveFile) lives in MysqlExportDialog.
const exportMeta = ref<{ connId: string; db: string; table: string } | null>(null)

// askMysqlTableConfirm 进入删除表/截断表的确认;文案与按钮随 action 区分。
function askMysqlTableConfirm(connId: string, db: string, table: string, action: 'drop' | 'truncate'): void {
  confirm.value = { connId, kind: 'mysql-table', name: table, action, db }
}

// --- Hive 表节点动作:结构编辑弹窗 / 导出表结构 / 截断(仅内部表)与删除 ---

// hiveStructureMeta holds the hive table whose structure-editing dialog is open.
const hiveStructureMeta = ref<{ connId: string; db: string; table: string } | null>(null)

// exportHiveTable 导出表结构(SHOW CREATE TABLE 原文)并经原生对话框存为 .sql;
// Hive 普通表不支持 INSERT VALUES,数据导出不提供,无需选项弹窗。
async function exportHiveTable(connId: string, db: string, table: string): Promise<void> {
  try {
    const res = await getApi().hiveExportTable?.({ connection_id: connId, database: db, table })
    if (res) await saveFile(res.filename, res.content, 'application/sql')
  } catch (e) {
    errorByConn.value[connId] = e instanceof Error ? e.message : String(e)
  }
}

// askHiveTableConfirm 进入截断表确认前的守卫:hiveTableColumns 探测表类型,
// 非 MANAGED_TABLE(外部表/视图等)或探测失败时 toast 提示并终止,不弹确认。
async function askHiveTableConfirm(connId: string, db: string, table: string, action: 'truncate'): Promise<void> {
  try {
    const meta = await getApi().hiveTableColumns?.({ connection_id: connId, database: db, table })
    if ((meta?.table_type ?? '') !== 'MANAGED_TABLE') {
      try {
        useToastStore().show('仅内部表(MANAGED_TABLE)支持清空')
      } catch {
        // pinia 未激活时忽略提示
      }
      return
    }
  } catch (e) {
    // 探测失败视为不可清空,不能盲目开放危险操作。
    try {
      useToastStore().show(e instanceof Error ? e.message : String(e))
    } catch {
      // pinia 未激活时忽略提示
    }
    return
  }
  confirm.value = { connId, kind: 'hive-table', name: table, action, db }
}

// expandMeta drives the 扩充分区 prompt, prefilled with the current count.
const expandMeta = ref<{ connId: string; topic: string; target: number } | null>(null)
const expanding = ref(false)
const expandError = ref<string | null>(null)

// resetMeta drives the reset-offset dialog: topic is fixed when entered from a
// topic node; group is prefilled when entered from a consumer group node.
const resetMeta = ref<{ connId: string; topic: string | null; group: string | null } | null>(null)

async function submitExpand(value: string): Promise<void> {
  const m = expandMeta.value
  if (!m || expanding.value) return
  const target = Number(value)
  if (!Number.isInteger(target) || target < 1) {
    expandError.value = '请输入正整数分区数'
    return
  }
  expanding.value = true
  expandError.value = null
  try {
    await getApi().alterTopicPartitions({ connection_id: m.connId, topic: m.topic, partitions: target })
    expandMeta.value = null
    await load(m.connId)
  } catch (e) {
    expandError.value = e instanceof Error ? e.message : String(e)
  } finally {
    expanding.value = false
  }
}

function askDelete(conn: Connection, kind: ObjectKind, name: string): void {
  confirm.value = { connId: conn.id, kind, name }
}

// Multi-select state is per connection and intentionally ephemeral: it lives
// here (not in the stores) and is cleared when leaving select mode, after a
// batch delete, or when the connection is collapsed.
const selectModeByConn = ref<Record<string, boolean>>({})
const selectedByConn = ref<Record<string, string[]>>({})
const batchFeedbackByConn = ref<Record<string, BatchDeleteFeedback | null>>({})

interface BatchDeleteFeedback {
  deleted: number
  failed: number
  failures: { name: string; error: string }[]
}

function isSelectMode(connId: string): boolean {
  return !!selectModeByConn.value[connId]
}

function selectedOf(connId: string): string[] {
  return selectedByConn.value[connId] ?? []
}

function batchFeedback(connId: string): BatchDeleteFeedback | null {
  return batchFeedbackByConn.value[connId] ?? null
}

function clearBatchState(connId: string): void {
  selectModeByConn.value[connId] = false
  selectedByConn.value[connId] = []
  batchFeedbackByConn.value[connId] = null
}

function toggleSelectMode(conn: Connection): void {
  if (isSelectMode(conn.id)) {
    clearBatchState(conn.id)
  } else {
    selectModeByConn.value[conn.id] = true
    batchFeedbackByConn.value[conn.id] = null
  }
}

// selectedSetsByConn 把「连接 → 已选名称数组」转成 Set 供行内判断:多选模式
// 下每一行渲染都要查一次勾选态,原 selectedOf(connId).includes(name) 是
// O(已选数),行数多了平方级;这里集中建一次 Set,行内判断 O(1)。
// (命名与 selectedByConn 数组版区分,selection 的增删仍走数组以保留勾选顺序。)
const selectedSetsByConn = computed<Record<string, Set<string>>>(() => {
  const sets: Record<string, Set<string>> = {}
  for (const [connId, names] of Object.entries(selectedByConn.value)) {
    sets[connId] = new Set(names)
  }
  return sets
})

function isTopicSelected(connId: string, name: string): boolean {
  return selectedSetsByConn.value[connId]?.has(name) ?? false
}

function toggleTopicSelected(connId: string, name: string): void {
  const cur = selectedOf(connId)
  selectedByConn.value[connId] = cur.includes(name)
    ? cur.filter((n) => n !== name)
    : [...cur, name]
}

// isAllSelected and toggleSelectAll operate on the visible (search filtered)
// topic list: select-all must never silently include topics the user filtered
// out of view, especially before a destructive batch delete.
function isAllSelected(connId: string): boolean {
  const list = filteredTopics(connId)
  return list.length > 0 && list.every((t) => isTopicSelected(connId, t.name))
}

function toggleSelectAll(connId: string): void {
  const list = filteredTopics(connId)
  selectedByConn.value[connId] = isAllSelected(connId) ? [] : list.map((t) => t.name)
}

function askBatchDelete(connId: string): void {
  const names = selectedOf(connId)
  if (names.length === 0) return
  confirm.value = { connId, kind: 'topic', name: '', names }
}

async function executeDelete(): Promise<void> {
  const pending = confirm.value
  if (!pending) return
  confirm.value = null
  const conn = props.connections.find((c) => c.id === pending.connId)
  if (!conn) return
  try {
    if (pending.names) {
      await executeBatchDelete(conn, pending.names)
      return
    }
    if (pending.kind === 'topic') {
      await getApi().deleteTopic({ connection_id: pending.connId, topic: pending.name })
    } else if (pending.kind === 'group') {
      await getApi().deleteConsumerGroup({ connection_id: pending.connId, group: pending.name })
    } else if (pending.kind === 'mysql-table') {
      const db = pending.db ?? ''
      if (pending.action === 'truncate') {
        await getApi().mysqlTruncateTable?.({ connection_id: pending.connId, database: db, table: pending.name })
      } else {
        await getApi().mysqlDropTable?.({ connection_id: pending.connId, database: db, table: pending.name })
      }
      // 成功即时反馈:截断/删除已真实执行(后端审计可查),避免「静默成功」
      // 让人误以为无效;打开中的表浏览器/控制台是旧结果,重新查询即见清空。
      try {
        useToastStore().show(
          pending.action === 'truncate'
            ? `表 ${db}.${pending.name} 已截断`
            : `表 ${db}.${pending.name} 已删除`,
        )
      } catch {
        // pinia 未激活时忽略提示,不能中断后续刷新
      }
      // 删除/截断成功后只刷新该库的表清单,不动整棵连接树。
      await mysqlSub.reload(pending.connId, db)
      return
    } else if (pending.kind === 'hive-table') {
      const db = pending.db ?? ''
      if (pending.action === 'truncate') {
        await getApi().hiveTruncateTable?.({ connection_id: pending.connId, database: db, table: pending.name })
      } else {
        await getApi().hiveDropTable?.({ connection_id: pending.connId, database: db, table: pending.name })
      }
      // 成功即时反馈:截断/删除已真实执行(后端审计可查),避免「静默成功」
      // 让人误以为无效;打开中的表浏览器/控制台是旧结果,重新查询即见清空。
      try {
        useToastStore().show(
          pending.action === 'truncate'
            ? `表 ${db}.${pending.name} 已截断`
            : `表 ${db}.${pending.name} 已删除`,
        )
      } catch {
        // pinia 未激活时忽略提示,不能中断后续刷新
      }
      // 删除/截断成功后只刷新该库的表清单,不动整棵连接树。
      await hiveSub.reload(pending.connId, db)
      return
    } else if (pending.kind === 'es-index') {
      await getApi().esDeleteIndex?.({ connection_id: pending.connId, index: pending.name })
    } else if (pending.kind === 'es-template') {
      await getApi().esDeleteTemplate?.({ connection_id: pending.connId, name: pending.name })
      // 模板删除后只重拉模板清单,不影响索引列表。
      await loadEsTemplates(pending.connId)
      return
    }
    await load(pending.connId)
  } catch (e) {
    errorByConn.value[pending.connId] = e instanceof Error ? e.message : String(e)
  }
}

// executeBatchDelete deletes the given topics in one call. Per-topic failures
// are surfaced as feedback while the tree reloads regardless, so topics whose
// deletion failed stay listed. If the connection was collapsed while the call
// was in flight, the completion skips both feedback and reload: the collapse
// already discarded the multi-select state, and writing to an invisible node
// only resurfaces stale feedback on the next expand.
async function executeBatchDelete(conn: Connection, names: string[]): Promise<void> {
  const results = await getApi().deleteTopics({ connection_id: conn.id, names })
  if (!isExpanded(conn.id)) return
  const failures = results
    .filter((r) => r.error)
    .map((r) => ({ name: r.name, error: r.error }))
  batchFeedbackByConn.value[conn.id] = {
    deleted: results.length - failures.length,
    failed: failures.length,
    failures,
  }
  selectedByConn.value[conn.id] = []
  await load(conn.id)
}

// TOPIC_EXPORT_COLUMNS is deliberately data-source neutral (name + partition
// count only) so the same export shape carries over to future source types.
const TOPIC_EXPORT_COLUMNS: ExportColumn<Topic>[] = [
  { label: 'topic', value: (t) => t.name },
  { label: 'partitions', value: (t) => String(t.partitions.length) },
]

// exportTopics exports the connection's full topic list (not the search
// filtered view and not just the selection), named after the connection.
function exportTopics(conn: Connection): void {
  const topics = topicsByConn.value[conn.id] ?? []
  void saveFile(`topics-${conn.name}`, exportCsv(topics, TOPIC_EXPORT_COLUMNS), CSV_MIME)
}
</script>

<template>
  <div class="conn-tree px-2 pt-2.5 pb-3 text-[13px] text-foreground" data-test="connection-tree">
    <Button class="mb-2.5 w-full" type="button" data-test="btn-new" @click="emit('new')">
      <Plus class="size-4" aria-hidden="true" />
      新建连接
    </Button>
    <div v-if="connections.length === 0" class="mx-0.5 mb-2 rounded-lg border border-dashed border-border px-3 py-6 text-center text-xs text-muted-foreground" data-test="tree-empty">暂无连接</div>
    <div v-for="conn in connections" :key="conn.id" class="mb-px" data-test="connection">
      <div
        class="conn-row group relative flex h-8 cursor-pointer items-center gap-1.5 rounded-md px-2 transition-colors select-none hover:bg-accent"
        data-test="conn-row"
        draggable="true"
        :class="{
          active: isExpanded(conn.id),
          'bg-accent': isExpanded(conn.id),
          'drop-before': dropHint?.targetId === conn.id && dropHint.place === 'before',
          'drop-after': dropHint?.targetId === conn.id && dropHint.place === 'after',
        }"
        @click="toggle(conn)"
        @contextmenu.prevent.stop="openConnMenu(conn, $event)"
        @dragstart="onConnDragStart(conn, $event)"
        @dragover="onConnDragOver(conn, $event)"
        @drop="onConnDrop(conn, $event)"
        @dragend="onConnDragEnd"
      >
        <span
          class="flex size-4 shrink-0 items-center justify-center transition-transform"
          :class="isExpanded(conn.id) ? 'open rotate-90 text-foreground' : 'text-muted-foreground'"
          data-test="conn-caret"
        >
          <ChevronRight class="size-4" aria-hidden="true" />
        </span>
        <span
          class="conn-status size-2 shrink-0 rounded-full"
          data-test="conn-status-dot"
          :class="statusClass(conn)"
          :data-status="statusOf(conn.id)"
          :title="STATUS_LABEL[statusOf(conn.id)]"
        ></span>
        <span class="min-w-0 flex-1 truncate font-semibold" data-test="conn-name">{{ conn.name }}</span>
        <!-- 圆角小方块 + 品牌色首字母:Badge 原语承载形状,主色/字色仍走
             组件内 --type-* 局部色表(亮暗两套),见 scoped CSS 注释。 -->
        <Badge
          variant="secondary"
          class="size-[18px] shrink-0 justify-center rounded-md p-0 font-mono text-[10px] font-bold"
          :class="`type-badge-${conn.type}`"
          data-test="conn-type-badge"
          aria-hidden="true"
        >{{ typeMeta(conn).letter }}</Badge>
        <span class="conn-type shrink-0 rounded px-1.5 text-[11px] font-medium leading-4" :class="`conn-type-${conn.type}`" data-test="conn-type">{{ typeMeta(conn).label }}</span>
        <!-- ↑↓ 排序微调:hover 显现(与 leaf-del 同范式),边界 disabled 置灰;
             click.stop 防止触发行的展开/收起。 -->
        <span class="ml-0.5 flex shrink-0 items-center gap-px">
          <button
            class="flex size-[18px] items-center justify-center rounded text-muted-foreground opacity-0 transition-opacity hover:bg-accent hover:text-foreground disabled:pointer-events-none disabled:opacity-0 group-hover:opacity-100 group-hover:disabled:opacity-30"
            type="button"
            data-test="btn-move-up"
            title="上移"
            aria-label="上移"
            :disabled="isFirstConn(conn.id)"
            @click.stop="moveConn(conn, -1)"
          >
            <ArrowUp class="size-3" aria-hidden="true" />
          </button>
          <button
            class="flex size-[18px] items-center justify-center rounded text-muted-foreground opacity-0 transition-opacity hover:bg-accent hover:text-foreground disabled:pointer-events-none disabled:opacity-0 group-hover:opacity-100 group-hover:disabled:opacity-30"
            type="button"
            data-test="btn-move-down"
            title="下移"
            aria-label="下移"
            :disabled="isLastConn(conn.id)"
            @click.stop="moveConn(conn, 1)"
          >
            <ArrowDown class="size-3" aria-hidden="true" />
          </button>
        </span>
      </div>

      <div v-if="isExpanded(conn.id) && (conn.type === 'kafka' || conn.type === 'redis')" class="ml-4 border-l border-border pl-2">
        <div v-if="loadingByConn[conn.id]" class="px-1 py-1 text-muted-foreground" data-test="tree-loading">加载中…</div>
        <div v-else-if="errorByConn[conn.id]" class="px-1 py-1 text-destructive" data-test="tree-error">{{ errorByConn[conn.id] }}</div>
        <template v-else>
          <div class="segmented my-1.5 mb-2 flex gap-0.5 rounded-lg border border-border bg-muted p-0.5" data-test="section-tabs" role="tablist">
            <button
              v-for="col in collectionsOf(conn.type)"
              :key="col.key"
              type="button"
              class="segmented-btn flex min-w-0 flex-1 items-center justify-center gap-1 rounded-md px-2 py-1 text-xs font-medium text-muted-foreground transition-colors hover:text-foreground"
              :class="activeSection(conn.id, collectionsOf(conn.type)) === col.key ? 'active bg-background text-foreground shadow-sm' : ''"
              :data-test="`section-tab-${col.key}`"
              role="tab"
              :aria-selected="activeSection(conn.id, collectionsOf(conn.type)) === col.key"
              @click="activeSectionByConn[conn.id] = col.key"
            >
              <component :is="KIND_ICONS[col.kind]" class="size-3.5 shrink-0" aria-hidden="true" />
              <span class="segmented-label min-w-0 flex-1 truncate text-left">{{ col.label }}</span>
              <span class="segmented-count shrink-0 rounded-full bg-background/80 px-1.5 text-[11px] leading-4 text-muted-foreground">{{ objectCount(conn.id, col) }}</span>
            </button>
          </div>

          <button v-if="conn.type === 'kafka'"
            class="lag-entry flex w-full items-center gap-1.5 rounded-md px-2 py-1 text-left transition-colors hover:bg-accent"
            type="button"
            data-test="btn-open-lag"
            title="查看该连接所有消费组的 Lag 积压总览"
            @click="emit('open-lag', conn.id)"
          >
            <BarChart3 class="size-4 shrink-0 text-muted-foreground" aria-hidden="true" />
            <span>Lag 总览</span>
          </button>

          <template v-for="col in collectionsOf(conn.type)" :key="col.key">
            <template v-if="activeSection(conn.id, collectionsOf(conn.type)) === col.key">
            <component
              :is="col.creatable ? 'button' : 'div'"
              class="group-label mt-2 mb-0.5 flex w-full items-center justify-between px-1.5 py-1 text-left text-[11px] uppercase tracking-wide text-muted-foreground"
              :class="{ clickable: col.creatable }"
              data-test="object-group"
              :type="col.creatable ? 'button' : undefined"
              :role="col.creatable ? 'button' : undefined"
              :aria-label="col.creatable ? `新建 ${col.label}` : undefined"
              :title="col.creatable ? '新建' : undefined"
              @click="col.creatable ? toggleCreate(conn, col) : undefined"
            >
              <span class="group-title flex min-w-0 flex-1 items-center gap-1">
                <component :is="KIND_ICONS[col.kind]" class="size-3.5 shrink-0" aria-hidden="true" />
                {{ col.label }}
              </span>
              <span v-if="col.creatable" class="group-add flex size-5 items-center justify-center rounded-md text-primary transition-colors" data-test="btn-create-object" aria-hidden="true">
                <Plus class="size-3.5" />
              </span>
            </component>

            <div v-if="col.kind === 'topic' && isCreatingFor(conn.id, 'topic')" class="create-form my-1 flex flex-col gap-1.5 rounded-lg border border-border bg-muted/50 p-2" data-test="create-form">
              <Input
                :value="createForm.name"
                @input="createForm.name = ($event.target as HTMLInputElement).value"
                class="h-7 text-xs"
                type="text"
                data-test="create-name"
                placeholder="Topic 名称"
                autocapitalize="off"
                autocorrect="off"
                autocomplete="off"
                spellcheck="false"
              />
              <div class="flex gap-2">
                <Label class="flex-1 flex-col items-stretch gap-1 text-xs font-normal text-muted-foreground">
                  分区
                  <Input :value="createForm.partitions" @input="createForm.partitions = Number(($event.target as HTMLInputElement).value)" class="h-7 text-xs" type="number" min="1" data-test="create-partitions" />
                </Label>
                <Label class="flex-1 flex-col items-stretch gap-1 text-xs font-normal text-muted-foreground">
                  副本
                  <Input :value="createForm.replication" @input="createForm.replication = Number(($event.target as HTMLInputElement).value)" class="h-7 text-xs" type="number" min="1" data-test="create-replication" />
                </Label>
              </div>
              <div v-if="createError" class="create-error text-xs text-destructive" data-test="create-error">{{ createError }}</div>
              <div class="create-actions flex gap-2">
                <Button size="sm" type="button" data-test="btn-create-submit" :disabled="creating" @click="submitCreate(conn)">
                  {{ creating ? '创建中…' : '创建' }}
                </Button>
                <Button size="sm" variant="outline" type="button" data-test="btn-create-cancel" @click="closeCreate">取消</Button>
              </div>
            </div>

            <div v-if="col.key === 'topics'" class="topic-search relative my-1.5">
              <Search class="pointer-events-none absolute top-1/2 left-2 size-3.5 -translate-y-1/2 text-muted-foreground" aria-hidden="true" />
              <Input
                :value="searchByConn[conn.id]"
                @input="searchByConn[conn.id] = ($event.target as HTMLInputElement).value"
                class="h-7 pl-7 text-xs"
                type="search"
                data-test="topic-search"
                placeholder="模糊搜索 Topic…"
                autocapitalize="off"
                autocorrect="off"
                autocomplete="off"
                spellcheck="false"
              />
            </div>

            <div v-if="col.key === 'topics'" class="topic-toolbar mt-0.5 flex items-center gap-1.5">
              <button class="tool-btn rounded-md border border-border px-2 py-0.5 text-[11px] font-medium text-muted-foreground transition-colors hover:bg-accent hover:text-foreground" type="button" data-test="select-mode-toggle" @click="toggleSelectMode(conn)">
                {{ isSelectMode(conn.id) ? '退出多选' : '多选' }}
              </button>
              <template v-if="isSelectMode(conn.id)">
                <button class="tool-btn rounded-md border border-border px-2 py-0.5 text-[11px] font-medium text-muted-foreground transition-colors hover:bg-accent hover:text-foreground" type="button" data-test="select-all-topics" @click="toggleSelectAll(conn.id)">
                  {{ isAllSelected(conn.id) ? '取消全选' : '全选' }}
                </button>
                <button
                  class="tool-btn rounded-md border border-border px-2 py-0.5 text-[11px] font-medium text-muted-foreground transition-colors hover:border-destructive/60 hover:bg-destructive/10 hover:text-destructive"
                  type="button"
                  data-test="batch-delete-topics"
                  :disabled="selectedOf(conn.id).length === 0"
                  title="删除勾选的 Topic"
                  @click="askBatchDelete(conn.id)"
                >删除({{ selectedOf(conn.id).length }})</button>
              </template>
              <button class="tool-btn rounded-md border border-border px-2 py-0.5 text-[11px] font-medium text-muted-foreground transition-colors hover:bg-accent hover:text-foreground" type="button" data-test="export-topics" title="导出 Topic 列表为 CSV" @click="exportTopics(conn)">导出列表</button>
              <button
                class="tool-btn flex items-center gap-1 rounded-md border border-border px-2 py-0.5 text-[11px] font-medium text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
                type="button"
                data-test="btn-refresh-counts"
                title="重新统计各 Topic 的消息数（一次批量 offset 查询）"
                @click="loadCounts(conn.id)"
              ><RefreshCw class="size-3" aria-hidden="true" /> 消息数</button>
            </div>

            <div v-if="col.key === 'topics' && batchFeedback(conn.id)" class="batch-result my-1 rounded-lg border border-border bg-muted/50 px-2 py-1.5">
              <div class="batch-summary text-xs text-foreground" data-test="batch-delete-summary">
                删除完成：成功 {{ batchFeedback(conn.id)?.deleted ?? 0 }} / 失败 {{ batchFeedback(conn.id)?.failed ?? 0 }}
              </div>
              <div v-if="(batchFeedback(conn.id)?.failures.length ?? 0) > 0" class="batch-failures mt-1 flex flex-col gap-0.5" data-test="batch-delete-failures">
                <div v-for="f in batchFeedback(conn.id)?.failures" :key="f.name" class="batch-failure truncate text-[11px] text-destructive">{{ f.name }}：{{ f.error }}</div>
              </div>
            </div>

            <div v-if="col.key === 'consumers'" class="topic-search relative my-1.5">
              <Search class="pointer-events-none absolute top-1/2 left-2 size-3.5 -translate-y-1/2 text-muted-foreground" aria-hidden="true" />
              <Input
                :value="groupSearchByConn[conn.id]"
                @input="groupSearchByConn[conn.id] = ($event.target as HTMLInputElement).value"
                class="h-7 pl-7 text-xs"
                type="search"
                data-test="group-search"
                placeholder="模糊搜索 Consumer…"
                autocapitalize="off"
                autocorrect="off"
                autocomplete="off"
                spellcheck="false"
              />
            </div>

            <template v-if="col.kind === 'topic'">
              <div
                v-for="t in filteredTopics(conn.id)"
                :key="t.name"
                class="leaf group flex h-7 cursor-pointer items-center gap-1.5 rounded-md px-1.5 transition-colors hover:bg-accent"
                data-test="topic-node"
                :title="`${t.name} (${t.partitions.length} 分区)`"
                @dblclick="emit('open-topic', conn.id, t.name, t.partitions.map((p) => p.id))"
                @contextmenu.prevent.stop="openTopicMenu($event, conn.id, t.name, t.partitions.length)"
              >
                <input
                  v-if="isSelectMode(conn.id)"
                  type="checkbox"
                  class="leaf-check size-3.5 shrink-0 accent-primary"
                  :data-test="`topic-check-${t.name}`"
                  :checked="isTopicSelected(conn.id, t.name)"
                  @click.stop="toggleTopicSelected(conn.id, t.name)"
                  @dblclick.stop
                />
                <Hash class="size-3.5 shrink-0 text-muted-foreground" aria-hidden="true" />
                <span class="leaf-name min-w-0 flex-1 truncate" data-test="topic-name">{{ t.name }}</span>
                <span
                  v-if="retainedOf(conn.id, t.name) !== null"
                  class="leaf-count shrink-0 rounded-full bg-muted px-1.5 font-mono text-[11px] leading-4 text-muted-foreground"
                  data-test="topic-count"
                  :title="`保留期内 ${retainedOf(conn.id, t.name)!.toLocaleString()} 条`"
                >{{ formatCount(retainedOf(conn.id, t.name)!) }}</span>
                <button
                  class="leaf-info flex size-5 shrink-0 items-center justify-center rounded text-muted-foreground transition-colors hover:bg-info/10 hover:text-info"
                  type="button"
                  data-test="btn-topic-info"
                  title="Topic 详情"
                  aria-label="Topic 详情"
                  @click.stop="detailMeta = { connId: conn.id, topic: t.name, edit: false }"
                >
                  <Info class="size-3.5" aria-hidden="true" />
                </button>
                <button
                  class="leaf-del flex size-5 shrink-0 items-center justify-center rounded text-muted-foreground opacity-0 transition-colors hover:bg-destructive/10 hover:text-destructive group-hover:opacity-100"
                  type="button"
                  data-test="btn-delete-topic"
                  title="删除 Topic"
                  @click.stop="askDelete(conn, 'topic', t.name)"
                ><Trash2 class="size-3.5" aria-hidden="true" /></button>
              </div>
              <div v-if="filteredTopics(conn.id).length === 0" class="leaf muted px-1.5 py-1 text-muted-foreground" data-test="topic-empty">
                {{ hasSearch(conn.id) ? '无匹配 Topic' : col.emptyText }}
              </div>
            </template>

            <template v-else-if="col.kind === 'redis-db'">
              <div
                v-for="d in redisDBs[conn.id] ?? []"
                :key="d.index"
                class="leaf flex h-7 cursor-pointer items-center gap-1.5 rounded-md px-1.5 transition-colors hover:bg-accent"
                data-test="redis-db-node"
                :title="`DB${d.index}(${d.keys} 个键)`"
                @dblclick="emit('open-redis-keys', conn.id, d.index)"
              >
                <Database class="size-3.5 shrink-0 text-muted-foreground" aria-hidden="true" />
                <span class="leaf-name min-w-0 flex-1 truncate" data-test="redis-db-name">DB{{ d.index }}</span>
                <span class="leaf-count shrink-0 rounded-full bg-muted px-1.5 font-mono text-[11px] leading-4 text-muted-foreground" data-test="redis-db-keys">{{ d.keys }}</span>
              </div>
              <div v-if="(redisDBs[conn.id] ?? []).length === 0" class="leaf muted px-1.5 py-1 text-muted-foreground" data-test="redis-db-empty">
                {{ loadingByConn[conn.id] ? '加载中…' : '（无数据库）' }}
              </div>
            </template>
            <template v-else-if="col.kind === 'group'">
              <div
                v-for="g in filteredGroups(conn.id)"
                :key="g.name"
                class="leaf group flex h-7 cursor-pointer items-center gap-1.5 rounded-md px-1.5 transition-colors hover:bg-accent"
                data-test="group-node"
                @dblclick="emit('open-group', conn.id, g.name)"
                @contextmenu.prevent.stop="openGroupMenu($event, conn.id, g.name)"
              >
                <Users class="size-3.5 shrink-0 text-muted-foreground" aria-hidden="true" />
                <span class="leaf-name min-w-0 flex-1 truncate">{{ g.name }}</span>
                <button
                  class="leaf-del flex size-5 shrink-0 items-center justify-center rounded text-muted-foreground opacity-0 transition-colors hover:bg-destructive/10 hover:text-destructive group-hover:opacity-100"
                  type="button"
                  data-test="btn-delete-group"
                  title="删除 Consumer Group"
                  @click.stop="askDelete(conn, 'group', g.name)"
                ><Trash2 class="size-3.5" aria-hidden="true" /></button>
              </div>
              <div v-if="filteredGroups(conn.id).length === 0" class="leaf muted px-1.5 py-1 text-muted-foreground" data-test="group-empty">
                {{ hasGroupSearch(conn.id) ? '无匹配 Consumer' : col.emptyText }}
              </div>
            </template>
            </template>
          </template>
        </template>
      </div>
      <div v-else-if="isExpanded(conn.id) && conn.type === 'clickhouse'" class="ml-4 border-l border-border pl-2">
        <div v-if="loadingByConn[conn.id]" class="px-1 py-1 text-muted-foreground" data-test="tree-loading">加载中…</div>
        <div v-else-if="errorByConn[conn.id]" class="px-1 py-1 text-destructive" data-test="tree-error">{{ errorByConn[conn.id] }}</div>
        <template v-else>
          <template v-for="db in chSub.dbsOf(conn.id) ?? []" :key="db">
            <div class="leaf flex h-7 cursor-pointer items-center gap-1 rounded-md px-1.5 transition-colors hover:bg-accent" data-test="ch-db-node" :title="`数据库 ${db}`" @click="chSub.toggleDb(conn.id, db)">
              <span class="flex size-4 shrink-0 items-center justify-center text-muted-foreground transition-transform" :class="chSub.isDbExpanded(conn.id, db) ? 'open rotate-90' : ''" data-test="ch-db-caret">
                <ChevronRight class="size-3.5" aria-hidden="true" />
              </span>
              <Database class="size-3.5 shrink-0 text-muted-foreground" aria-hidden="true" />
              <span class="leaf-name min-w-0 flex-1 truncate" data-test="ch-db-name">{{ db }}</span>
              <span
                v-if="chSub.isLoaded(conn.id, db)"
                class="leaf-badge shrink-0 rounded-full bg-muted px-1.5 font-mono text-[10px] leading-4 text-muted-foreground"
                data-test="ch-db-count"
              >{{ chSub.countLabel(conn.id, db) }}</span>
            </div>
            <template v-if="chSub.isDbExpanded(conn.id, db)">
              <div class="ch-table-filter relative my-1 w-[90%]">
                <Search class="pointer-events-none absolute top-1/2 left-2 size-3 -translate-y-1/2 text-muted-foreground" aria-hidden="true" />
                <Input
                  :value="chTableFilterByDb[chSub.keyOf(conn.id, db)]"
                  @input="chTableFilterByDb[chSub.keyOf(conn.id, db)] = ($event.target as HTMLInputElement).value"
                  class="h-6.5 pl-7 text-xs"
                  type="search"
                  data-test="ch-table-filter"
                  placeholder="模糊搜索表…"
                  autocapitalize="off"
                  autocorrect="off"
                  autocomplete="off"
                  spellcheck="false"
                />
              </div>
              <div
                v-if="chSub.isLoading(conn.id, db)"
                class="leaf muted px-1.5 py-1 text-muted-foreground"
                data-test="ch-tables-loading"
              >加载中…</div>
              <template v-else>
                <div
                  v-for="t in chSub.filtered(conn.id, db)"
                  :key="t.name"
                  class="leaf ch-table flex h-7 cursor-pointer items-center gap-1.5 rounded-md px-1.5 pl-6 transition-colors hover:bg-accent"
                  data-test="ch-table-node"
                  :title="`${t.name}(${t.engine})`"
                  @dblclick="emit('open-ch-table', conn.id, db, t.name)"
                >
                  <Table2 class="size-3.5 shrink-0 text-muted-foreground" aria-hidden="true" />
                  <span class="leaf-name min-w-0 flex-1 truncate" data-test="ch-table-name">{{ t.name }}</span>
                  <span class="leaf-badge shrink-0 rounded-full bg-primary/10 px-1.5 font-mono text-[10px] leading-4 text-primary" data-test="ch-table-engine">{{ t.engine }}</span>
                  <span class="leaf-badge shrink-0 rounded-full bg-muted px-1.5 font-mono text-[10px] leading-4 text-muted-foreground" data-test="ch-table-rows">{{ formatBytes(t.total_rows) }}</span>
                </div>
                <div
                  v-if="chSub.filtered(conn.id, db).length === 0"
                  class="leaf muted px-1.5 py-1 pl-6 text-muted-foreground"
                  data-test="ch-table-empty"
                >{{ chSub.isEmpty(conn.id, db) ? '（无表）' : '无匹配表' }}</div>
              </template>
            </template>
          </template>
          <div v-if="(chSub.dbsOf(conn.id) ?? []).length === 0" class="leaf muted px-1.5 py-1 text-muted-foreground" data-test="ch-db-empty">（无数据库）</div>
        </template>
      </div>
      <div v-else-if="isExpanded(conn.id) && (conn.type === 'mysql' || conn.type === 'tidb')" class="ml-4 border-l border-border pl-2">
        <div v-if="loadingByConn[conn.id]" class="px-1 py-1 text-muted-foreground" data-test="tree-loading">加载中…</div>
        <div v-else-if="errorByConn[conn.id]" class="px-1 py-1 text-destructive" data-test="tree-error">{{ errorByConn[conn.id] }}</div>
        <template v-else>
          <template v-for="db in mysqlSub.dbsOf(conn.id) ?? []" :key="db">
            <div class="leaf flex h-7 cursor-pointer items-center gap-1 rounded-md px-1.5 transition-colors hover:bg-accent" data-test="mysql-db-node" :title="`数据库 ${db}`" @click="mysqlSub.toggleDb(conn.id, db)">
              <span class="flex size-4 shrink-0 items-center justify-center text-muted-foreground transition-transform" :class="mysqlSub.isDbExpanded(conn.id, db) ? 'open rotate-90' : ''" data-test="mysql-db-caret">
                <ChevronRight class="size-3.5" aria-hidden="true" />
              </span>
              <Database class="size-3.5 shrink-0 text-muted-foreground" aria-hidden="true" />
              <span class="leaf-name min-w-0 flex-1 truncate" data-test="mysql-db-name">{{ db }}</span>
              <span
                v-if="mysqlSub.isLoaded(conn.id, db)"
                class="leaf-badge shrink-0 rounded-full bg-muted px-1.5 font-mono text-[10px] leading-4 text-muted-foreground"
                data-test="mysql-db-count"
              >{{ mysqlSub.countLabel(conn.id, db) }}</span>
            </div>
            <template v-if="mysqlSub.isDbExpanded(conn.id, db)">
              <div class="ch-table-filter relative my-1 w-[90%]">
                <Search class="pointer-events-none absolute top-1/2 left-2 size-3 -translate-y-1/2 text-muted-foreground" aria-hidden="true" />
                <Input
                  :value="mysqlTableFilterByDb[mysqlSub.keyOf(conn.id, db)]"
                  @input="mysqlTableFilterByDb[mysqlSub.keyOf(conn.id, db)] = ($event.target as HTMLInputElement).value"
                  class="h-6.5 pl-7 text-xs"
                  type="search"
                  data-test="mysql-table-filter"
                  placeholder="模糊搜索表…"
                  autocapitalize="off"
                  autocorrect="off"
                  autocomplete="off"
                  spellcheck="false"
                />
              </div>
              <div
                v-if="mysqlSub.isLoading(conn.id, db)"
                class="leaf muted px-1.5 py-1 text-muted-foreground"
                data-test="mysql-tables-loading"
              >加载中…</div>
              <template v-else>
                <div
                  v-for="t in mysqlSub.filtered(conn.id, db)"
                  :key="t.name"
                  class="leaf ch-table group flex h-7 cursor-pointer items-center gap-1.5 rounded-md px-1.5 pl-6 transition-colors hover:bg-accent"
                  data-test="mysql-table-node"
                  :title="`${t.name}(${t.engine})`"
                  @dblclick="emit('open-mysql-table', conn.id, db, t.name)"
                  @contextmenu.prevent.stop="openMysqlTableMenu($event, conn.id, db, t.name)"
                >
                  <Table2 class="size-3.5 shrink-0 text-muted-foreground" aria-hidden="true" />
                  <span class="leaf-name min-w-0 flex-1 truncate" data-test="mysql-table-name">{{ t.name }}</span>
                  <span class="leaf-badge shrink-0 rounded-full bg-primary/10 px-1.5 font-mono text-[10px] leading-4 text-primary" data-test="mysql-table-engine">{{ t.engine }}</span>
                  <span v-if="t.table_rows !== null" class="leaf-badge shrink-0 rounded-full bg-muted px-1.5 font-mono text-[10px] leading-4 text-muted-foreground" data-test="mysql-table-rows">{{ formatBytes(t.table_rows) }}</span>
                </div>
                <div
                  v-if="mysqlSub.filtered(conn.id, db).length === 0"
                  class="leaf muted px-1.5 py-1 pl-6 text-muted-foreground"
                  data-test="mysql-table-empty"
                >{{ mysqlSub.isEmpty(conn.id, db) ? '（无表）' : '无匹配表' }}</div>
              </template>
            </template>
          </template>
          <div v-if="(mysqlSub.dbsOf(conn.id) ?? []).length === 0" class="leaf muted px-1.5 py-1 text-muted-foreground" data-test="mysql-db-empty">（无数据库）</div>
        </template>
      </div>
      <div v-else-if="isExpanded(conn.id) && conn.type === 'hive'" class="ml-4 border-l border-border pl-2">
        <div v-if="loadingByConn[conn.id]" class="px-1 py-1 text-muted-foreground" data-test="tree-loading">加载中…</div>
        <div v-else-if="errorByConn[conn.id]" class="px-1 py-1 text-destructive" data-test="tree-error">{{ errorByConn[conn.id] }}</div>
        <template v-else>
          <template v-for="db in hiveSub.dbsOf(conn.id) ?? []" :key="db">
            <div class="leaf flex h-7 cursor-pointer items-center gap-1 rounded-md px-1.5 transition-colors hover:bg-accent" data-test="hive-db-node" :title="`数据库 ${db}`" @click="hiveSub.toggleDb(conn.id, db)">
              <span class="flex size-4 shrink-0 items-center justify-center text-muted-foreground transition-transform" :class="hiveSub.isDbExpanded(conn.id, db) ? 'open rotate-90' : ''" data-test="hive-db-caret">
                <ChevronRight class="size-3.5" aria-hidden="true" />
              </span>
              <Database class="size-3.5 shrink-0 text-muted-foreground" aria-hidden="true" />
              <span class="leaf-name min-w-0 flex-1 truncate" data-test="hive-db-name">{{ db }}</span>
              <span
                v-if="hiveSub.isLoaded(conn.id, db)"
                class="leaf-badge shrink-0 rounded-full bg-muted px-1.5 font-mono text-[10px] leading-4 text-muted-foreground"
                data-test="hive-db-count"
              >{{ hiveSub.countLabel(conn.id, db) }}</span>
            </div>
            <template v-if="hiveSub.isDbExpanded(conn.id, db)">
              <div class="ch-table-filter relative my-1 w-[90%]">
                <Search class="pointer-events-none absolute top-1/2 left-2 size-3 -translate-y-1/2 text-muted-foreground" aria-hidden="true" />
                <Input
                  :value="hiveTableFilterByDb[hiveSub.keyOf(conn.id, db)]"
                  @input="hiveTableFilterByDb[hiveSub.keyOf(conn.id, db)] = ($event.target as HTMLInputElement).value"
                  class="h-6.5 pl-7 text-xs"
                  type="search"
                  data-test="hive-table-filter"
                  placeholder="模糊搜索表…"
                  autocapitalize="off"
                  autocorrect="off"
                  autocomplete="off"
                  spellcheck="false"
                />
              </div>
              <div
                v-if="hiveSub.isLoading(conn.id, db)"
                class="leaf muted px-1.5 py-1 text-muted-foreground"
                data-test="hive-tables-loading"
              >加载中…</div>
              <template v-else>
                <div
                  v-for="t in hiveSub.filtered(conn.id, db)"
                  :key="t.name"
                  class="leaf ch-table flex h-7 cursor-pointer items-center gap-1.5 rounded-md px-1.5 pl-6 transition-colors hover:bg-accent"
                  data-test="hive-table-node"
                  :title="t.name"
                  @dblclick="emit('open-hive-table', conn.id, db, t.name)"
                  @contextmenu.prevent.stop="openHiveTableMenu($event, conn.id, db, t.name)"
                >
                  <Table2 class="size-3.5 shrink-0 text-muted-foreground" aria-hidden="true" />
                  <span class="leaf-name min-w-0 flex-1 truncate" data-test="hive-table-name">{{ t.name }}</span>
                </div>
                <div
                  v-if="hiveSub.filtered(conn.id, db).length === 0"
                  class="leaf muted px-1.5 py-1 pl-6 text-muted-foreground"
                  data-test="hive-table-empty"
                >{{ hiveSub.isEmpty(conn.id, db) ? '（无表）' : '无匹配表' }}</div>
              </template>
            </template>
          </template>
          <div v-if="(hiveSub.dbsOf(conn.id) ?? []).length === 0" class="leaf muted px-1.5 py-1 text-muted-foreground" data-test="hive-db-empty">（无数据库）</div>
        </template>
      </div>
      <div v-else-if="isExpanded(conn.id) && conn.type === 'postgres'" class="ml-4 border-l border-border pl-2">
        <div v-if="loadingByConn[conn.id]" class="px-1 py-1 text-muted-foreground" data-test="tree-loading">加载中…</div>
        <div v-else-if="errorByConn[conn.id]" class="px-1 py-1 text-destructive" data-test="tree-error">{{ errorByConn[conn.id] }}</div>
        <template v-else>
          <template v-for="db in pgDBs[conn.id] ?? []" :key="db">
            <div class="leaf flex h-7 cursor-pointer items-center gap-1 rounded-md px-1.5 transition-colors hover:bg-accent" data-test="pg-db-node" :title="`数据库 ${db}`" @click="togglePgDb(conn.id, db)">
              <span class="flex size-4 shrink-0 items-center justify-center text-muted-foreground transition-transform" :class="isPgDbExpanded(conn.id, db) ? 'open rotate-90' : ''" data-test="pg-db-caret">
                <ChevronRight class="size-3.5" aria-hidden="true" />
              </span>
              <Database class="size-3.5 shrink-0 text-muted-foreground" aria-hidden="true" />
              <span class="leaf-name min-w-0 flex-1 truncate" data-test="pg-db-name">{{ db }}</span>
              <span
                v-if="pgSchemasByDb[pgDbKey(conn.id, db)]"
                class="leaf-badge shrink-0 rounded-full bg-muted px-1.5 font-mono text-[10px] leading-4 text-muted-foreground"
                data-test="pg-db-count"
              >{{ (pgSchemasByDb[pgDbKey(conn.id, db)] ?? []).length }}</span>
            </div>
            <template v-if="isPgDbExpanded(conn.id, db)">
              <div
                v-if="pgSchemaLoadingByDb[pgDbKey(conn.id, db)]"
                class="leaf muted px-1.5 py-1 text-muted-foreground"
                data-test="pg-schemas-loading"
              >加载中…</div>
              <template v-else>
                <template v-for="schema in pgSchemasByDb[pgDbKey(conn.id, db)] ?? []" :key="schema">
                  <div class="leaf pg-schema-node-inner flex h-7 cursor-pointer items-center gap-1 rounded-md px-1.5 pl-6 transition-colors hover:bg-accent" data-test="pg-schema-node" :title="`Schema ${schema}`" @click="togglePgSchema(conn.id, db, schema)">
                    <span class="flex size-4 shrink-0 items-center justify-center text-muted-foreground transition-transform" :class="isPgSchemaExpanded(conn.id, db, schema) ? 'open rotate-90' : ''" data-test="pg-schema-caret">
                      <ChevronRight class="size-3.5" aria-hidden="true" />
                    </span>
                    <Folder class="size-3.5 shrink-0 text-muted-foreground" aria-hidden="true" />
                    <span class="leaf-name min-w-0 flex-1 truncate" data-test="pg-schema-name">{{ schema }}</span>
                    <span
                      v-if="pgRelationsBySchema[pgSchemaKey(conn.id, db, schema)]"
                      class="leaf-badge shrink-0 rounded-full bg-muted px-1.5 font-mono text-[10px] leading-4 text-muted-foreground"
                      data-test="pg-schema-count"
                    >{{ pgSchemaCountLabel(conn.id, db, schema) }}</span>
                  </div>
                  <template v-if="isPgSchemaExpanded(conn.id, db, schema)">
                    <div class="ch-table-filter relative my-1 ml-6 w-[85%]">
                      <Search class="pointer-events-none absolute top-1/2 left-2 size-3 -translate-y-1/2 text-muted-foreground" aria-hidden="true" />
                      <Input
                        :value="pgRelationFilterBySchema[pgSchemaKey(conn.id, db, schema)]"
                        @input="pgRelationFilterBySchema[pgSchemaKey(conn.id, db, schema)] = ($event.target as HTMLInputElement).value"
                        class="h-6.5 pl-7 text-xs"
                        type="search"
                        data-test="pg-relation-filter"
                        placeholder="模糊搜索表…"
                        autocapitalize="off"
                        autocorrect="off"
                        autocomplete="off"
                        spellcheck="false"
                      />
                    </div>
                    <div
                      v-if="pgRelationLoadingBySchema[pgSchemaKey(conn.id, db, schema)]"
                      class="leaf muted px-1.5 py-1 pl-10 text-muted-foreground"
                      data-test="pg-relations-loading"
                    >加载中…</div>
                    <template v-else>
                      <div
                        v-for="r in filteredPgRelations(conn.id, db, schema)"
                        :key="`${r.relation_type}:${r.relation}`"
                        class="leaf ch-table flex h-7 cursor-pointer items-center gap-1.5 rounded-md px-1.5 pl-10 transition-colors hover:bg-accent"
                        data-test="pg-relation-node"
                        role="button"
                        tabindex="0"
                        :title="`${r.relation}(${r.relation_type})`"
                        @dblclick="openPgRelation(conn.id, db, r)"
                        @keydown.enter.prevent="openPgRelation(conn.id, db, r)"
                        @keydown.space.prevent="openPgRelation(conn.id, db, r)"
                      >
                        <Table2 class="size-3.5 shrink-0 text-muted-foreground" aria-hidden="true" />
                        <span class="leaf-name min-w-0 flex-1 truncate" data-test="pg-relation-name">{{ r.relation }}</span>
                        <span class="leaf-badge shrink-0 rounded-full bg-primary/10 px-1.5 font-mono text-[10px] leading-4 text-primary" data-test="pg-relation-type">{{ r.relation_type }}</span>
                      </div>
                      <div
                        v-if="filteredPgRelations(conn.id, db, schema).length === 0"
                        class="leaf muted px-1.5 py-1 pl-10 text-muted-foreground"
                        data-test="pg-relation-empty"
                      >{{ (pgRelationsBySchema[pgSchemaKey(conn.id, db, schema)] ?? []).length === 0 ? '（无表）' : '无匹配表' }}</div>
                    </template>
                  </template>
                </template>
                <div
                  v-if="(pgSchemasByDb[pgDbKey(conn.id, db)] ?? []).length === 0"
                  class="leaf muted px-1.5 py-1 pl-6 text-muted-foreground"
                  data-test="pg-schema-empty"
                >（无 schema）</div>
              </template>
            </template>
          </template>
          <div v-if="(pgDBs[conn.id] ?? []).length === 0" class="leaf muted px-1.5 py-1 text-muted-foreground" data-test="pg-db-empty">（无数据库）</div>
        </template>
      </div>
      <div v-else-if="isExpanded(conn.id) && conn.type === 'es'" class="ml-4 border-l border-border pl-2">
        <div v-if="loadingByConn[conn.id]" class="px-1 py-1 text-muted-foreground" data-test="tree-loading">加载中…</div>
        <div v-else-if="errorByConn[conn.id]" class="px-1 py-1 text-destructive" data-test="tree-error">{{ errorByConn[conn.id] }}</div>
        <template v-else>
          <!-- 分段按钮:索引 / 索引模板,互斥切换,同一时刻只渲染一个列表区。 -->
          <div class="segmented my-1.5 mb-2 flex gap-0.5 rounded-lg border border-border bg-muted p-0.5" data-test="section-tabs" role="tablist">
            <button
              type="button"
              class="segmented-btn flex min-w-0 flex-1 items-center justify-center gap-1 rounded-md px-2 py-1 text-xs font-medium text-muted-foreground transition-colors hover:text-foreground"
              :class="esSectionOf(conn.id) === 'es-indices' ? 'active bg-background text-foreground shadow-sm' : ''"
              data-test="section-tab-es-indices"
              role="tab"
              :aria-selected="esSectionOf(conn.id) === 'es-indices'"
              @click="switchEsSection(conn.id, 'es-indices')"
            >
              <FileText class="size-3.5 shrink-0" aria-hidden="true" />
              <span class="segmented-label min-w-0 flex-1 truncate text-left">索引</span>
              <span class="segmented-count shrink-0 rounded-full bg-background/80 px-1.5 text-[11px] leading-4 text-muted-foreground">{{ (esIndices[conn.id] ?? []).length }}</span>
            </button>
            <button
              type="button"
              class="segmented-btn flex min-w-0 flex-1 items-center justify-center gap-1 rounded-md px-2 py-1 text-xs font-medium text-muted-foreground transition-colors hover:text-foreground"
              :class="esSectionOf(conn.id) === 'es-templates' ? 'active bg-background text-foreground shadow-sm' : ''"
              data-test="section-tab-es-templates"
              role="tab"
              :aria-selected="esSectionOf(conn.id) === 'es-templates'"
              @click="switchEsSection(conn.id, 'es-templates')"
            >
              <FileCog class="size-3.5 shrink-0" aria-hidden="true" />
              <span class="segmented-label min-w-0 flex-1 truncate text-left">索引模板</span>
              <span v-if="esTemplatesByConn[conn.id]" class="segmented-count shrink-0 rounded-full bg-background/80 px-1.5 text-[11px] leading-4 text-muted-foreground">{{ esTemplatesByConn[conn.id]?.length }}</span>
            </button>
          </div>

          <template v-if="esSectionOf(conn.id) === 'es-indices'">
            <!-- 分区标题行:照抄 kafka group-label creatable,点击展开内联新建表单。 -->
            <button
              class="group-label clickable mt-2 mb-0.5 flex w-full cursor-pointer items-center justify-between rounded-md px-1.5 py-1 text-left text-[11px] uppercase tracking-wide text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
              type="button"
              data-test="es-indices-header"
              title="新建索引"
              @click="toggleEsCreate(conn)"
            >
              <span class="group-title flex min-w-0 flex-1 items-center gap-1">
                <FileText class="size-3.5 shrink-0" aria-hidden="true" />
                索引
              </span>
              <span class="group-add flex size-5 items-center justify-center rounded-md text-primary transition-colors" aria-hidden="true">
                <Plus class="size-3.5" />
              </span>
            </button>
            <div v-if="isEsCreatingFor(conn.id)" class="create-form my-1 flex flex-col gap-1.5 rounded-lg border border-border bg-muted/50 p-2" data-test="es-create-form">
              <Input
                :value="esCreateForm.name"
                @input="esCreateForm.name = ($event.target as HTMLInputElement).value"
                class="h-7 text-xs"
                type="text"
                data-test="es-create-index"
                placeholder="索引名称"
                autocapitalize="off"
                autocorrect="off"
                autocomplete="off"
                spellcheck="false"
              />
              <div class="flex gap-2">
                <Label class="flex-1 flex-col items-stretch gap-1 text-xs font-normal text-muted-foreground">
                  分片
                  <Input :value="esCreateForm.shards" @input="esCreateForm.shards = Number(($event.target as HTMLInputElement).value)" class="h-7 text-xs" type="number" min="1" data-test="es-create-shards" />
                </Label>
                <Label class="flex-1 flex-col items-stretch gap-1 text-xs font-normal text-muted-foreground">
                  副本
                  <Input :value="esCreateForm.replicas" @input="esCreateForm.replicas = Number(($event.target as HTMLInputElement).value)" class="h-7 text-xs" type="number" min="1" data-test="es-create-replicas" />
                </Label>
              </div>
              <div v-if="esCreateError" class="create-error text-xs text-destructive" data-test="es-create-error">{{ esCreateError }}</div>
              <div class="create-actions flex gap-2">
                <Button
                  size="sm"
                  type="button"
                  data-test="btn-es-create-submit"
                  :disabled="esCreating || esCreateForm.name.trim().length === 0"
                  @click="submitEsCreate(conn)"
                >
                  {{ esCreating ? '创建中…' : '创建' }}
                </Button>
                <Button size="sm" variant="outline" type="button" data-test="btn-es-create-cancel" @click="closeEsCreate">取消</Button>
              </div>
            </div>
            <!-- ES 无库层级:连接展开直接列索引(系统索引由后端过滤),双击打开索引浏览。 -->
            <div class="ch-table-filter my-1">
              <div class="es-filter-row flex items-center gap-1.5">
                <div class="relative min-w-0 flex-1">
                  <Search class="pointer-events-none absolute top-1/2 left-2 size-3 -translate-y-1/2 text-muted-foreground" aria-hidden="true" />
                  <Input
                    :value="esIndexFilterByConn[conn.id]"
                    @input="esIndexFilterByConn[conn.id] = ($event.target as HTMLInputElement).value"
                    class="h-6.5 pl-7 text-xs"
                    type="search"
                    data-test="es-index-filter"
                    placeholder="筛选索引"
                    autocapitalize="off"
                    autocorrect="off"
                    autocomplete="off"
                    spellcheck="false"
                  />
                </div>
                <span class="leaf-badge shrink-0 rounded-full bg-muted px-1.5 font-mono text-[10px] leading-4 text-muted-foreground" data-test="es-index-count">{{ esIndexCountLabel(conn.id) }}</span>
              </div>
            </div>
            <div
              v-for="esIdx in filteredEsIndices(conn.id)"
              :key="esIdx.name"
              class="leaf flex h-7 cursor-pointer items-center gap-1.5 rounded-md px-1.5 transition-colors hover:bg-accent"
              data-test="es-index-node"
              :title="`${esIdx.name}(${formatBytes(esIdx.store_size_bytes)})`"
              @dblclick="emit('open-es-index', conn.id, esIdx.name)"
              @contextmenu.prevent.stop="openEsIndexMenu($event, conn.id, esIdx.name)"
            >
              <FileText class="size-3.5 shrink-0 text-muted-foreground" aria-hidden="true" />
              <span class="leaf-name min-w-0 flex-1 truncate" data-test="es-index-name">{{ esIdx.name }}</span>
              <span
                class="leaf-badge shrink-0 rounded-full bg-muted px-1.5 font-mono text-[10px] leading-4 text-muted-foreground"
                data-test="es-index-docs"
                :title="`约 ${esIdx.docs_count.toLocaleString()} 条文档`"
              >{{ formatCount(esIdx.docs_count) }}</span>
            </div>
            <div v-if="filteredEsIndices(conn.id).length === 0" class="leaf muted px-1.5 py-1 text-muted-foreground" data-test="es-index-empty">
              {{ (esIndices[conn.id] ?? []).length === 0 ? '（无索引）' : '无匹配索引' }}
            </div>
          </template>

          <template v-else>
            <!-- 分区标题行:整行可点击,打开模板面板新建态。 -->
            <button
              class="group-label clickable mt-2 mb-0.5 flex w-full cursor-pointer items-center justify-between rounded-md px-1.5 py-1 text-left text-[11px] uppercase tracking-wide text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
              type="button"
              data-test="es-templates-header"
              title="新建索引模板"
              @click="emit('open-es-template-create', conn.id)"
            >
              <span class="group-title flex min-w-0 flex-1 items-center gap-1">
                <FileCog class="size-3.5 shrink-0" aria-hidden="true" />
                索引模板
              </span>
              <span class="group-add flex size-5 items-center justify-center rounded-md text-primary transition-colors" aria-hidden="true">
                <Plus class="size-3.5" />
              </span>
            </button>
            <div v-if="esTemplatesLoadingByConn[conn.id]" class="px-1 py-1 text-muted-foreground" data-test="es-templates-loading">加载中…</div>
            <div v-else-if="esTemplatesErrorByConn[conn.id]" class="px-1 py-1 text-destructive" data-test="es-templates-error">{{ esTemplatesErrorByConn[conn.id] }}</div>
            <template v-else>
              <div
                v-for="tpl in esTemplatesByConn[conn.id] ?? []"
                :key="tpl.name"
                class="leaf flex h-7 cursor-pointer items-center gap-1.5 rounded-md px-1.5 transition-colors hover:bg-accent"
                :data-test="`es-template-item-${tpl.name}`"
                :title="`${tpl.name}(order ${tpl.order})`"
                @click="emit('open-es-template', conn.id, tpl.name)"
                @contextmenu.prevent.stop="openEsTemplateMenu($event, conn.id, tpl.name)"
              >
                <FileCog class="size-3.5 shrink-0 text-muted-foreground" aria-hidden="true" />
                <span class="leaf-name min-w-0 flex-1 truncate">{{ tpl.name }}</span>
                <span class="leaf-badge shrink-0 rounded-full bg-muted px-1.5 font-mono text-[10px] leading-4 text-muted-foreground" data-test="es-template-order" title="模板合并顺序(order 越大优先级越高)">{{ tpl.order }}</span>
              </div>
              <div v-if="(esTemplatesByConn[conn.id] ?? []).length === 0" class="leaf muted px-1.5 py-1 text-muted-foreground" data-test="es-templates-empty">（无模板）</div>
            </template>
          </template>
        </template>
      </div>
      <div v-else-if="isExpanded(conn.id)" class="ml-4 border-l border-border pl-2">
        <div class="leaf muted px-1.5 py-1 text-muted-foreground" data-test="type-unsupported">{{ typeMeta(conn).label }} 类型暂未支持</div>
      </div>
    </div>
    <TopicDetailDrawer
      :show="!!detailMeta"
      :connection-id="detailMeta?.connId ?? ''"
      :topic="detailMeta?.topic ?? ''"
      :edit="detailMeta?.edit ?? false"
      @close="detailMeta = null"
    />
    <ContextMenu
      :show="!!ctxMenu"
      :x="ctxMenu?.x ?? 0"
      :y="ctxMenu?.y ?? 0"
      :items="ctxItems"
      @select="onCtxSelect"
      @close="closeCtxMenu"
    />
    <PromptDialog
      :show="!!expandMeta"
      title="扩充分区"
      :label="`Topic「${expandMeta?.topic ?? ''}」目标分区数`"
      :value="expandMeta?.target ?? 1"
      :hint="expandError ?? 'Kafka 仅支持扩容分区，不能缩减；新分区的数据不会自动重平衡。'"
      @confirm="submitExpand"
      @cancel="expandMeta = null"
    />
    <ResetOffsetDialog
      :show="!!resetMeta"
      :connection-id="resetMeta?.connId ?? ''"
      :topic="resetMeta?.topic ?? null"
      :group="resetMeta?.group ?? null"
      @close="resetMeta = null"
    />
    <MysqlTableStructure
      :show="!!structureMeta"
      :connection-id="structureMeta?.connId ?? ''"
      :database="structureMeta?.db ?? ''"
      :table="structureMeta?.table ?? ''"
      @close="structureMeta = null"
    />
    <!-- Hive 编辑表字段弹窗(与 MySQL 同型,按 Hive 方言裁剪)。 -->
    <HiveTableStructure
      :show="!!hiveStructureMeta"
      :connection-id="hiveStructureMeta?.connId ?? ''"
      :database="hiveStructureMeta?.db ?? ''"
      :table="hiveStructureMeta?.table ?? ''"
      @close="hiveStructureMeta = null"
    />
    <MysqlExportDialog
      v-if="exportMeta"
      :show="true"
      :connection-id="exportMeta.connId"
      :database="exportMeta.db"
      :table="exportMeta.table"
      @close="exportMeta = null"
    />
    <ConfirmDialog
      :show="!!confirm"
      :message="confirmMessage"
      :confirm-text="confirmText"
      @confirm="executeDelete"
      @cancel="confirm = null"
    />
    <!-- 修改索引设置弹窗:与 ConfirmDialog 同样 Teleport 到 body(侧栏的
         backdrop-filter 会把 position:fixed 限制在侧栏盒内)。 -->
    <Teleport to="body">
      <div v-if="esSettingsMeta" class="es-settings-backdrop fixed inset-0 z-[100] flex items-center justify-center bg-black/40 backdrop-blur-[2px]" data-test="es-settings-dialog" @click.self="esSettingsMeta = null">
        <div class="es-settings-modal flex w-80 max-w-[calc(100vw-48px)] flex-col gap-2.5 rounded-xl border border-border bg-background p-4 shadow-lg">
          <div class="es-settings-title text-sm font-semibold">修改索引设置</div>
          <div class="es-settings-index truncate text-xs text-muted-foreground" :title="esSettingsMeta.index">{{ esSettingsMeta.index }}</div>
          <Label class="create-field flex-col items-start gap-1 text-xs font-normal text-muted-foreground">
            副本数
            <Input
              :value="esSettingsReplicas"
              @input="esSettingsReplicas = Number(($event.target as HTMLInputElement).value)"
              class="h-7 text-xs"
              type="number"
              min="0"
              step="1"
              data-test="es-settings-replicas"
            />
          </Label>
          <div v-if="esSettingsError" class="create-error text-xs text-destructive" data-test="es-settings-error">{{ esSettingsError }}</div>
          <div class="es-settings-actions flex justify-end gap-2">
            <Button variant="outline" size="sm" type="button" data-test="es-settings-cancel" @click="esSettingsMeta = null">取消</Button>
            <Button
              size="sm"
              type="button"
              data-test="es-settings-submit"
              :disabled="esSettingsSaving"
              @click="submitEsSettings"
            >{{ esSettingsSaving ? '保存中…' : '保存' }}</Button>
          </div>
        </div>
      </div>
    </Teleport>
  </div>
</template>

<style scoped>
/*
 * UI 对齐 DBX 后本组件仅保留 Tailwind 难以表达的少量规则:
 * 1) 状态点语义色:连接状态 → success/destructive/muted 的映射与脉冲动画;
 * 2) 数据源品牌色徽标:--type-* 局部色表(styles.css 退役后收编为组件内
 *    const 色表,亮暗两套),以 .type-badge-<type>/.conn-type-<type> class 关联;
 * 3) 拖拽插入指示线:压在目标行上/下边缘的伪元素横线。
 * 其余样式全部改为模板内的 Tailwind 工具类。
 */
/* 状态点:默认未连接灰;connected/connecting=success,connecting 脉冲;error=destructive。 */
.conn-status { background: color-mix(in srgb, var(--muted-foreground) 45%, transparent); }
.conn-status-connected, .conn-status-connecting { background: var(--success); }
.conn-status-connecting { animation: conn-pulse 1.1s ease-in-out infinite; }
.conn-status-error { background: var(--destructive); }
.conn-status-disconnected, .conn-status-unknown { background: color-mix(in srgb, var(--muted-foreground) 45%, transparent); }
@keyframes conn-pulse { 0%, 100% { opacity: 1; } 50% { opacity: 0.3; } }

/* 数据源品牌色 const 表(原全局 styles.css --type-* 变量收编):
   --type-<t> 为字母徽标实底主色,-contrast 为徽标字色,-soft 为类型文字
   徽标的柔和底色。pink 主题浅底与浅色明度接近,沿用浅色套。 */
.conn-tree {
  --type-kafka: #e8710a;      --type-kafka-contrast: #ffffff;   --type-kafka-soft: rgba(232, 113, 10, 0.12);
  --type-mysql: #0868a8;      --type-mysql-contrast: #ffffff;   --type-mysql-soft: rgba(8, 104, 168, 0.12);
  --type-redis: #d82c20;      --type-redis-contrast: #ffffff;   --type-redis-soft: rgba(216, 44, 32, 0.12);
  --type-es: #ddb100;         --type-es-contrast: #3d3000;      --type-es-soft: rgba(221, 177, 0, 0.16);
  --type-tidb: #d4111e;       --type-tidb-contrast: #ffffff;    --type-tidb-soft: rgba(212, 17, 30, 0.12);
  --type-clickhouse: #d9a400; --type-clickhouse-contrast: #3d3000; --type-clickhouse-soft: rgba(217, 164, 0, 0.16);
  --type-postgres: #2c8c99;   --type-postgres-contrast: #ffffff; --type-postgres-soft: rgba(44, 140, 153, 0.13);
  --type-hive: #d99a13;       --type-hive-contrast: #3a2a00;    --type-hive-soft: rgba(217, 154, 19, 0.16);
}
/* 深色:主色整体提亮一档、字色换深色保持对比,柔和底透明度略高以在深底可辨。 */
.dark .conn-tree {
  --type-kafka: #f59e4c;      --type-kafka-contrast: #3a1c00;   --type-kafka-soft: rgba(245, 158, 76, 0.2);
  --type-mysql: #58b6f0;      --type-mysql-contrast: #04263c;   --type-mysql-soft: rgba(88, 182, 240, 0.2);
  --type-redis: #f16a5f;      --type-redis-contrast: #3a0603;   --type-redis-soft: rgba(241, 106, 95, 0.2);
  --type-es: #f2d270;         --type-es-contrast: #4a3800;      --type-es-soft: rgba(242, 210, 112, 0.22);
  --type-tidb: #f47c85;       --type-tidb-contrast: #3d0308;    --type-tidb-soft: rgba(244, 124, 133, 0.2);
  --type-clickhouse: #f5cd47; --type-clickhouse-contrast: #3d3000; --type-clickhouse-soft: rgba(245, 205, 71, 0.22);
  --type-postgres: #6cc7d2;   --type-postgres-contrast: #062e33; --type-postgres-soft: rgba(108, 199, 210, 0.2);
  --type-hive: #edc25a;       --type-hive-contrast: #3a2a00;    --type-hive-soft: rgba(237, 194, 90, 0.22);
}

/* 数据源品牌色:字母徽标实底主色 + 类型文字徽标柔和底色(变量含 dark 覆盖)。 */
.type-badge-kafka { background: var(--type-kafka); color: var(--type-kafka-contrast); }
.type-badge-mysql { background: var(--type-mysql); color: var(--type-mysql-contrast); }
.type-badge-redis { background: var(--type-redis); color: var(--type-redis-contrast); }
.type-badge-es { background: var(--type-es); color: var(--type-es-contrast); }
.type-badge-tidb { background: var(--type-tidb); color: var(--type-tidb-contrast); }
.type-badge-clickhouse { background: var(--type-clickhouse); color: var(--type-clickhouse-contrast); }
.type-badge-postgres { background: var(--type-postgres); color: var(--type-postgres-contrast); }
.type-badge-hive { background: var(--type-hive); color: var(--type-hive-contrast); }
.conn-type-kafka { color: var(--type-kafka); background: var(--type-kafka-soft); }
.conn-type-mysql { color: var(--type-mysql); background: var(--type-mysql-soft); }
.conn-type-es { color: var(--type-es); background: var(--type-es-soft); }
.conn-type-redis { color: var(--type-redis); background: var(--type-redis-soft); }
.conn-type-clickhouse { color: var(--type-clickhouse); background: var(--type-clickhouse-soft); }
.conn-type-tidb { color: var(--type-tidb); background: var(--type-tidb-soft); }
.conn-type-postgres { color: var(--type-postgres); background: var(--type-postgres-soft); }
.conn-type-hive { color: var(--type-hive); background: var(--type-hive-soft); }

/* 拖拽插入位置指示线:accent 色 2px 横线压在目标行的上/下边缘(伪元素)。 */
.conn-row.drop-before, .conn-row.drop-after { position: relative; }
.conn-row.drop-before::before,
.conn-row.drop-after::after {
  content: '';
  position: absolute;
  left: 4px;
  right: 4px;
  height: 2px;
  border-radius: 1px;
  background: var(--primary);
  pointer-events: none;
}
.conn-row.drop-before::before { top: -2px; }
.conn-row.drop-after::after { bottom: -2px; }

/* 分组标题 hover 显现的可点击态(动态 component 上无法用 group-hover 表达)。 */
.group-label.clickable { cursor: pointer; transition: background 0.12s ease, color 0.12s ease; }
.group-label.clickable:hover { background: var(--primary); color: var(--accent-foreground); }
</style>
