// Thin wrapper around the generated Wails bindings so stores/components can be
// tested by swapping the Api implementation via setApi().
import * as App from '../../wailsjs/go/backend/App'
import type {
  Connection,
  KafkaConfig,
  Topic,
  TopicDetail,
  ClusterHealth,
  ConsumerGroup,
  GroupDetail,
  Message,
  ConsumeRequest,
  ResetOffsetRequest,
  PreviewOffsetRequest,
  PreviewOffsetMap,
  ProduceRequest,
  BatchProduceRequest,
  ProduceResult,
  CreateTopicRequest,
  UpdateConnectionRequest,
  DeleteTopicRequest,
  DeleteTopicsRequest,
  AlterTopicConfigRequest,
  AlterTopicPartitionsRequest,
  TopicMessageCounts,
  TopicDeleteResult,
  DeleteConsumerGroupRequest,
  ActiveMembersRequest,
  ActiveProducer,
  ActiveConsumer,
  AuditEntry,
  SaveTextFileRequest,
  UpdateCheckRequest,
  UpdateCheckResult,
  UpdateDownloadRequest,
  UpdateProgressInfo,
  ApplyUpdateRequest,
  RedisConfigShape,
  RedisDBInfo,
  RedisKeyInfo,
  RedisValue,
  RedisServerInfo,
  RedisScanRequest,
  RedisScanResult,
  RedisKeyRequest,
  RedisDeleteKeysRequest,
  RedisSetStringRequest,
  RedisFlushRequest,
  RedisHashSetFieldRequest,
  SavedQuery,
  ListSavedQueriesRequest,
  SaveSavedQueryRequest,
  UpdateSavedQueryRequest,
  DeleteSavedQueryRequest,
  RedisHashDeleteFieldRequest,
  RedisListSetIndexRequest,
  RedisListPushRequest,
  RedisListDeleteIndexRequest,
  RedisSetAddRequest,
  RedisSetRemoveRequest,
  RedisZSetAddRequest,
  RedisZSetRemoveRequest,
  CHConfigShape,
  CHListTablesRequest,
  CHTableInfo,
  CHPageRowsRequest,
  CHPageRowsResult,
  CHTruncateTableRequest,
  CHExecuteRequest,
  CHStatementResult,
  CHDeleteRowRequest,
  CHDeleteRowPreview,
  DriverInfo,
  MysqlConfigShape,
  MysqlListTablesRequest,
  MysqlTableInfo,
  MysqlPageRowsRequest,
  MysqlPageRowsResult,
  MysqlExecuteRequest,
  MysqlStatementResult,
  MysqlTruncateTableRequest,
  MysqlTableDDLRequest,
  MysqlCellUpdateRequest,
  MysqlCellUpdatePreview,
  MysqlDropTableRequest,
  MysqlTableColumnsRequest,
  MysqlTableColumnsResult,
  MysqlColumnDef,
  MysqlAlterTableRequest,
  MysqlExportTableRequest,
  MysqlExportTableResult,
  MysqlDeleteRowRequest,
  MysqlDeleteRowPreview,
  PostgresConfigShape,
  PostgresListSchemasRequest,
  PostgresListTablesRequest,
  PostgresRelationInfo,
  PostgresPageRowsRequest,
  PostgresPageRowsResult,
  PostgresExecuteRequest,
  PostgresStatementResult,
  PostgresTruncateTableRequest,
  PostgresCellUpdateRequest,
  PostgresCellUpdatePreview,
  PostgresDeleteRowRequest,
  PostgresDeleteRowPreview,
  EsConfigShape,
  EsIndexInfo,
  EsColumn,
  EsMappingRequest,
  EsPageRowsRequest,
  EsPageRowsResult,
  EsExecuteRequest,
  EsStatementResult,
  EsCellUpdateRequest,
  EsCreateDocRequest,
  EsGetDocRequest,
  HiveConfigShape,
  HiveAuthMode,
  HiveListTablesRequest,
  HiveTableInfo,
  HivePageRowsRequest,
  HivePageRowsResult,
  HiveExecuteRequest,
  HiveStatementResult,
  HiveCellRef,
  HiveCellUpdateRequest,
  HiveCellUpdatePreview,
  HiveDeleteRowRequest,
  HiveDeleteRowPreview,
  HiveTruncateTableRequest,
  HiveDropTableRequest,
  HiveTableColumnsRequest,
  HiveTableColumnsResult,
  HiveColumnDef,
  HiveAlterTableRequest,
  HiveExportTableRequest,
  HiveExportTableResult,
  EsDoc,
  EsPutDocRequest,
  EsDeleteDocRequest,
  EsDeleteByQueryRequest,
  EsCreateIndexRequest,
  EsDeleteIndexRequest,
  EsUpdateIndexSettingsRequest,
  EsDeleteTemplateRequest,
  EsTemplateInfo,
  EsGetTemplateRequest,
  EsTemplateContent,
  EsPutTemplateRequest,
  EsClusterStatsRequest,
  EsClusterStats,
} from './types'

export interface Api {
  createConnection(conn: Connection): Promise<Connection>
  updateConnection(req: UpdateConnectionRequest): Promise<Connection>
  listConnections(): Promise<Connection[]>
  getConnection(id: string): Promise<Connection>
  deleteConnection(id: string): Promise<void>
  // 侧栏连接排序持久化(镜像后端 ReorderConnections):入参为重排后的完整
  // id 顺序。绑定尚未由 wails generate 生成,声明为可选成员(既有 fake 零
  // 改动);调用方用可选链(?.)访问。
  reorderConnections?(ids: string[]): Promise<void>
  // 连接测试:connectionId 可选——编辑既有连接时携带,后端把留空的密码字段
  // 用已存配置回填(「留空=不变」);纯新建测试不传,按提交配置原样测试。
  testConnection(cfg: KafkaConfig, connectionId?: string): Promise<void>
  connect(id: string): Promise<void>
  disconnect(id: string): Promise<void>
  listTopics(id: string): Promise<Topic[]>
  describeTopic(id: string, topic: string): Promise<TopicDetail>
  alterTopicConfig(req: AlterTopicConfigRequest): Promise<void>
  alterTopicPartitions(req: AlterTopicPartitionsRequest): Promise<void>
  getTopicMessageCounts(id: string, topics: string[]): Promise<Record<string, TopicMessageCounts>>
  describeCluster(id: string): Promise<ClusterHealth>
  listConsumerGroups(id: string): Promise<ConsumerGroup[]>
  describeGroup(id: string, group: string): Promise<GroupDetail>
  createTopic(req: CreateTopicRequest): Promise<void>
  deleteTopic(req: DeleteTopicRequest): Promise<void>
  deleteTopics(req: DeleteTopicsRequest): Promise<TopicDeleteResult[]>
  deleteConsumerGroup(req: DeleteConsumerGroupRequest): Promise<void>
  consumeMessages(req: ConsumeRequest): Promise<Message[]>
  consumeMessagesByTimestamp(req: ConsumeRequest): Promise<Message[]>
  getPartitionLag(id: string, topic: string, group: string): Promise<Record<number, number>>
  listActiveProducers(req: ActiveMembersRequest): Promise<ActiveProducer[]>
  listActiveConsumers(req: ActiveMembersRequest): Promise<ActiveConsumer[]>
  resetConsumerGroupOffset(req: ResetOffsetRequest): Promise<void>
  previewResetOffset(req: PreviewOffsetRequest): Promise<PreviewOffsetMap>
  produceMessage(req: ProduceRequest): Promise<void>
  produceMessages(req: BatchProduceRequest): Promise<ProduceResult[]>
  listAudit(limit?: number): Promise<AuditEntry[]>
  saveTextFile(req: SaveTextFileRequest): Promise<string>
  checkUpdate(req: UpdateCheckRequest): Promise<UpdateCheckResult>
  downloadUpdate(req: UpdateDownloadRequest): Promise<void>
  applyUpdate(req: ApplyUpdateRequest): Promise<void>
  updateProgress(): Promise<UpdateProgressInfo>
  openURL(url: string): Promise<void>
  testRedisConnection(cfg: RedisConfigShape, connectionId?: string): Promise<void>
  listRedisDBs(id: string): Promise<RedisDBInfo[]>
  redisScan(req: RedisScanRequest): Promise<RedisScanResult>
  redisGetKey(req: RedisKeyRequest): Promise<RedisValue>
  redisRenameKey(req: RedisKeyRequest): Promise<void>
  redisDeleteKeys(req: RedisDeleteKeysRequest): Promise<number>
  redisSetTTL(req: RedisKeyRequest): Promise<void>
  redisSetString(req: RedisSetStringRequest): Promise<void>
  redisFlushDB(req: RedisFlushRequest): Promise<void>
  redisFlushAll(req: RedisFlushRequest): Promise<void>
  redisServerInfo(id: string): Promise<RedisServerInfo>
  redisHashSetField(req: RedisHashSetFieldRequest): Promise<void>
  redisHashDeleteField(req: RedisHashDeleteFieldRequest): Promise<void>
  redisListSetIndex(req: RedisListSetIndexRequest): Promise<void>
  redisListPush(req: RedisListPushRequest): Promise<void>
  redisListDeleteIndex(req: RedisListDeleteIndexRequest): Promise<void>
  redisSetAdd(req: RedisSetAddRequest): Promise<void>
  redisSetRemove(req: RedisSetRemoveRequest): Promise<void>
  redisZSetAdd(req: RedisZSetAddRequest): Promise<void>
  redisZSetRemove(req: RedisZSetRemoveRequest): Promise<void>
  listSavedQueries(req: ListSavedQueriesRequest): Promise<SavedQuery[]>
  saveSavedQuery(req: SaveSavedQueryRequest): Promise<SavedQuery>
  updateSavedQuery(req: UpdateSavedQueryRequest): Promise<SavedQuery>
  deleteSavedQuery(req: DeleteSavedQueryRequest): Promise<void>
  testCHConnection(cfg: CHConfigShape, connectionId?: string): Promise<void>
  listCHDatabases(id: string): Promise<string[]>
  listCHTables(req: CHListTablesRequest): Promise<CHTableInfo[]>
  chPageRows(req: CHPageRowsRequest): Promise<CHPageRowsResult>
  chTruncateTable(req: CHTruncateTableRequest): Promise<void>
  // 按行删除:可选成员(既有 fake 零改动);调用方用可选链(?.)访问。
  chPreviewDeleteRow?(req: CHDeleteRowRequest): Promise<CHDeleteRowPreview>
  chDeleteRow?(req: CHDeleteRowRequest): Promise<void>
  chExecute(req: CHExecuteRequest): Promise<CHStatementResult[]>
  listDrivers(): Promise<DriverInfo[]>
  // MySQL/TiDB 系列:后端绑定尚未由 wails generate 生成,先声明为可选成员,
  // 避免全仓既有 fakeApi 被迫补齐;调用方用可选链(?.)访问。
  testMysqlConnection?(cfg: MysqlConfigShape, connectionId?: string): Promise<void>
  listMysqlDatabases?(id: string): Promise<string[]>
  listMysqlTables?(req: MysqlListTablesRequest): Promise<MysqlTableInfo[]>
  mysqlPageRows?(req: MysqlPageRowsRequest): Promise<MysqlPageRowsResult>
  mysqlExecute?(req: MysqlExecuteRequest): Promise<MysqlStatementResult[]>
  mysqlPreviewCellUpdate?(req: MysqlCellUpdateRequest): Promise<MysqlCellUpdatePreview>
  mysqlUpdateCell?(req: MysqlCellUpdateRequest): Promise<void>
  mysqlTruncateTable?(req: MysqlTruncateTableRequest): Promise<void>
  // 按行删除(控制台结果与表浏览器共用):绑定尚未由 wails generate 生成,
  // 可选成员(既有 fake 零改动);调用方用可选链(?.)访问。
  mysqlPreviewDeleteRow?(req: MysqlDeleteRowRequest): Promise<MysqlDeleteRowPreview>
  mysqlDeleteRow?(req: MysqlDeleteRowRequest): Promise<void>
  // 表级 DDL/元数据(连接树右键菜单):接口侧声明为可选成员(既有 fake 零
  // 改动),Wails 绑定已由 wails generate 生成;调用方用可选链(?.)访问。
  mysqlDropTable?(req: MysqlDropTableRequest): Promise<void>
  mysqlTableColumns?(req: MysqlTableColumnsRequest): Promise<MysqlTableColumnsResult>
  mysqlAlterTable?(req: MysqlAlterTableRequest): Promise<void>
  mysqlExportTable?(req: MysqlExportTableRequest): Promise<MysqlExportTableResult>
  // 建表语句(SQL 控制台 Ctrl+表名查看):MysqlTableDDL 绑定由后端添加中
  // (wailsjs 尚未重生成),声明为可选成员(既有 fake 零改动);调用方用可选链(?.)访问。
  mysqlTableDDL?(req: MysqlTableDDLRequest): Promise<string>
  testPostgresConnection?(cfg: PostgresConfigShape, connectionId?: string): Promise<void>
  listPostgresDatabases?(id: string): Promise<string[]>
  listPostgresSchemas?(req: PostgresListSchemasRequest): Promise<string[]>
  listPostgresTables?(req: PostgresListTablesRequest): Promise<PostgresRelationInfo[]>
  postgresPageRows?(req: PostgresPageRowsRequest): Promise<PostgresPageRowsResult>
  postgresExecute?(req: PostgresExecuteRequest): Promise<PostgresStatementResult[]>
  postgresTruncateTable?(req: PostgresTruncateTableRequest): Promise<void>
  postgresPreviewDeleteRow?(req: PostgresDeleteRowRequest): Promise<PostgresDeleteRowPreview>
  postgresDeleteRow?(req: PostgresDeleteRowRequest): Promise<void>
  postgresPreviewCellUpdate?(req: PostgresCellUpdateRequest): Promise<PostgresCellUpdatePreview>
  postgresUpdateCell?(req: PostgresCellUpdateRequest): Promise<void>
  // Elasticsearch 系列:后端绑定尚未由 wails generate 生成,同样声明为可选
  // 成员(全仓既有 fakeApi 零改动);调用方用可选链(?.)访问。
  testEsConnection?(cfg: EsConfigShape, connectionId?: string): Promise<void>
  listEsIndices?(id: string): Promise<EsIndexInfo[]>
  esMapping?(req: EsMappingRequest): Promise<EsColumn[]>
  esPageRows?(req: EsPageRowsRequest): Promise<EsPageRowsResult>
  esExecute?(req: EsExecuteRequest): Promise<EsStatementResult[]>
  // 显式刷新索引(POST /{index}/_refresh):EsTableBrowser 手动刷新与写后
  // 自动刷新共用;best-effort 调用,失败被调用方吞掉。
  esRefreshIndex?(req: { connection_id: string; index: string }): Promise<void>
  esGetDoc?(req: EsGetDocRequest): Promise<EsDoc>
  esPutDoc?(req: EsPutDocRequest): Promise<void>
  esUpdateCell?(req: EsCellUpdateRequest): Promise<void>
  esDeleteDoc?(req: EsDeleteDocRequest): Promise<void>
  esDeleteByQuery?(req: EsDeleteByQueryRequest): Promise<number>
  // 新增文档(ESCreateDoc):绑定尚未由 wails generate 生成(主控稍后重生
  // 成),声明为可选成员;调用方用可选链(?.)访问。
  esCreateDoc?(req: EsCreateDocRequest): Promise<EsDoc>
  // ES 集合编辑系列(索引/模板的新建、删除与设置修改):后端绑定尚未由
  // wails generate 生成,同样声明为可选成员;调用方用可选链(?.)访问。
  esCreateIndex?(req: EsCreateIndexRequest): Promise<void>
  esDeleteIndex?(req: EsDeleteIndexRequest): Promise<void>
  esUpdateIndexSettings?(req: EsUpdateIndexSettingsRequest): Promise<void>
  esDeleteTemplate?(req: EsDeleteTemplateRequest): Promise<void>
  // ES 索引模板读取/保存与集群监控:ListEsTemplates/GetEsTemplate/PutEsTemplate/
  // DeleteEsTemplate 绑定已由 wailsjs 生成;EsClusterStats 绑定尚未生成(主控
  // 稍后重生成)。全部声明为可选成员(既有 fake 零改动),调用方用可选链(?.)访问。
  listEsTemplates?(id: string): Promise<EsTemplateInfo[]>
  getEsTemplate?(req: EsGetTemplateRequest): Promise<EsTemplateContent>
  putEsTemplate?(req: EsPutTemplateRequest): Promise<void>
  esClusterStats?(req: EsClusterStatsRequest): Promise<EsClusterStats>
  // Hive 系列:后端绑定尚未由 wails generate 生成,可选成员(既有 fake 零
  // 改动);调用方用可选链(?.)访问。
  testHiveConnection?(cfg: HiveConfigShape, connectionId?: string): Promise<void>
  listHiveDatabases?(id: string): Promise<string[]>
  listHiveTables?(req: HiveListTablesRequest): Promise<HiveTableInfo[]>
  hivePageRows?(req: HivePageRowsRequest): Promise<HivePageRowsResult>
  hiveExecute?(req: HiveExecuteRequest): Promise<HiveStatementResult[]>
  hivePreviewCellUpdate?(req: HiveCellUpdateRequest): Promise<HiveCellUpdatePreview>
  hiveUpdateCell?(req: HiveCellUpdateRequest): Promise<void>
  hivePreviewDeleteRow?(req: HiveDeleteRowRequest): Promise<HiveDeleteRowPreview>
  hiveDeleteRow?(req: HiveDeleteRowRequest): Promise<void>
  hiveTruncateTable?(req: HiveTruncateTableRequest): Promise<void>
  hiveDropTable?(req: HiveDropTableRequest): Promise<void>
  hiveTableColumns?(req: HiveTableColumnsRequest): Promise<HiveTableColumnsResult>
  hiveAlterTable?(req: HiveAlterTableRequest): Promise<void>
  hiveExportTable?(req: HiveExportTableRequest): Promise<HiveExportTableResult>
}

// WailsApi 所有方法的统一转发收口:全文件唯一的类型断言集中在这里。
// 必须经形状断言而不能直接按生成签名调用,因为 wailsjs 生成的 App.d.ts 与实际
// 运行时形状存在两类系统性差异:
// 1. 导出名与本地方法名并不一致(如 testEsConnection→TestESConnection、
//    esDeleteTemplate→DeleteEsTemplate),且历史上部分绑定尚未由 wails
//    generate 生成,声明文件中根本没有对应导出;
// 2. 已生成签名的出入参是 wailsjs models/backend 命名空间里的另一套平行类型,
//    与 ./types 的领域类型结构不兼容(Go []byte 生成侧是 number[],经 JSON
//    传输实为 base64 string,见类定义上方的说明),直传无法通过类型检查。
// 因此把 App 模块一次性断言为「方法名 → 任意参数 → Promise」的宽松形状后按名
// 调用;入参与返回值的领域类型约束由 Api 接口签名承担,断言风险集中在此一处。
function call<T>(name: string, ...args: unknown[]): Promise<T> {
  const fn = (App as unknown as Record<string, (...a: unknown[]) => Promise<T>>)[name]
  return fn(...args)
}

// 连接测试请求统一包装(镜像后端 TestXxxConnectionRequest):编辑既有连接时
// 携带 connection_id,后端把留空的密码字段用已存配置回填(「留空=不变」,
// 列表/详情回传的密码已脱敏,前端不再持有明文);纯新建测试 id 传空串,
// 后端按提交配置原样测试。
function testRequest<C>(cfg: C, connectionId?: string): { connection_id: string; config: C } {
  return { connection_id: connectionId ?? '', config: cfg }
}

// The Wails binding generator models Go `[]byte` fields as `number[]`, but
// over JSON the transport delivers them as base64 strings. The backend model
// therefore exposes Message.Key/Value and Header.Value as `string` so the
// wire format matches our domain types and text (e.g. UTF-8 Chinese) is
// delivered verbatim instead of base64-encoded.
export class WailsApi implements Api {
  createConnection = (conn: Connection): Promise<Connection> => call('CreateConnection', conn)
  updateConnection = (req: UpdateConnectionRequest): Promise<Connection> => call('UpdateConnection', req)
  listConnections = (): Promise<Connection[]> => call('ListConnections')
  getConnection = (id: string): Promise<Connection> => call('GetConnection', id)
  deleteConnection = (id: string): Promise<void> => call('DeleteConnection', id)
  reorderConnections = (ids: string[]): Promise<void> => call('ReorderConnections', ids)
  testConnection = (cfg: KafkaConfig, connectionId?: string): Promise<void> => call('TestConnection', testRequest(cfg, connectionId))
  connect = (id: string): Promise<void> => call('Connect', id)
  disconnect = (id: string): Promise<void> => call('Disconnect', id)
  listTopics = (id: string): Promise<Topic[]> => call('ListTopics', id)
  describeTopic = (id: string, topic: string): Promise<TopicDetail> => call('DescribeTopic', id, topic)
  alterTopicConfig = (req: AlterTopicConfigRequest): Promise<void> => call('AlterTopicConfig', req)
  alterTopicPartitions = (req: AlterTopicPartitionsRequest): Promise<void> => call('AlterTopicPartitions', req)
  getTopicMessageCounts = (id: string, topics: string[]): Promise<Record<string, TopicMessageCounts>> => call('GetTopicMessageCounts', id, topics)
  describeCluster = (id: string): Promise<ClusterHealth> => call('DescribeCluster', id)
  listConsumerGroups = (id: string): Promise<ConsumerGroup[]> => call('ListConsumerGroups', id)
  describeGroup = (id: string, group: string): Promise<GroupDetail> => call('DescribeGroup', id, group)
  createTopic = (req: CreateTopicRequest): Promise<void> => call('CreateTopic', req)
  deleteTopic = (req: DeleteTopicRequest): Promise<void> => call('DeleteTopic', req)
  deleteTopics = (req: DeleteTopicsRequest): Promise<TopicDeleteResult[]> => call('DeleteTopics', req)
  deleteConsumerGroup = (req: DeleteConsumerGroupRequest): Promise<void> => call('DeleteConsumerGroup', req)
  consumeMessages = (req: ConsumeRequest): Promise<Message[]> => call('ConsumeMessages', req)
  consumeMessagesByTimestamp = (req: ConsumeRequest): Promise<Message[]> => call('ConsumeMessagesByTimestamp', req)
  getPartitionLag = (id: string, topic: string, group: string): Promise<Record<number, number>> => call('GetPartitionLag', id, topic, group)
  listActiveProducers = (req: ActiveMembersRequest): Promise<ActiveProducer[]> => call('ListActiveProducers', req)
  listActiveConsumers = (req: ActiveMembersRequest): Promise<ActiveConsumer[]> => call('ListActiveConsumers', req)
  resetConsumerGroupOffset = (req: ResetOffsetRequest): Promise<void> => call('ResetConsumerGroupOffset', req)
  previewResetOffset = (req: PreviewOffsetRequest): Promise<PreviewOffsetMap> => call('PreviewResetOffset', req)
  produceMessage = (req: ProduceRequest): Promise<void> => call('ProduceMessage', req)
  produceMessages = (req: BatchProduceRequest): Promise<ProduceResult[]> => call('ProduceMessages', req)
  listAudit = (limit?: number): Promise<AuditEntry[]> => call('ListAudit', limit ?? 200)
  saveTextFile = (req: SaveTextFileRequest): Promise<string> => call('SaveTextFile', req)
  checkUpdate = (req: UpdateCheckRequest): Promise<UpdateCheckResult> => call('CheckUpdate', req)
  downloadUpdate = (req: UpdateDownloadRequest): Promise<void> => call('DownloadUpdate', req)
  applyUpdate = (req: ApplyUpdateRequest): Promise<void> => call('ApplyUpdate', req)
  updateProgress = (): Promise<UpdateProgressInfo> => call('UpdateProgress')
  openURL = (url: string): Promise<void> => call('OpenURL', url)
  testRedisConnection = (cfg: RedisConfigShape, connectionId?: string): Promise<void> => call('TestRedisConnection', testRequest(cfg, connectionId))
  listRedisDBs = (id: string): Promise<RedisDBInfo[]> => call('ListRedisDBs', id)
  redisScan = (req: RedisScanRequest): Promise<RedisScanResult> => call('RedisScan', req)
  redisGetKey = (req: RedisKeyRequest): Promise<RedisValue> => call('RedisGetKey', req)
  redisRenameKey = (req: RedisKeyRequest): Promise<void> => call('RedisRenameKey', req)
  redisDeleteKeys = (req: RedisDeleteKeysRequest): Promise<number> => call('RedisDeleteKeys', req)
  redisSetTTL = (req: RedisKeyRequest): Promise<void> => call('RedisSetTTL', req)
  redisSetString = (req: RedisSetStringRequest): Promise<void> => call('RedisSetString', req)
  redisFlushDB = (req: RedisFlushRequest): Promise<void> => call('RedisFlushDB', req)
  redisFlushAll = (req: RedisFlushRequest): Promise<void> => call('RedisFlushAll', req)
  redisServerInfo = (id: string): Promise<RedisServerInfo> => call('RedisServerInfo', id)
  redisHashSetField = (req: RedisHashSetFieldRequest): Promise<void> => call('RedisHashSetField', req)
  redisHashDeleteField = (req: RedisHashDeleteFieldRequest): Promise<void> => call('RedisHashDeleteField', req)
  redisListSetIndex = (req: RedisListSetIndexRequest): Promise<void> => call('RedisListSetIndex', req)
  redisListPush = (req: RedisListPushRequest): Promise<void> => call('RedisListPush', req)
  redisListDeleteIndex = (req: RedisListDeleteIndexRequest): Promise<void> => call('RedisListDeleteIndex', req)
  redisSetAdd = (req: RedisSetAddRequest): Promise<void> => call('RedisSetAdd', req)
  redisSetRemove = (req: RedisSetRemoveRequest): Promise<void> => call('RedisSetRemove', req)
  redisZSetAdd = (req: RedisZSetAddRequest): Promise<void> => call('RedisZSetAdd', req)
  redisZSetRemove = (req: RedisZSetRemoveRequest): Promise<void> => call('RedisZSetRemove', req)
  listSavedQueries = (req: ListSavedQueriesRequest): Promise<SavedQuery[]> => call('ListSavedQueries', req)
  saveSavedQuery = (req: SaveSavedQueryRequest): Promise<SavedQuery> => call('SaveSavedQuery', req)
  updateSavedQuery = (req: UpdateSavedQueryRequest): Promise<SavedQuery> => call('UpdateSavedQuery', req)
  deleteSavedQuery = (req: DeleteSavedQueryRequest): Promise<void> => call('DeleteSavedQuery', req)
  testCHConnection = (cfg: CHConfigShape, connectionId?: string): Promise<void> => call('TestCHConnection', testRequest(cfg, connectionId))
  listCHDatabases = (id: string): Promise<string[]> => call('ListCHDatabases', id)
  listCHTables = (req: CHListTablesRequest): Promise<CHTableInfo[]> => call('ListCHTables', req)
  chPageRows = (req: CHPageRowsRequest): Promise<CHPageRowsResult> => call('CHPageRows', req)
  chTruncateTable = (req: CHTruncateTableRequest): Promise<void> => call('CHTruncateTable', req)
  chPreviewDeleteRow = (req: CHDeleteRowRequest): Promise<CHDeleteRowPreview> => call('CHPreviewDeleteRow', req)
  chDeleteRow = (req: CHDeleteRowRequest): Promise<void> => call('CHDeleteRow', req)
  chExecute = (req: CHExecuteRequest): Promise<CHStatementResult[]> => call('CHExecute', req)
  listDrivers = (): Promise<DriverInfo[]> => call('ListDrivers')
  testMysqlConnection = (cfg: MysqlConfigShape, connectionId?: string): Promise<void> => call('TestMysqlConnection', testRequest(cfg, connectionId))
  listMysqlDatabases = (id: string): Promise<string[]> => call('ListMysqlDatabases', id)
  listMysqlTables = (req: MysqlListTablesRequest): Promise<MysqlTableInfo[]> => call('ListMysqlTables', req)
  mysqlPageRows = (req: MysqlPageRowsRequest): Promise<MysqlPageRowsResult> => call('MysqlPageRows', req)
  mysqlExecute = (req: MysqlExecuteRequest): Promise<MysqlStatementResult[]> => call('MysqlExecute', req)
  mysqlPreviewCellUpdate = (req: MysqlCellUpdateRequest): Promise<MysqlCellUpdatePreview> => call('MysqlPreviewCellUpdate', req)
  mysqlUpdateCell = (req: MysqlCellUpdateRequest): Promise<void> => call('MysqlUpdateCell', req)
  mysqlTruncateTable = (req: MysqlTruncateTableRequest): Promise<void> => call('MysqlTruncateTable', req)
  mysqlPreviewDeleteRow = (req: MysqlDeleteRowRequest): Promise<MysqlDeleteRowPreview> => call('MysqlPreviewDeleteRow', req)
  mysqlDeleteRow = (req: MysqlDeleteRowRequest): Promise<void> => call('MysqlDeleteRow', req)
  mysqlDropTable = (req: MysqlDropTableRequest): Promise<void> => call('MysqlDropTable', req)
  mysqlTableColumns = (req: MysqlTableColumnsRequest): Promise<MysqlTableColumnsResult> => call('MysqlTableColumns', req)
  mysqlAlterTable = (req: MysqlAlterTableRequest): Promise<void> => call('MysqlAlterTable', req)
  mysqlExportTable = (req: MysqlExportTableRequest): Promise<MysqlExportTableResult> => call('MysqlExportTable', req)
  mysqlTableDDL = (req: MysqlTableDDLRequest): Promise<string> => call('MysqlTableDDL', req)
  testPostgresConnection = (cfg: PostgresConfigShape, connectionId?: string): Promise<void> => call('TestPostgresConnection', testRequest(cfg, connectionId))
  listPostgresDatabases = (id: string): Promise<string[]> => call('ListPostgresDatabases', id)
  listPostgresSchemas = (req: PostgresListSchemasRequest): Promise<string[]> => call('ListPostgresSchemas', req)
  listPostgresTables = (req: PostgresListTablesRequest): Promise<PostgresRelationInfo[]> => call('ListPostgresTables', req)
  postgresPageRows = (req: PostgresPageRowsRequest): Promise<PostgresPageRowsResult> => call('PostgresPageRows', req)
  postgresExecute = (req: PostgresExecuteRequest): Promise<PostgresStatementResult[]> => call('PostgresExecute', req)
  postgresTruncateTable = (req: PostgresTruncateTableRequest): Promise<void> => call('PostgresTruncateTable', req)
  postgresPreviewDeleteRow = (req: PostgresDeleteRowRequest): Promise<PostgresDeleteRowPreview> => call('PostgresPreviewDeleteRow', req)
  postgresDeleteRow = (req: PostgresDeleteRowRequest): Promise<void> => call('PostgresDeleteRow', req)
  postgresPreviewCellUpdate = (req: PostgresCellUpdateRequest): Promise<PostgresCellUpdatePreview> => call('PostgresPreviewCellUpdate', req)
  postgresUpdateCell = (req: PostgresCellUpdateRequest): Promise<void> => call('PostgresUpdateCell', req)
  testEsConnection = (cfg: EsConfigShape, connectionId?: string): Promise<void> => call('TestESConnection', testRequest(cfg, connectionId))
  listEsIndices = (id: string): Promise<EsIndexInfo[]> => call('ListESIndices', id)
  esMapping = (req: EsMappingRequest): Promise<EsColumn[]> => call('ESMapping', req)
  esPageRows = (req: EsPageRowsRequest): Promise<EsPageRowsResult> => call('ESPageRows', req)
  esExecute = (req: EsExecuteRequest): Promise<EsStatementResult[]> => call('ESExecute', req)
  esRefreshIndex = (req: { connection_id: string; index: string }): Promise<void> => call('EsRefreshIndex', req)
  esGetDoc = (req: EsGetDocRequest): Promise<EsDoc> => call('ESGetDoc', req)
  esPutDoc = (req: EsPutDocRequest): Promise<void> => call('ESPutDoc', req)
  esCreateDoc = (req: EsCreateDocRequest): Promise<EsDoc> => call('EsCreateDoc', req)
  esUpdateCell = (req: EsCellUpdateRequest): Promise<void> => call('ESUpdateCell', req)
  esDeleteDoc = (req: EsDeleteDocRequest): Promise<void> => call('ESDeleteDoc', req)
  esDeleteByQuery = (req: EsDeleteByQueryRequest): Promise<number> => call('ESDeleteByQuery', req)
  esCreateIndex = (req: EsCreateIndexRequest): Promise<void> => call('EsCreateIndex', req)
  esDeleteIndex = (req: EsDeleteIndexRequest): Promise<void> => call('EsDeleteIndex', req)
  esUpdateIndexSettings = (req: EsUpdateIndexSettingsRequest): Promise<void> => call('EsUpdateIndexSettings', req)
  esDeleteTemplate = (req: EsDeleteTemplateRequest): Promise<void> => call('DeleteEsTemplate', req)
  listEsTemplates = (id: string): Promise<EsTemplateInfo[]> => call('ListEsTemplates', id)
  getEsTemplate = (req: EsGetTemplateRequest): Promise<EsTemplateContent> => call('GetEsTemplate', req)
  putEsTemplate = (req: EsPutTemplateRequest): Promise<void> => call('PutEsTemplate', req)
  esClusterStats = (req: EsClusterStatsRequest): Promise<EsClusterStats> => call('EsClusterStats', req)
  testHiveConnection = (cfg: HiveConfigShape, connectionId?: string): Promise<void> => call('TestHiveConnection', testRequest(cfg, connectionId))
  listHiveDatabases = (id: string): Promise<string[]> => call('ListHiveDatabases', id)
  listHiveTables = (req: HiveListTablesRequest): Promise<HiveTableInfo[]> => call('ListHiveTables', req)
  hivePageRows = (req: HivePageRowsRequest): Promise<HivePageRowsResult> => call('HivePageRows', req)
  hiveExecute = (req: HiveExecuteRequest): Promise<HiveStatementResult[]> => call('HiveExecute', req)
  hivePreviewCellUpdate = (req: HiveCellUpdateRequest): Promise<HiveCellUpdatePreview> => call('HivePreviewCellUpdate', req)
  hiveUpdateCell = (req: HiveCellUpdateRequest): Promise<void> => call('HiveUpdateCell', req)
  hivePreviewDeleteRow = (req: HiveDeleteRowRequest): Promise<HiveDeleteRowPreview> => call('HivePreviewDeleteRow', req)
  hiveDeleteRow = (req: HiveDeleteRowRequest): Promise<void> => call('HiveDeleteRow', req)
  hiveTruncateTable = (req: HiveTruncateTableRequest): Promise<void> => call('HiveTruncateTable', req)
  hiveDropTable = (req: HiveDropTableRequest): Promise<void> => call('HiveDropTable', req)
  hiveTableColumns = (req: HiveTableColumnsRequest): Promise<HiveTableColumnsResult> => call('HiveTableColumns', req)
  hiveAlterTable = (req: HiveAlterTableRequest): Promise<void> => call('HiveAlterTable', req)
  hiveExportTable = (req: HiveExportTableRequest): Promise<HiveExportTableResult> => call('HiveExportTable', req)
}

let current: Api = new WailsApi()

export function getApi(): Api {
  return current
}

export function setApi(api: Api): void {
  current = api
}
