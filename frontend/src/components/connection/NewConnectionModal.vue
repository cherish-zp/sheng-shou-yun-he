<script setup lang="ts">
import { reactive, ref, computed, watch, type Component } from 'vue'
import { getApi } from '@/api/client'
import { useConnectionsStore, type NewConnectionInput } from '@/store/connections'
import type { Connection, CHConfigShape, EsConfigShape, HiveConfigShape, KafkaConfig, MysqlConfigShape, PostgresConfigShape, RedisConfigShape, SASLConfig, TLSConfig } from '@/api/types'
import PasswordInput from '@/components/common/PasswordInput.vue'
// Dialog 原语:Root/Overlay/Content 直接取自 reka-ui(不经 ui/dialog 的
// DialogContent——它内置 DialogPortal 会把内容 teleport 到 body,既有用例
// 以 wrapper.find 按组件内 DOM 断言,必须原地渲染);Header/Title/Description/
// Footer 无 portal,复用 ui 基础件。
import { DialogContent, DialogOverlay, DialogRoot } from 'reka-ui'
import { DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import { Braces, Database, Hexagon, Layers, Leaf, Loader2, Search, Table2, X, Zap } from 'lucide-vue-next'

const props = defineProps<{ show: boolean; connection?: Connection | null }>()
const emit = defineEmits<{ (e: 'close'): void }>()

const store = useConnectionsStore()

const SECURITY_PROTOCOLS = ['PLAINTEXT', 'SSL', 'SASL_PLAINTEXT', 'SASL_SSL'] as const

// 两段式第一段:数据库类型卡片。数据来自本地常量数组,后续可换驱动管理页数据。
// 图标统一 lucide 线性件(emoji 字符串随 UI 对齐 DBX 退役)。
type CardType = 'kafka' | 'redis' | 'clickhouse' | 'mysql' | 'tidb' | 'es' | 'postgres' | 'hive'
const TYPE_CARDS: { type: CardType; label: string; icon: Component }[] = [
  { type: 'kafka', label: 'Kafka', icon: Zap },
  { type: 'redis', label: 'Redis', icon: Braces },
  { type: 'clickhouse', label: 'ClickHouse', icon: Database },
  { type: 'mysql', label: 'MySQL', icon: Table2 },
  { type: 'tidb', label: 'TiDB', icon: Leaf },
  { type: 'es', label: 'Elasticsearch', icon: Search },
  { type: 'postgres', label: 'PostgreSQL', icon: Layers },
  { type: 'hive', label: 'Hive', icon: Hexagon },
]

// 原生 select/checkbox 暂不替换为 reka 原语:既有用例以 setValue/checked
// 断言驱动,原语(button 形态)会破坏交互契约;外观按 ui Input 的 token
// 类手工对齐(语义 token 亮暗主题自动生效)。
const SELECT_CLS = 'h-8 w-full min-w-0 rounded-md border border-input bg-transparent px-2.5 text-[13px] shadow-xs outline-none transition-[color,box-shadow] focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50 disabled:cursor-not-allowed disabled:opacity-50'
const CHECK_CLS = 'size-3.5 shrink-0 accent-primary'

// createDefaultForm 表单初始值工厂:每次「新建连接」打开都从空白开始,
// 不残留上一次输入(编辑模式由 fillFrom 覆盖,不受影响)。
function createDefaultForm() {
  return {
    name: '',
    connType: 'kafka' as CardType,
    addr: '',
    redisPassword: '',
    redisDB: 0,
    redisTLS: false,
    brokers: '',
    securityProtocol: 'PLAINTEXT',
    mechanism: 'PLAIN',
    username: '',
    password: '',
    principal: '',
    keytabPath: '',
    krb5ConfPath: '',
    serviceName: 'kafka',
    caCert: '',
    insecureSkipVerify: false,
    chHosts: '',
    chUsername: 'default',
    chPassword: '',
    chDatabase: 'default',
    chTLS: false,
    chProtocol: 'native' as 'native' | 'http',
    // mysql/tidb 共用一套字段。
    mysqlHost: '',
    mysqlPort: 3306,
    mysqlUsername: 'root',
    mysqlPassword: '',
    mysqlDatabase: '',
    mysqlTlsMode: 'disabled' as MysqlConfigShape['tls_mode'],
    // es:hosts 地址自带端口,无需端口预填;认证按 auth_mode 三选一。
    esHosts: '',
    esUsername: '',
    esPassword: '',
    esApiKey: '',
    esAuthMode: 'none' as EsConfigShape['auth_mode'],
    esTlsMode: 'disabled' as EsConfigShape['tls_mode'],
    // postgres:独立一套字段;search_path 可选,超时默认 5000ms。
    pgHost: '',
    pgPort: 5432,
    pgUsername: 'postgres',
    pgPassword: '',
    pgDatabase: '',
    pgTlsMode: 'disable' as PostgresConfigShape['tls_mode'],
    pgSearchPath: '',
    pgConnectTimeoutMs: 5000,
    // hive:auth_mode 决定凭据字段显隐;默认库可选(留空 = default)。
    hiveHost: '',
    hivePort: 10000,
    hiveAuthMode: 'nosasl' as HiveConfigShape['auth_mode'],
    hiveUsername: '',
    hivePassword: '',
    hiveDatabase: '',
    hivePrincipal: '',
    hiveKeytab: '',
    hiveKrb5Conf: '',
  }
}

const form = reactive(createDefaultForm())
const testing = ref(false)
const tested = ref(false)
const testError = ref<string | null>(null)
const saveError = ref<string | null>(null)
// 删除确认层状态(声明需在下方 immediate watch 之前,回调会访问)。
const deleteConfirmOpen = ref(false)
const deleting = ref(false)
const deleteError = ref<string | null>(null)
// 密码可见性:由 common/PasswordInput 内部管理,各密码框实例独立切换,默认密文。

const editing = computed(() => !!props.connection)

// 编辑态凭据占位提示:后端不再回传明文密码(列表/详情出口脱敏),留空保存/
// 测试均表示沿用原密码(后端按「留空=不变」回填);新建态无此语义,不提示。
const secretPlaceholder = computed(() => (editing.value ? '留空则保持原密码' : undefined))

const isSasl = computed(() => form.securityProtocol.startsWith('SASL_'))
const isTLS = computed(() => form.securityProtocol.endsWith('_SSL'))
const isGssapi = computed(() => isSasl.value && form.mechanism === 'GSSAPI')

// envDefaults 由部署环境的 DS_KAFKA_* 变量提供 Kerberos 预填值
// (新建连接时的初始默认,可覆盖)。
function envDefault(key: string): string {
  const v = process.env[key]
  return typeof v === 'string' ? v : ''
}

// pickType 响应类型卡片点击:切进 mysql/tidb 时按类型预填默认端口与 TLS 模式
// (MySQL 3306/disabled,TiDB 4000/disabled)。自建 TiDB 默认不开 TLS,
// TLS 供 TiDB Cloud 等托管服务显式选择;es 的地址自带端口,无需预填。
// 编辑回填(fillFrom)直接写 connType,不会触发这里的预填,避免覆盖已有配置。
function pickType(t: CardType): void {
  form.connType = t
  if (t === 'mysql') {
    form.mysqlPort = 3306
    form.mysqlTlsMode = 'disabled'
  } else if (t === 'tidb') {
    form.mysqlPort = 4000
    form.mysqlTlsMode = 'disabled'
  } else if (t === 'postgres') {
    form.pgPort = 5432
    form.pgTlsMode = 'disable'
    form.pgConnectTimeoutMs = 5000
  } else if (t === 'hive') {
    form.hivePort = 10000
    form.hiveAuthMode = 'nosasl'
  }
}

// fillFrom 用已有连接预填表单。旧配置缺 security_protocol 时按
// sasl/tls 布尔推导(sasl.enabled→SASL_PLAINTEXT, tls.enabled→SSL,
// 两者→SASL_SSL, 否则 PLAINTEXT),与后端 EffectiveSecurityProtocol 一致。
function fillFrom(conn: Connection): void {
  form.name = conn.name
  if (conn.type === 'redis') {
    const cfg = conn.config as RedisConfigShape
    form.connType = 'redis'
    form.addr = cfg.addr
    form.redisPassword = cfg.password ?? ''
    form.redisDB = cfg.db ?? 0
    form.redisTLS = !!cfg.tls
    return
  }
  if (conn.type === 'clickhouse') {
    const cfg = conn.config as CHConfigShape
    form.connType = 'clickhouse'
    form.chHosts = cfg.hosts.join(', ')
    form.chUsername = cfg.username
    form.chPassword = cfg.password ?? ''
    form.chDatabase = cfg.database
    form.chTLS = !!cfg.tls
    // 旧配置缺省 protocol 时按 native 回填,与后端归一逻辑一致。
    form.chProtocol = cfg.protocol ?? 'native'
    return
  }
  if (conn.type === 'mysql' || conn.type === 'tidb') {
    const cfg = conn.config as MysqlConfigShape
    form.connType = conn.type
    form.mysqlHost = cfg.host
    form.mysqlPort = cfg.port
    form.mysqlUsername = cfg.username
    form.mysqlPassword = cfg.password
    form.mysqlDatabase = cfg.database
    // 旧配置缺省 tls_mode 时按 disabled 回填。
    form.mysqlTlsMode = cfg.tls_mode ?? 'disabled'
    return
  }
  if (conn.type === 'postgres') {
    const cfg = conn.config as PostgresConfigShape
    form.connType = 'postgres'
    form.pgHost = cfg.host
    form.pgPort = cfg.port
    form.pgUsername = cfg.username
    form.pgPassword = cfg.password
    form.pgDatabase = cfg.database
    // 旧配置缺省 tls_mode/timeout 时按 disable/5000 回填。
    form.pgTlsMode = cfg.tls_mode ?? 'disable'
    form.pgSearchPath = cfg.search_path ?? ''
    form.pgConnectTimeoutMs = cfg.connect_timeout_ms ?? 5000
    return
  }
  if (conn.type === 'hive') {
    const cfg = conn.config as HiveConfigShape
    form.connType = 'hive'
    form.hiveHost = cfg.host
    form.hivePort = cfg.port
    // 旧配置缺省 auth_mode 时按 nosasl 回填。
    form.hiveAuthMode = cfg.auth_mode ?? 'nosasl'
    form.hiveUsername = cfg.username ?? ''
    form.hivePassword = cfg.password ?? ''
    form.hiveDatabase = cfg.database ?? ''
    form.hivePrincipal = cfg.kerberos?.principal ?? ''
    form.hiveKeytab = cfg.kerberos?.keytab ?? ''
    form.hiveKrb5Conf = cfg.kerberos?.krb5_conf ?? ''
    return
  }
  if (conn.type === 'es') {
    const cfg = conn.config as EsConfigShape
    form.connType = 'es'
    form.esHosts = cfg.hosts.join(', ')
    // 旧配置缺省 auth_mode 时按 none 回填。
    form.esAuthMode = cfg.auth_mode ?? 'none'
    form.esUsername = cfg.username ?? ''
    form.esPassword = cfg.password ?? ''
    form.esApiKey = cfg.api_key ?? ''
    // 旧配置缺省 tls_mode 时按 disabled 回填。
    form.esTlsMode = cfg.tls_mode ?? 'disabled'
    return
  }
  form.connType = 'kafka'
  const cfg = conn.config as KafkaConfig
  const sasl = cfg.sasl
  const tls = cfg.tls
  form.brokers = cfg.bootstrap_servers.join(', ')
  form.securityProtocol = cfg.security_protocol
    || (sasl?.enabled && tls?.enabled ? 'SASL_SSL' : sasl?.enabled ? 'SASL_PLAINTEXT' : tls?.enabled ? 'SSL' : 'PLAINTEXT')
  form.mechanism = sasl?.mechanism || 'PLAIN'
  form.username = sasl?.username ?? ''
  form.password = sasl?.password ?? ''
  form.principal = sasl?.principal ?? ''
  form.keytabPath = sasl?.keytab_path ?? ''
  form.krb5ConfPath = sasl?.krb5_conf_path ?? ''
  form.serviceName = sasl?.service_name || 'kafka'
  form.caCert = tls?.ca_cert ?? ''
  form.insecureSkipVerify = !!tls?.insecure_skip_verify
}

watch(
  () => props.show,
  (show) => {
    if (!show) {
      // 关闭时一并收起删除确认层,避免下次打开残留上一次的确认态。
      deleteConfirmOpen.value = false
      deleteError.value = null
      return
    }
    if (props.connection) {
      fillFrom(props.connection)
      return
    }
    // 新建模式:先整体重置为初始值(不残留上一次的输入),再应用环境默认。
    Object.assign(form, createDefaultForm())
    const principal = envDefault('DS_KAFKA_KERBEROS_PRINCIPAL')
    if (principal) {
      form.securityProtocol = envDefault('DS_KAFKA_SECURITY_PROTOCOL') || 'SASL_PLAINTEXT'
      form.mechanism = envDefault('DS_KAFKA_SASL_MECHANISM') || 'GSSAPI'
      form.principal = principal
      form.keytabPath = envDefault('DS_KAFKA_KERBEROS_KEYTAB')
      form.krb5ConfPath = envDefault('DS_KERBEROS_KRB5FILE')
      form.serviceName = envDefault('DS_KAFKA_SASL_KERBEROS_SERVICE_NAME') || 'kafka'
    }
  },
  // 表单可能以 show=true 直接挂载,立即预填一次。
  { immediate: true },
)

const brokers = computed(() => form.brokers.split(',').map((s) => s.trim()).filter(Boolean))
const chHosts = computed(() => form.chHosts.split(',').map((s) => s.trim()).filter(Boolean))
const esHosts = computed(() => form.esHosts.split(',').map((s) => s.trim()).filter(Boolean))
const sasl = computed<SASLConfig | undefined>(() => {
  if (!isSasl.value) return undefined
  if (isGssapi.value) {
    return {
      enabled: true,
      mechanism: 'GSSAPI',
      principal: form.principal,
      keytab_path: form.keytabPath,
      krb5_conf_path: form.krb5ConfPath,
      service_name: form.serviceName || 'kafka',
    }
  }
  return { enabled: true, mechanism: form.mechanism, username: form.username, password: form.password }
})
const tls = computed<TLSConfig | undefined>(() => {
  if (!isTLS.value && !form.caCert && !form.insecureSkipVerify) return undefined
  return { enabled: isTLS.value, ca_cert: form.caCert, insecure_skip_verify: form.insecureSkipVerify }
})
const config = computed<KafkaConfig | RedisConfigShape | CHConfigShape | MysqlConfigShape | EsConfigShape | PostgresConfigShape | HiveConfigShape>(() => {
  if (form.connType === 'redis') {
    return {
      addr: form.addr,
      password: form.redisPassword || undefined,
      db: form.redisDB,
      tls: form.redisTLS,
    }
  }
  if (form.connType === 'clickhouse') {
    return {
      hosts: chHosts.value,
      username: form.chUsername,
      password: form.chPassword || undefined,
      database: form.chDatabase,
      tls: form.chTLS,
      // 显式携带(而非缺省省略),保存后编辑回填与后端归一都更清晰。
      protocol: form.chProtocol,
    }
  }
  if (form.connType === 'mysql' || form.connType === 'tidb') {
    return {
      host: form.mysqlHost,
      port: Number(form.mysqlPort) || 0,
      username: form.mysqlUsername,
      password: form.mysqlPassword,
      database: form.mysqlDatabase,
      tls_mode: form.mysqlTlsMode,
    }
  }
  if (form.connType === 'postgres') {
    return {
      host: form.pgHost,
      port: Number(form.pgPort) || 0,
      username: form.pgUsername,
      password: form.pgPassword,
      database: form.pgDatabase,
      tls_mode: form.pgTlsMode,
      ...(form.pgSearchPath.trim() !== '' ? { search_path: form.pgSearchPath.trim() } : {}),
      connect_timeout_ms: Number(form.pgConnectTimeoutMs) || 5000,
    }
  }
  if (form.connType === 'hive') {
    // 凭据按认证方式裁剪:username/password 仅 LDAP 携带;kerberos 走子对象;
    // 默认库可选,留空省略(后端语义 = default);krb5_conf 可选同理。
    return {
      host: form.hiveHost,
      port: Number(form.hivePort) || 0,
      auth_mode: form.hiveAuthMode,
      ...(form.hiveAuthMode === 'ldap' ? { username: form.hiveUsername, password: form.hivePassword } : {}),
      ...(form.hiveDatabase.trim() !== '' ? { database: form.hiveDatabase.trim() } : {}),
      ...(form.hiveAuthMode === 'kerberos'
        ? {
            kerberos: {
              principal: form.hivePrincipal,
              keytab: form.hiveKeytab,
              ...(form.hiveKrb5Conf.trim() !== '' ? { krb5_conf: form.hiveKrb5Conf.trim() } : {}),
            },
          }
        : {}),
    }
  }
  if (form.connType === 'es') {
    // 凭据字段始终显式携带(非对应模式为空串),保存后回填与后端归一都更清晰。
    return {
      hosts: esHosts.value,
      username: form.esUsername,
      password: form.esPassword,
      api_key: form.esApiKey,
      auth_mode: form.esAuthMode,
      tls_mode: form.esTlsMode,
    }
  }
  return {
    bootstrap_servers: brokers.value,
    security_protocol: form.securityProtocol,
    sasl: sasl.value,
    tls: tls.value,
  }
})

const nameInvalid = computed(() => form.name.trim() === '')
const brokersInvalid = computed(() => form.connType === 'kafka' && brokers.value.length === 0)
const addrInvalid = computed(() => form.connType === 'redis' && form.addr.trim() === '')
const chHostsInvalid = computed(() => form.connType === 'clickhouse' && chHosts.value.length === 0)
const isMysqlFamily = computed(() => form.connType === 'mysql' || form.connType === 'tidb')
const mysqlHostInvalid = computed(() => isMysqlFamily.value && form.mysqlHost.trim() === '')
const mysqlPortInvalid = computed(() => isMysqlFamily.value && (Number(form.mysqlPort) < 1 || Number(form.mysqlPort) > 65535))
// ES 地址任一非空即可(逗号/空格不算有效地址)。
const esHostsInvalid = computed(() => form.connType === 'es' && esHosts.value.length === 0)
const pgHostInvalid = computed(() => form.connType === 'postgres' && form.pgHost.trim() === '')
const pgPortInvalid = computed(() => form.connType === 'postgres' && (Number(form.pgPort) < 1 || Number(form.pgPort) > 65535))
// PG 数据库名必填(连接串必须携带 db,后端无法从空库推导默认库)。
const pgDatabaseInvalid = computed(() => form.connType === 'postgres' && form.pgDatabase.trim() === '')
// hive:主机/端口必填;LDAP 认证用户名必填;kerberos 认证 principal/keytab 必填。
const hiveHostInvalid = computed(() => form.connType === 'hive' && form.hiveHost.trim() === '')
const hivePortInvalid = computed(() => form.connType === 'hive' && (Number(form.hivePort) < 1 || Number(form.hivePort) > 65535))
const hiveUsernameInvalid = computed(() => form.connType === 'hive' && form.hiveAuthMode === 'ldap' && form.hiveUsername.trim() === '')
const hivePrincipalInvalid = computed(() => form.connType === 'hive' && form.hiveAuthMode === 'kerberos' && form.hivePrincipal.trim() === '')
const hiveKeytabInvalid = computed(() => form.connType === 'hive' && form.hiveAuthMode === 'kerberos' && form.hiveKeytab.trim() === '')
const saveInvalid = computed(() => nameInvalid.value || brokersInvalid.value || addrInvalid.value || chHostsInvalid.value || mysqlHostInvalid.value || mysqlPortInvalid.value || esHostsInvalid.value || pgHostInvalid.value || pgPortInvalid.value || pgDatabaseInvalid.value || hiveHostInvalid.value || hivePortInvalid.value || hiveUsernameInvalid.value || hivePrincipalInvalid.value || hiveKeytabInvalid.value)
// 测试连接不需要名称,只校验目标地址。
const targetInvalid = computed(() => brokersInvalid.value || addrInvalid.value || chHostsInvalid.value || mysqlHostInvalid.value || mysqlPortInvalid.value || esHostsInvalid.value || pgHostInvalid.value || pgPortInvalid.value || pgDatabaseInvalid.value || hiveHostInvalid.value || hivePortInvalid.value || hiveUsernameInvalid.value || hivePrincipalInvalid.value || hiveKeytabInvalid.value)

async function runTest(): Promise<void> {
  if (targetInvalid.value) return
  testing.value = true
  tested.value = false
  testError.value = null
  // 编辑既有连接时携带 connection_id:后端把留空的密码字段回填为已存值
  // (「留空=不变」,凭据不回传渲染层);新建模式不携带,按表单原样测试。
  const testConnId = editing.value ? props.connection?.id : undefined
  try {
    // 按类型分派:redis 走 TestRedisConnection,clickhouse 走 TestCHConnection,
    // mysql/tidb 共用 TestMysqlConnection(config 形状相同),es 走 TestESConnection,
    // hive 走 TestHiveConnection,kafka 走原 TestConnection。
    if (form.connType === 'redis') {
      await getApi().testRedisConnection(config.value as RedisConfigShape, testConnId)
    } else if (form.connType === 'clickhouse') {
      await getApi().testCHConnection(config.value as CHConfigShape, testConnId)
    } else if (form.connType === 'mysql' || form.connType === 'tidb') {
      await getApi().testMysqlConnection?.(config.value as MysqlConfigShape, testConnId)
    } else if (form.connType === 'postgres') {
      await getApi().testPostgresConnection?.(config.value as PostgresConfigShape, testConnId)
    } else if (form.connType === 'hive') {
      await getApi().testHiveConnection?.(config.value as HiveConfigShape, testConnId)
    } else if (form.connType === 'es') {
      await getApi().testEsConnection?.(config.value as EsConfigShape, testConnId)
    } else {
      await store.testConnection(config.value as KafkaConfig, testConnId)
    }
    tested.value = true
  } catch (e) {
    testError.value = e instanceof Error ? e.message : String(e)
  } finally {
    testing.value = false
  }
}

async function save(): Promise<void> {
  saveError.value = null
  if (saveInvalid.value) return
  try {
    // store 入参的 config 联合类型尚未纳入 MysqlConfigShape/EsConfigShape,
    // 这里收窄断言;type 始终显式携带 form.connType,避免后端对空 type 默认 kafka。
    const input = { name: form.name.trim(), type: form.connType, config: config.value } as NewConnectionInput
    if (props.connection) {
      await store.update(props.connection.id, input)
    } else {
      await store.create(input)
    }
    emit('close')
  } catch (e) {
    saveError.value = e instanceof Error ? e.message : String(e)
  }
}

// 编辑模式的删除入口:点击后先弹应用内确认层(盖过本弹窗,z-index 与
// PromptDialog/ResetOffsetDialog 的 1200 对齐;ConfirmDialog 组件自身只有
// z-index 100,会被弹窗压住,故在弹窗内自建同构确认层)。确认后经
// store.remove 删除(内部调 deleteConnection 并同步本地列表与状态,与
// App.vue 的删除链路一致),成功后关闭弹窗;失败原因展示在确认层内。
function askDeleteConnection(): void {
  deleteError.value = null
  deleteConfirmOpen.value = true
}

function cancelDeleteConnection(): void {
  deleteConfirmOpen.value = false
}

async function confirmDeleteConnection(): Promise<void> {
  if (!props.connection || deleting.value) return
  deleting.value = true
  deleteError.value = null
  try {
    await store.remove(props.connection.id)
    deleteConfirmOpen.value = false
    emit('close')
  } catch (e) {
    deleteError.value = e instanceof Error ? e.message : String(e)
  } finally {
    deleting.value = false
  }
}

function close(): void {
  emit('close')
}
</script>

<template>
  <!-- Dialog 原语(无 Portal,原地渲染):遮罩 bg-black/50,面板在
       DialogContent 的视口盒内 grid 居中,避免 transform 破坏内部 fixed
       定位(删除确认层 absolute inset-0 依赖 DialogContent 的视口盒)。 -->
  <DialogRoot :open="show">
    <DialogOverlay v-if="show" class="fixed inset-0 z-50 bg-black/50" />
    <DialogContent
      v-if="show"
      class="fixed inset-0 z-50 grid place-items-center overflow-hidden p-4"
      data-test="new-connection-modal"
    >
      <div class="modal flex max-h-[85vh] w-[460px] max-w-[92vw] flex-col overflow-hidden rounded-lg border border-border bg-background shadow-lg">
        <DialogHeader class="modal-header flex-row items-center justify-between gap-2 border-b border-border px-4 py-3 text-left">
          <DialogTitle class="modal-title text-[15px] font-semibold" data-test="modal-title">{{ editing ? '编辑连接' : '新建连接' }}</DialogTitle>
          <DialogDescription class="sr-only">连接配置表单</DialogDescription>
          <button class="modal-close flex size-7 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-accent hover:text-foreground" type="button" data-test="modal-close" @click="close">
            <X class="size-4" aria-hidden="true" />
          </button>
        </DialogHeader>
        <div class="modal-body grid grid-cols-2 gap-x-3 gap-y-3.5 overflow-y-auto px-4 py-4">
          <div class="field col-span-2 flex flex-col gap-1.5">
            <Label class="text-xs font-medium text-muted-foreground">名称 <span class="req text-destructive">*</span></Label>
            <Input :value="form.name" @input="form.name = ($event.target as HTMLInputElement).value" data-test="input-name" class="h-8 text-[13px]" placeholder="例如：本地开发" autocapitalize="off" autocorrect="off" autocomplete="off" />
            <span v-if="nameInvalid" class="err text-xs text-destructive">名称不能为空</span>
          </div>
          <div class="field col-span-2 flex flex-col gap-1.5">
            <Label class="text-xs font-medium text-muted-foreground">数据库类型</Label>
            <div class="type-grid grid grid-cols-4 gap-2" data-test="type-grid">
              <button
                v-for="c in TYPE_CARDS"
                :key="c.type"
                type="button"
                class="type-card flex flex-col items-center gap-1 rounded-lg border px-2 py-2.5 transition-colors"
                :class="form.connType === c.type ? 'active border-primary bg-background ring-1 ring-primary/30' : 'border-border bg-muted/40 hover:border-ring hover:bg-background'"
                :data-test="`type-card-${c.type}`"
                @click="pickType(c.type)"
              >
                <component :is="c.icon" class="size-5 shrink-0 text-foreground" aria-hidden="true" />
                <span class="type-card-label text-xs font-medium">{{ c.label }}</span>
              </button>
            </div>
          </div>
          <template v-if="form.connType === 'redis'">
            <div class="field col-span-2 flex flex-col gap-1.5">
              <Label class="text-xs font-medium text-muted-foreground">地址 <span class="req text-destructive">*</span>(集群可填逗号分隔多个种子)</Label>
              <Input :value="form.addr" @input="form.addr = ($event.target as HTMLInputElement).value" data-test="input-addr" class="h-8 text-[13px]" placeholder="127.0.0.1:6379" autocapitalize="off" autocorrect="off" autocomplete="off" spellcheck="false" />
            </div>
            <div class="field flex flex-col gap-1.5">
              <Label class="text-xs font-medium text-muted-foreground">密码(可选)</Label>
              <PasswordInput v-model="form.redisPassword" data-test="input-redis-password" toggle-testid="toggle-password-redis" :placeholder="secretPlaceholder" />
            </div>
            <div class="field flex flex-col gap-1.5">
              <Label class="text-xs font-medium text-muted-foreground">DB(集群模式固定 0)</Label>
              <Input :value="form.redisDB" @input="form.redisDB = Number(($event.target as HTMLInputElement).value)" data-test="input-redis-db" class="h-8 text-[13px]" type="number" min="0" />
            </div>
          </template>
          <template v-else-if="form.connType === 'clickhouse'">
            <div class="field col-span-2 flex flex-col gap-1.5">
              <Label class="text-xs font-medium text-muted-foreground">节点地址 <span class="req text-destructive">*</span>(多节点逗号分隔)</Label>
              <Input :value="form.chHosts" @input="form.chHosts = ($event.target as HTMLInputElement).value" data-test="input-ch-hosts" class="h-8 text-[13px]" placeholder="node1:9000,node2:9000" autocapitalize="off" autocorrect="off" autocomplete="off" spellcheck="false" />
              <span class="hint text-xs text-muted-foreground" data-test="ch-port-hint">原生协议默认端口 9000;HTTP 协议默认端口 8123</span>
              <span v-if="chHostsInvalid" class="err text-xs text-destructive">至少填写一个节点</span>
            </div>
            <div class="field flex flex-col gap-1.5">
              <Label class="text-xs font-medium text-muted-foreground">协议</Label>
              <select v-model="form.chProtocol" :class="SELECT_CLS" data-test="input-ch-protocol">
                <option value="native">Native(9000)</option>
                <option value="http">HTTP(8123)</option>
              </select>
            </div>
            <div class="field flex flex-col gap-1.5">
              <Label class="text-xs font-medium text-muted-foreground">用户名</Label>
              <Input :value="form.chUsername" @input="form.chUsername = ($event.target as HTMLInputElement).value" data-test="input-ch-username" class="h-8 text-[13px]" autocapitalize="off" autocorrect="off" autocomplete="off" spellcheck="false" />
            </div>
            <div class="field flex flex-col gap-1.5">
              <Label class="text-xs font-medium text-muted-foreground">密码</Label>
              <PasswordInput v-model="form.chPassword" data-test="input-ch-password" toggle-testid="toggle-password-ch" :placeholder="secretPlaceholder" />
            </div>
            <div class="field flex flex-col gap-1.5">
              <Label class="text-xs font-medium text-muted-foreground">数据库</Label>
              <Input :value="form.chDatabase" @input="form.chDatabase = ($event.target as HTMLInputElement).value" data-test="input-ch-database" class="h-8 text-[13px]" autocapitalize="off" autocorrect="off" autocomplete="off" spellcheck="false" />
            </div>
            <div class="field col-span-2 flex items-center">
              <label class="checkbox-label flex items-center gap-2 text-[13px]">
                <input v-model="form.chTLS" type="checkbox" :class="CHECK_CLS" data-test="input-ch-tls" />
                启用 TLS
              </label>
            </div>
          </template>
          <template v-else-if="form.connType === 'mysql' || form.connType === 'tidb'">
            <div class="field flex flex-col gap-1.5">
              <Label class="text-xs font-medium text-muted-foreground">主机 <span class="req text-destructive">*</span></Label>
              <Input :value="form.mysqlHost" @input="form.mysqlHost = ($event.target as HTMLInputElement).value" data-test="input-mysql-host" class="h-8 text-[13px]" placeholder="127.0.0.1" autocapitalize="off" autocorrect="off" autocomplete="off" spellcheck="false" />
              <span v-if="mysqlHostInvalid" class="err text-xs text-destructive">主机不能为空</span>
            </div>
            <div class="field flex flex-col gap-1.5">
              <Label class="text-xs font-medium text-muted-foreground">端口 <span class="req text-destructive">*</span></Label>
              <Input :value="form.mysqlPort" @input="form.mysqlPort = Number(($event.target as HTMLInputElement).value)" data-test="input-mysql-port" class="h-8 text-[13px]" type="number" min="1" max="65535" />
              <span v-if="mysqlPortInvalid" class="err text-xs text-destructive">端口需为 1-65535</span>
              <span class="hint text-xs text-muted-foreground" data-test="mysql-port-hint">MySQL 默认 3306;TiDB 默认 4000</span>
            </div>
            <div class="field flex flex-col gap-1.5">
              <Label class="text-xs font-medium text-muted-foreground">用户名</Label>
              <Input :value="form.mysqlUsername" @input="form.mysqlUsername = ($event.target as HTMLInputElement).value" data-test="input-mysql-username" class="h-8 text-[13px]" autocapitalize="off" autocorrect="off" autocomplete="off" spellcheck="false" />
            </div>
            <div class="field flex flex-col gap-1.5">
              <Label class="text-xs font-medium text-muted-foreground">密码</Label>
              <PasswordInput v-model="form.mysqlPassword" data-test="input-mysql-password" toggle-testid="toggle-password-mysql" :placeholder="secretPlaceholder" />
            </div>
            <div class="field flex flex-col gap-1.5">
              <Label class="text-xs font-medium text-muted-foreground">数据库</Label>
              <Input :value="form.mysqlDatabase" @input="form.mysqlDatabase = ($event.target as HTMLInputElement).value" data-test="input-mysql-database" class="h-8 text-[13px]" autocapitalize="off" autocorrect="off" autocomplete="off" spellcheck="false" />
            </div>
            <div class="field flex flex-col gap-1.5">
              <Label class="text-xs font-medium text-muted-foreground">TLS</Label>
              <select v-model="form.mysqlTlsMode" :class="SELECT_CLS" data-test="input-mysql-tls-mode">
                <option value="disabled">禁用</option>
                <option value="skip-verify">启用（跳过证书校验）</option>
                <option value="verify-full">启用（校验证书）</option>
              </select>
              <p class="hint text-xs text-muted-foreground" data-test="mysql-tls-hint">自建 MySQL/TiDB 默认禁用即可;TiDB Cloud 等托管服务需选择启用</p>
            </div>
          </template>
          <template v-else-if="form.connType === 'postgres'">
            <div class="field flex flex-col gap-1.5">
              <Label class="text-xs font-medium text-muted-foreground">主机 <span class="req text-destructive">*</span></Label>
              <Input :value="form.pgHost" @input="form.pgHost = ($event.target as HTMLInputElement).value" data-test="input-postgres-host" class="h-8 text-[13px]" placeholder="127.0.0.1" autocapitalize="off" autocorrect="off" autocomplete="off" spellcheck="false" />
              <span v-if="pgHostInvalid" class="err text-xs text-destructive">主机不能为空</span>
            </div>
            <div class="field flex flex-col gap-1.5">
              <Label class="text-xs font-medium text-muted-foreground">端口</Label>
              <Input :value="form.pgPort" @input="form.pgPort = Number(($event.target as HTMLInputElement).value)" data-test="input-postgres-port" class="h-8 text-[13px]" type="number" min="1" max="65535" />
              <span v-if="pgPortInvalid" class="err text-xs text-destructive">端口需为 1-65535</span>
              <span class="hint text-xs text-muted-foreground" data-test="postgres-port-hint">PostgreSQL 默认 5432</span>
            </div>
            <div class="field flex flex-col gap-1.5">
              <Label class="text-xs font-medium text-muted-foreground">用户名</Label>
              <Input :value="form.pgUsername" @input="form.pgUsername = ($event.target as HTMLInputElement).value" data-test="input-postgres-username" class="h-8 text-[13px]" autocapitalize="off" autocorrect="off" autocomplete="off" spellcheck="false" />
            </div>
            <div class="field flex flex-col gap-1.5">
              <Label class="text-xs font-medium text-muted-foreground">密码</Label>
              <PasswordInput v-model="form.pgPassword" data-test="input-postgres-password" toggle-testid="toggle-password-postgres" :placeholder="secretPlaceholder" />
            </div>
            <div class="field flex flex-col gap-1.5">
              <Label class="text-xs font-medium text-muted-foreground">数据库 <span class="req text-destructive">*</span></Label>
              <Input :value="form.pgDatabase" @input="form.pgDatabase = ($event.target as HTMLInputElement).value" data-test="input-postgres-database" class="h-8 text-[13px]" placeholder="postgres" autocapitalize="off" autocorrect="off" autocomplete="off" spellcheck="false" />
              <span v-if="pgDatabaseInvalid" class="err text-xs text-destructive" data-test="postgres-database-err">数据库不能为空</span>
            </div>
            <div class="field flex flex-col gap-1.5">
              <Label class="text-xs font-medium text-muted-foreground">search_path(可选)</Label>
              <Input :value="form.pgSearchPath" @input="form.pgSearchPath = ($event.target as HTMLInputElement).value" data-test="input-postgres-search-path" class="h-8 text-[13px]" placeholder="public" autocapitalize="off" autocorrect="off" autocomplete="off" spellcheck="false" />
              <span class="hint text-xs text-muted-foreground">留空时使用 PostgreSQL 默认搜索路径</span>
            </div>
            <div class="field flex flex-col gap-1.5">
              <Label class="text-xs font-medium text-muted-foreground">连接超时(ms)</Label>
              <Input :value="form.pgConnectTimeoutMs" @input="form.pgConnectTimeoutMs = Number(($event.target as HTMLInputElement).value)" data-test="input-postgres-timeout" class="h-8 text-[13px]" type="number" min="1000" />
            </div>
            <div class="field flex flex-col gap-1.5">
              <Label class="text-xs font-medium text-muted-foreground">TLS</Label>
              <select v-model="form.pgTlsMode" :class="SELECT_CLS" data-test="input-postgres-tls-mode">
                <option value="disable">禁用</option>
                <option value="require">启用（不校验证书）</option>
                <option value="verify-ca">启用（校验证书颁发方）</option>
                <option value="verify-full">启用（校验证书与主机名）</option>
              </select>
              <p class="hint text-xs text-muted-foreground" data-test="postgres-tls-hint">自建 PostgreSQL 默认禁用即可;托管服务通常需选择启用</p>
            </div>
          </template>
          <template v-else-if="form.connType === 'hive'">
            <div class="field flex flex-col gap-1.5">
              <Label class="text-xs font-medium text-muted-foreground">主机 <span class="req text-destructive">*</span></Label>
              <Input :value="form.hiveHost" @input="form.hiveHost = ($event.target as HTMLInputElement).value" data-test="input-hive-host" class="h-8 text-[13px]" placeholder="127.0.0.1" autocapitalize="off" autocorrect="off" autocomplete="off" spellcheck="false" />
              <span v-if="hiveHostInvalid" class="err text-xs text-destructive">主机不能为空</span>
            </div>
            <div class="field flex flex-col gap-1.5">
              <Label class="text-xs font-medium text-muted-foreground">端口</Label>
              <Input :value="form.hivePort" @input="form.hivePort = Number(($event.target as HTMLInputElement).value)" data-test="input-hive-port" class="h-8 text-[13px]" type="number" min="1" max="65535" />
              <span v-if="hivePortInvalid" class="err text-xs text-destructive">端口需为 1-65535</span>
              <span class="hint text-xs text-muted-foreground" data-test="hive-port-hint">HiveServer2 默认 10000</span>
            </div>
            <div class="field flex flex-col gap-1.5">
              <Label class="text-xs font-medium text-muted-foreground">认证方式</Label>
              <select v-model="form.hiveAuthMode" :class="SELECT_CLS" data-test="hive-auth-mode">
                <option value="nosasl">无认证</option>
                <option value="ldap">LDAP</option>
                <option value="kerberos">Kerberos</option>
              </select>
            </div>
            <div class="field flex flex-col gap-1.5">
              <Label class="text-xs font-medium text-muted-foreground">默认库(可选)</Label>
              <Input :value="form.hiveDatabase" @input="form.hiveDatabase = ($event.target as HTMLInputElement).value" data-test="hive-database" class="h-8 text-[13px]" placeholder="留空 = default" autocapitalize="off" autocorrect="off" autocomplete="off" spellcheck="false" />
            </div>
            <template v-if="form.hiveAuthMode === 'ldap'">
              <div class="field flex flex-col gap-1.5">
                <Label class="text-xs font-medium text-muted-foreground">用户名 <span class="req text-destructive">*</span></Label>
                <Input :value="form.hiveUsername" @input="form.hiveUsername = ($event.target as HTMLInputElement).value" data-test="hive-username" class="h-8 text-[13px]" autocapitalize="off" autocorrect="off" autocomplete="off" spellcheck="false" />
                <span v-if="hiveUsernameInvalid" class="err text-xs text-destructive" data-test="hive-username-err">LDAP 认证需填写用户名</span>
              </div>
              <div class="field flex flex-col gap-1.5">
                <Label class="text-xs font-medium text-muted-foreground">密码</Label>
                <PasswordInput v-model="form.hivePassword" data-test="hive-password" toggle-testid="toggle-password-hive" :placeholder="secretPlaceholder" />
              </div>
            </template>
            <template v-else-if="form.hiveAuthMode === 'kerberos'">
              <div class="field flex flex-col gap-1.5">
                <Label class="text-xs font-medium text-muted-foreground">Principal <span class="req text-destructive">*</span></Label>
                <Input :value="form.hivePrincipal" @input="form.hivePrincipal = ($event.target as HTMLInputElement).value" data-test="hive-kerberos-principal" class="h-8 text-[13px]" placeholder="hive/_HOST@EXAMPLE.COM" autocapitalize="off" autocorrect="off" autocomplete="off" spellcheck="false" />
                <span v-if="hivePrincipalInvalid" class="err text-xs text-destructive" data-test="hive-kerberos-principal-err">Kerberos 认证需填写 Principal</span>
              </div>
              <div class="field flex flex-col gap-1.5">
                <Label class="text-xs font-medium text-muted-foreground">keytab 路径 <span class="req text-destructive">*</span></Label>
                <Input :value="form.hiveKeytab" @input="form.hiveKeytab = ($event.target as HTMLInputElement).value" data-test="hive-kerberos-keytab" class="h-8 text-[13px]" placeholder="/etc/security/keytabs/hive.keytab" autocapitalize="off" autocorrect="off" autocomplete="off" spellcheck="false" />
                <span v-if="hiveKeytabInvalid" class="err text-xs text-destructive" data-test="hive-kerberos-keytab-err">Kerberos 认证需填写 keytab 路径</span>
              </div>
              <div class="field col-span-2 flex flex-col gap-1.5">
                <Label class="text-xs font-medium text-muted-foreground">krb5.conf 路径(可选)</Label>
                <Input :value="form.hiveKrb5Conf" @input="form.hiveKrb5Conf = ($event.target as HTMLInputElement).value" data-test="hive-kerberos-krb5-conf" class="h-8 text-[13px]" placeholder="/etc/krb5.conf" autocapitalize="off" autocorrect="off" autocomplete="off" spellcheck="false" />
              </div>
            </template>
          </template>
          <template v-else-if="form.connType === 'es'">
            <div class="field col-span-2 flex flex-col gap-1.5">
              <Label class="text-xs font-medium text-muted-foreground">节点地址 <span class="req text-destructive">*</span>(多节点逗号分隔)</Label>
              <Input :value="form.esHosts" @input="form.esHosts = ($event.target as HTMLInputElement).value" data-test="input-es-hosts" class="h-8 text-[13px]" placeholder="127.0.0.1:9200" autocapitalize="off" autocorrect="off" autocomplete="off" spellcheck="false" />
              <span class="hint text-xs text-muted-foreground">地址自带端口;无协议前缀时按 http 处理,可写 http://host:9200</span>
              <span v-if="esHostsInvalid" class="err text-xs text-destructive">至少填写一个节点</span>
            </div>
            <div class="field flex flex-col gap-1.5">
              <Label class="text-xs font-medium text-muted-foreground">认证方式</Label>
              <select v-model="form.esAuthMode" :class="SELECT_CLS" data-test="input-es-auth-mode">
                <option value="none">无认证</option>
                <option value="basic">用户名 / 密码</option>
                <option value="apikey">API Key</option>
              </select>
            </div>
            <div class="field flex flex-col gap-1.5">
              <Label class="text-xs font-medium text-muted-foreground">TLS</Label>
              <select v-model="form.esTlsMode" :class="SELECT_CLS" data-test="input-es-tls-mode">
                <option value="disabled">禁用</option>
                <option value="skip-verify">启用（跳过证书校验）</option>
                <option value="verify-full">启用（校验证书）</option>
              </select>
              <p class="hint text-xs text-muted-foreground" data-test="es-tls-hint">自建 ES 默认禁用即可;启用后按 https 访问上方地址</p>
            </div>
            <template v-if="form.esAuthMode === 'basic'">
              <div class="field flex flex-col gap-1.5">
                <Label class="text-xs font-medium text-muted-foreground">用户名</Label>
                <Input :value="form.esUsername" @input="form.esUsername = ($event.target as HTMLInputElement).value" data-test="input-es-username" class="h-8 text-[13px]" autocapitalize="off" autocorrect="off" autocomplete="off" spellcheck="false" />
              </div>
              <div class="field flex flex-col gap-1.5">
                <Label class="text-xs font-medium text-muted-foreground">密码</Label>
                <PasswordInput v-model="form.esPassword" data-test="input-es-password" toggle-testid="toggle-password-es" :placeholder="secretPlaceholder" />
              </div>
            </template>
            <template v-else-if="form.esAuthMode === 'apikey'">
              <div class="field col-span-2 flex flex-col gap-1.5">
                <Label class="text-xs font-medium text-muted-foreground">API Key(Base64「id:api_key」或编码凭据)</Label>
                <Input :value="form.esApiKey" @input="form.esApiKey = ($event.target as HTMLInputElement).value" data-test="input-es-api-key" class="h-8 text-[13px]" :placeholder="secretPlaceholder" autocapitalize="off" autocorrect="off" autocomplete="off" spellcheck="false" />
              </div>
            </template>
          </template>
          <template v-else>
          <div class="field col-span-2 flex flex-col gap-1.5">
            <Label class="text-xs font-medium text-muted-foreground">bootstrap.servers <span class="req text-destructive">*</span></Label>
            <Input :value="form.brokers" @input="form.brokers = ($event.target as HTMLInputElement).value" data-test="input-brokers" class="h-8 text-[13px]" placeholder="localhost:9092,broker2:9092" autocapitalize="off" autocorrect="off" autocomplete="off" spellcheck="false" />
            <span v-if="brokersInvalid" class="err text-xs text-destructive">至少填写一个 broker</span>
          </div>
          <div class="field flex flex-col gap-1.5" :class="isSasl ? 'col-span-1' : 'col-span-2'">
            <Label class="text-xs font-medium text-muted-foreground">安全协议</Label>
            <select v-model="form.securityProtocol" :class="SELECT_CLS" data-test="input-security-protocol">
              <option v-for="p in SECURITY_PROTOCOLS" :key="p" :value="p">{{ p }}</option>
            </select>
          </div>
          <template v-if="isSasl">
            <div class="field flex flex-col gap-1.5">
              <Label class="text-xs font-medium text-muted-foreground">认证方式</Label>
              <select v-model="form.mechanism" :class="SELECT_CLS" data-test="input-mechanism">
                <option value="PLAIN">PLAIN</option>
                <option value="SCRAM-SHA-256">SCRAM-SHA-256</option>
                <option value="SCRAM-SHA-512">SCRAM-SHA-512</option>
                <option value="GSSAPI">GSSAPI（Kerberos）</option>
              </select>
            </div>
            <template v-if="!isGssapi">
              <div class="field flex flex-col gap-1.5">
                <Label class="text-xs font-medium text-muted-foreground">用户名</Label>
                <Input :value="form.username" @input="form.username = ($event.target as HTMLInputElement).value" data-test="input-username" class="h-8 text-[13px]" autocapitalize="off" autocorrect="off" autocomplete="off" spellcheck="false" />
              </div>
              <div class="field flex flex-col gap-1.5">
                <Label class="text-xs font-medium text-muted-foreground">密码</Label>
                <PasswordInput v-model="form.password" data-test="input-password" toggle-testid="toggle-password-kafka" :placeholder="secretPlaceholder" />
              </div>
            </template>
            <template v-else>
              <div class="field flex flex-col gap-1.5">
                <Label class="text-xs font-medium text-muted-foreground">Kerberos Principal（须含 @REALM）</Label>
                <Input :value="form.principal" @input="form.principal = ($event.target as HTMLInputElement).value" data-test="input-principal" class="h-8 text-[13px]" placeholder="admin/admin@YHSJ.COM" autocapitalize="off" autocorrect="off" autocomplete="off" spellcheck="false" />
              </div>
              <div class="field flex flex-col gap-1.5">
                <Label class="text-xs font-medium text-muted-foreground">keytab 路径</Label>
                <Input :value="form.keytabPath" @input="form.keytabPath = ($event.target as HTMLInputElement).value" data-test="input-keytab" class="h-8 text-[13px]" placeholder="/etc/security/keytabs/admin.keytab" autocapitalize="off" autocorrect="off" autocomplete="off" spellcheck="false" />
              </div>
              <div class="field flex flex-col gap-1.5">
                <Label class="text-xs font-medium text-muted-foreground">krb5.conf 路径</Label>
                <Input :value="form.krb5ConfPath" @input="form.krb5ConfPath = ($event.target as HTMLInputElement).value" data-test="input-krb5" class="h-8 text-[13px]" placeholder="/etc/krb5.conf" autocapitalize="off" autocorrect="off" autocomplete="off" spellcheck="false" />
              </div>
              <div class="field flex flex-col gap-1.5">
                <Label class="text-xs font-medium text-muted-foreground">服务名（service principal 首段）</Label>
                <Input :value="form.serviceName" @input="form.serviceName = ($event.target as HTMLInputElement).value" data-test="input-service-name" class="h-8 text-[13px]" placeholder="kafka" autocapitalize="off" autocorrect="off" autocomplete="off" spellcheck="false" />
              </div>
            </template>
          </template>
          <template v-if="isTLS">
            <div class="field col-span-2 flex flex-col gap-1.5">
              <Label class="text-xs font-medium text-muted-foreground">CA 证书（可选）</Label>
              <Textarea :value="form.caCert" @input="form.caCert = ($event.target as HTMLTextAreaElement).value" data-test="input-ca" class="min-h-16 resize-y text-[13px]" rows="3" />
            </div>
            <div class="field col-span-2 flex items-center">
              <label class="checkbox-label flex items-center gap-2 text-[13px]">
                <input v-model="form.insecureSkipVerify" type="checkbox" :class="CHECK_CLS" data-test="input-insecure" />
                跳过证书校验
              </label>
            </div>
          </template>
          </template>

          <div v-if="tested" class="msg ok col-span-2 rounded-md bg-success/10 px-2.5 py-2 text-[13px] text-success" data-test="test-ok">连接测试成功</div>
          <div v-if="testError" class="msg err col-span-2 rounded-md bg-destructive/10 px-2.5 py-2 text-[13px] text-destructive" data-test="test-error">{{ testError }}</div>
          <div v-if="saveError" class="msg err col-span-2 rounded-md bg-destructive/10 px-2.5 py-2 text-[13px] text-destructive" data-test="save-error">{{ saveError }}</div>
        </div>
        <DialogFooter class="modal-footer flex-row items-center justify-end gap-2 border-t border-border px-4 py-3">
          <Button v-if="editing" variant="ghost" size="sm" class="delete-entry mr-auto text-destructive hover:bg-destructive/10 hover:text-destructive" type="button" data-test="btn-delete-connection" @click="askDeleteConnection">
            删除连接
          </Button>
          <Button variant="outline" size="sm" type="button" data-test="btn-test" :disabled="testing || targetInvalid" @click="runTest">
            <Loader2 v-if="testing" class="size-4 animate-spin" aria-hidden="true" />
            {{ testing ? '测试中…' : '测试连接' }}
          </Button>
          <Button variant="ghost" size="sm" type="button" @click="close">取消</Button>
          <Button size="sm" type="button" data-test="btn-save" :disabled="nameInvalid || saveInvalid" @click="save">
            保存
          </Button>
        </DialogFooter>
      </div>
      <!-- 删除确认层:结构与 ConfirmDialog 同构,absolute 覆盖 DialogContent
           的视口盒(面板无 transform,定位不受影响),自建以叠在弹窗之上。 -->
      <div v-if="deleteConfirmOpen" class="delete-confirm-backdrop absolute inset-0 z-[1200] grid place-items-center bg-black/50 p-4" data-test="delete-confirm-dialog" @click.self="cancelDeleteConnection">
        <div class="delete-confirm w-[380px] max-w-[calc(100vw-48px)] rounded-xl border border-border bg-background p-4.5 shadow-lg">
          <div class="delete-confirm-title mb-3 text-sm font-semibold">确认操作</div>
          <p class="delete-confirm-message m-0 text-[13px] leading-relaxed" data-test="delete-confirm-message">
            确认删除连接「{{ props.connection?.name ?? '' }}」？连接配置将被移除，此操作不可恢复。
          </p>
          <div v-if="deleteError" class="msg err mt-2 rounded-md bg-destructive/10 px-2.5 py-2 text-xs text-destructive" data-test="delete-error">{{ deleteError }}</div>
          <div class="delete-confirm-footer mt-4 flex justify-end gap-2">
            <Button variant="outline" size="sm" type="button" data-test="delete-confirm-cancel" @click="cancelDeleteConnection">取消</Button>
            <Button variant="destructive" size="sm" type="button" data-test="delete-confirm-ok" :disabled="deleting" @click="confirmDeleteConnection">删除</Button>
          </div>
        </div>
      </div>
    </DialogContent>
  </DialogRoot>
</template>
