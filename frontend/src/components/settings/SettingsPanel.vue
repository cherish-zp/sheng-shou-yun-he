<script setup lang="ts">
import { ref, onMounted, watch } from 'vue'
import { getApi } from '@/api/client'
import type { AuditEntry, DriverInfo } from '@/api/types'
import { formatTime } from '@/utils/format'
import { DEFAULT_QUERY_DIR, getQueryDir, setQueryDir } from '@/utils/queryDir'
import { APP_VERSION } from '@/version'
import { useToastStore } from '@/store/toast'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'

const props = defineProps<{ show: boolean }>()
const emit = defineEmits<{ (e: 'close'): void; (e: 'check-update'): void }>()

// 左侧导航(macOS 系统设置风格):默认进入「通用」。
type SettingsTab = 'generic' | 'audit' | 'drivers' | 'about'
const TABS: { id: SettingsTab; icon: string; label: string }[] = [
  { id: 'generic', icon: '⚙️', label: '通用' },
  { id: 'audit', icon: '📜', label: '审计日志' },
  { id: 'drivers', icon: '🧩', label: '驱动管理' },
  { id: 'about', icon: 'ℹ️', label: '关于' },
]
const activeTab = ref<SettingsTab>('generic')

const THEME_KEY = 'dbclient-theme'
type ThemeName = 'dark' | 'light' | 'pink'
// localStorage 无值或值非法时默认「少女粉」;已有存储值(dark/light)尊重不动。
function normalizeTheme(v: string | null): ThemeName {
  return v === 'dark' || v === 'light' ? v : 'pink'
}
const theme = ref<ThemeName>(normalizeTheme(localStorage.getItem(THEME_KEY)))

// 查询文件目录:初始读 localStorage(空值回退默认),输入变更即持久化。
const queryDir = ref(getQueryDir())
watch(queryDir, (dir) => setQueryDir(dir))

function resetQueryDir(): void {
  queryDir.value = DEFAULT_QUERY_DIR
}

const AUDIT_LIMIT = 200
const audit = ref<AuditEntry[]>([])
const auditLoading = ref(false)
const auditError = ref<string | null>(null)

// 驱动管理:优先读后端 ListDrivers,后端绑定未生成/失败时回退本地常量。
const FALLBACK_DRIVERS: DriverInfo[] = [
  { name: 'Kafka', library: 'franz-go', version: 'v1.21.6', default_port: 9092, description: 'Kafka 集群管理与消息收发(原生 TCP 协议)' },
  { name: 'MySQL', library: 'go-sql-driver/mysql', version: 'v1.10.1', default_port: 3306, description: 'MySQL 库表浏览、分页查询与 SQL 控制台(原生 TCP 协议)' },
  { name: 'TiDB', library: 'go-sql-driver/mysql', version: 'v1.10.1', default_port: 4000, description: 'TiDB 库表浏览、分页查询与 SQL 控制台(兼容 MySQL 协议)' },
  { name: 'PostgreSQL', library: 'pgx', version: 'v5.11.0', default_port: 5432, description: 'PostgreSQL 库/模式浏览、分页查询与 SQL 控制台(原生 TCP 协议)' },
  { name: 'Hive', library: 'gohive', version: 'v1.6.0', default_port: 10000, description: 'HiveServer2 原生 Thrift 客户端,支持 NOSASL/LDAP/Kerberos' },
  { name: 'ClickHouse', library: 'clickhouse-go', version: 'v2.48.0', default_port: 9000, description: 'ClickHouse 库表浏览、分页查询与 SQL 控制台(原生 TCP 协议)' },
  { name: 'Elasticsearch', library: 'net/http + encoding/json', version: 'v8.x', default_port: 9200, description: 'Elasticsearch/OpenSearch 索引浏览、文档编辑与 SQL 控制台(REST 协议)' },
  { name: 'Redis', library: 'go-redis', version: 'v9.22.0', default_port: 6379, description: 'Redis 单机/集群键空间浏览与编辑' },
]
const drivers = ref<DriverInfo[]>([])
const driversError = ref(false)

async function loadDrivers(): Promise<void> {
  driversError.value = false
  try {
    drivers.value = await getApi().listDrivers()
  } catch {
    drivers.value = FALLBACK_DRIVERS
    driversError.value = true
  }
}

// ACTION_LABELS maps the backend audit action codes to Chinese labels; unknown
// actions fall back to their raw code so the trail stays readable.
const ACTION_LABELS: Record<string, string> = {
  create_connection: '新建连接',
  delete_connection: '删除连接',
  create_topic: '新建 Topic',
  delete_topic: '删除 Topic',
  delete_topics: '批量删除 Topic',
  delete_consumer_group: '删除消费组',
  alter_topic_config: '修改 Topic 配置',
  reset_group_offset: '重置消费组位移',
}

function actionLabel(action: string): string {
  return ACTION_LABELS[action] ?? action
}

function apply(name: ThemeName): void {
  document.documentElement.setAttribute('data-theme', name)
  // shadcn/Tailwind 体系按 .dark 类切换暗色(与 DBX 一致);data-theme
  // 继续服务旧手写令牌(迁移期共存)。
  document.documentElement.classList.toggle('dark', name === 'dark')
  localStorage.setItem(THEME_KEY, name)
}

function changeTheme(): void {
  apply(theme.value)
}

// loadAudit fetches the most recent operation audit trail. Failures surface in
// an error banner rather than silently hiding the section.
async function loadAudit(): Promise<void> {
  auditLoading.value = true
  auditError.value = null
  try {
    audit.value = await getApi().listAudit(AUDIT_LIMIT)
  } catch (e) {
    audit.value = []
    auditError.value = e instanceof Error ? e.message : String(e)
  } finally {
    auditLoading.value = false
  }
}

onMounted(() => {
  apply(theme.value)
  if (props.show) {
    void loadAudit()
    void loadDrivers()
  }
})

// Load the audit trail whenever the settings modal is opened so it reflects
// any operations performed since the last visit.
watch(
  () => props.show,
  (visible) => {
    if (visible) {
      void loadAudit()
      void loadDrivers()
    }
  },
)

function close(): void {
  emit('close')
}

// --- 关于:手动检查更新 ---------------------------------------------------------

// toast:与全局轻提示同一 store(Layout 底部居中渲染),检查结果就地反馈。
const toast = useToastStore()
const checkingUpdate = ref(false)

// checkForUpdate 手动检查:发现新版本才 emit 给 Layout 打开 UpdateDialog
// (弹窗内会重新探测并接管下载/安装流程);已是最新就地 toast 告知;手动检查
// 不受「忽略此版本」限制——用户主动检查就应看到结果。
async function checkForUpdate(): Promise<void> {
  if (checkingUpdate.value) return
  checkingUpdate.value = true
  try {
    const res = await getApi().checkUpdate({ current_version: APP_VERSION })
    if (res.has_update) {
      emit('check-update')
    } else {
      toast.show(`当前已是最新版本 ${res.latest_version}`)
    }
  } catch (e) {
    toast.show(`检查更新失败: ${e instanceof Error ? e.message : String(e)}`)
  } finally {
    checkingUpdate.value = false
  }
}
</script>

<template>
  <!-- 项目规范:遮罩点击不关闭,仅右上角 ✕ 关闭。 -->
  <div v-if="show" class="fixed inset-0 z-[900] flex items-center justify-center bg-black/50" data-test="settings-panel">
    <div class="flex h-[500px] w-[680px] max-h-[90vh] max-w-[94vw] flex-col overflow-hidden rounded-lg border border-border bg-background text-foreground shadow-lg">
      <div class="flex items-center justify-between border-b border-border px-[18px] py-4">
        <span class="text-[15px] font-semibold">设置</span>
        <Button variant="ghost" size="sm" class="h-6 w-6 p-0 text-muted-foreground" data-test="modal-close" @click="close">✕</Button>
      </div>
      <div class="flex min-h-0 flex-1">
        <nav class="flex w-[170px] flex-none flex-col gap-0.5 overflow-y-auto border-r border-border bg-muted/50 px-2.5 py-3" data-test="settings-nav">
          <button
            v-for="t in TABS"
            :key="t.id"
            class="flex w-full items-center gap-2 rounded-md px-2.5 py-2 text-left text-[13px]"
            :class="activeTab === t.id ? 'bg-accent font-semibold text-accent-foreground' : 'text-foreground hover:bg-muted'"
            type="button"
            :data-test="`settings-tab-${t.id}`"
            @click="activeTab = t.id"
          >
            <span aria-hidden="true">{{ t.icon }}</span>
            <span>{{ t.label }}</span>
          </button>
        </nav>
        <div class="min-w-0 flex-1 overflow-y-auto px-[18px] py-4" data-test="settings-tab-content">
          <div v-if="activeTab === 'generic'" class="tab-pane">
            <div class="mb-3.5">
              <Label class="mb-1 text-xs font-normal text-muted-foreground">主题</Label>
              <select
                v-model="theme"
                data-test="select-theme"
                class="h-9 w-full rounded-md border border-input bg-transparent px-3 text-[13px] shadow-xs outline-none transition-[color,box-shadow] focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50"
                @change="changeTheme"
              >
                <option value="pink">少女粉</option>
                <option value="dark">深色</option>
                <option value="light">浅色</option>
              </select>
            </div>
            <div class="mb-3.5" data-test="query-dir-section">
              <Label class="mb-1 text-xs font-normal text-muted-foreground" for="input-query-dir">查询文件目录</Label>
              <div class="flex gap-2">
                <Input
                  id="input-query-dir"
                  :value="queryDir"
                  type="text"
                  class="flex-1 text-[13px]"
                  data-test="input-query-dir"
                  placeholder="~/.db-client/queries"
                  @input="queryDir = ($event.target as HTMLInputElement).value"
                />
                <Button variant="outline" size="sm" class="h-9 text-xs" data-test="btn-query-dir-reset" @click="resetQueryDir">恢复默认</Button>
              </div>
              <p class="mt-1 text-xs text-muted-foreground/70">SQL 控制台的保存查询将存放为 .sql 文件</p>
            </div>
          </div>

          <div v-else-if="activeTab === 'audit'" class="pb-3" data-test="audit-section">
            <div class="mb-2 flex items-center justify-between">
              <span class="text-[13px] font-semibold">操作审计</span>
              <Button variant="outline" size="sm" class="h-7 text-xs" data-test="audit-refresh" @click="loadAudit">刷新</Button>
            </div>
            <div v-if="auditLoading" class="py-2 text-xs text-muted-foreground" data-test="audit-loading">加载中…</div>
            <div v-else-if="auditError" class="py-2 text-xs text-destructive" data-test="audit-error">{{ auditError }}</div>
            <div v-else-if="audit.length === 0" class="py-2 text-xs text-muted-foreground" data-test="audit-empty">暂无操作记录</div>
            <table v-else class="block w-full overflow-y-auto text-xs" data-test="audit-table">
              <thead>
                <tr>
                  <th class="sticky top-0 whitespace-nowrap border-b border-border bg-card px-2 py-1.5 text-left font-semibold text-muted-foreground">时间</th>
                  <th class="sticky top-0 whitespace-nowrap border-b border-border bg-card px-2 py-1.5 text-left font-semibold text-muted-foreground">动作</th>
                  <th class="sticky top-0 whitespace-nowrap border-b border-border bg-card px-2 py-1.5 text-left font-semibold text-muted-foreground">对象</th>
                  <th class="sticky top-0 whitespace-nowrap border-b border-border bg-card px-2 py-1.5 text-left font-semibold text-muted-foreground">结果</th>
                  <th class="sticky top-0 whitespace-nowrap border-b border-border bg-card px-2 py-1.5 text-left font-semibold text-muted-foreground">详情</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="e in audit" :key="e.id ?? e.timestamp" class="hover:bg-muted/60" data-test="audit-row">
                  <td class="whitespace-nowrap border-b border-border px-2 py-1.5 align-top font-mono tabular-nums" data-test="audit-time">{{ formatTime(e.timestamp) }}</td>
                  <td class="whitespace-nowrap border-b border-border px-2 py-1.5 align-top">{{ actionLabel(e.action) }}</td>
                  <td class="whitespace-nowrap border-b border-border px-2 py-1.5 align-top font-mono">{{ e.target }}</td>
                  <td class="whitespace-nowrap border-b border-border px-2 py-1.5 align-top" :class="e.result === 'ok' ? 'text-success' : 'text-destructive'">{{ e.result === 'ok' ? '成功' : '失败' }}</td>
                  <td class="min-w-[120px] break-all border-b border-border px-2 py-1.5 align-top">{{ e.detail || '-' }}</td>
                </tr>
              </tbody>
            </table>
          </div>

          <div v-else-if="activeTab === 'drivers'" class="pb-3" data-test="drivers-section">
            <div class="mb-2 flex items-center justify-between">
              <span class="text-[13px] font-semibold">驱动管理</span>
            </div>
            <p class="mb-2 text-xs text-muted-foreground/70" data-test="drivers-hint">驱动为内置原生实现,无需外部路径</p>
            <table class="w-full text-xs" data-test="drivers-table">
              <thead>
                <tr>
                  <th class="whitespace-nowrap border-b border-border px-2 py-1.5 text-left font-semibold text-muted-foreground">名称</th>
                  <th class="whitespace-nowrap border-b border-border px-2 py-1.5 text-left font-semibold text-muted-foreground">驱动库</th>
                  <th class="whitespace-nowrap border-b border-border px-2 py-1.5 text-left font-semibold text-muted-foreground">版本</th>
                  <th class="whitespace-nowrap border-b border-border px-2 py-1.5 text-left font-semibold text-muted-foreground">默认端口</th>
                  <th class="whitespace-nowrap border-b border-border px-2 py-1.5 text-left font-semibold text-muted-foreground">说明</th>
                  <th class="whitespace-nowrap border-b border-border px-2 py-1.5 text-left font-semibold text-muted-foreground">外部路径</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="d in drivers" :key="d.name" class="hover:bg-muted/60" data-test="driver-row">
                  <td class="whitespace-nowrap border-b border-border px-2 py-1.5 align-top" data-test="driver-name">{{ d.name }}</td>
                  <td class="whitespace-nowrap border-b border-border px-2 py-1.5 align-top font-mono" data-test="driver-library">{{ d.library }}</td>
                  <td class="whitespace-nowrap border-b border-border px-2 py-1.5 align-top font-mono" data-test="driver-version">{{ d.version }}</td>
                  <td class="whitespace-nowrap border-b border-border px-2 py-1.5 align-top font-mono tabular-nums" data-test="driver-port">{{ d.default_port }}</td>
                  <td class="break-all border-b border-border px-2 py-1.5 align-top" data-test="driver-desc">{{ d.description }}</td>
                  <td class="border-b border-border px-2 py-1.5 align-top">
                    <Input class="h-8 w-[160px] text-xs opacity-60" type="text" data-test="driver-path" :placeholder="'外部驱动路径(预留)'" disabled :data-driver="d.name" />
                  </td>
                </tr>
              </tbody>
            </table>
          </div>

          <div v-else class="tab-pane">
            <p class="mb-3 text-xs leading-relaxed text-muted-foreground" data-test="about-intro">多数据源数据库管理客户端 · 支持 Kafka / MySQL / TiDB / PostgreSQL / Hive / ClickHouse / Elasticsearch / Redis</p>
            <div class="flex items-center justify-between border-b border-border py-2 text-[13px]"><span class="text-muted-foreground">版本</span><span class="font-mono" data-test="about-version">{{ APP_VERSION }}</span></div>
            <div class="flex items-center justify-between border-b border-border py-2 text-[13px]"><span class="text-muted-foreground">作者</span><span data-test="about-author">By Mr Zp</span></div>
            <div class="mt-4 flex items-center gap-2.5">
              <Button size="sm" data-test="btn-about-check" :disabled="checkingUpdate" @click="checkForUpdate">
                {{ checkingUpdate ? '检查中…' : '检查更新' }}
              </Button>
              <span class="text-xs text-muted-foreground/70">将在 Gitee 检查最新版本</span>
            </div>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>
