<script setup lang="ts">
import { ref, onMounted, watch } from 'vue'
import { getApi } from '@/api/client'
import type { AuditEntry, DriverInfo } from '@/api/types'
import { formatTime } from '@/utils/format'
import { DEFAULT_QUERY_DIR, getQueryDir, setQueryDir } from '@/utils/queryDir'
import { APP_VERSION } from '@/version'
import { useToastStore } from '@/store/toast'

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
  <div v-if="show" class="modal-backdrop" data-test="settings-panel">
    <div class="modal">
      <div class="modal-header">
        <span class="modal-title">设置</span>
        <button class="modal-close" type="button" data-test="modal-close" @click="close">✕</button>
      </div>
      <div class="modal-body">
        <nav class="settings-nav" data-test="settings-nav">
          <button
            v-for="t in TABS"
            :key="t.id"
            class="nav-item"
            :class="{ active: activeTab === t.id }"
            type="button"
            :data-test="`settings-tab-${t.id}`"
            @click="activeTab = t.id"
          >
            <span class="nav-icon" aria-hidden="true">{{ t.icon }}</span>
            <span class="nav-label">{{ t.label }}</span>
          </button>
        </nav>
        <div class="settings-content" data-test="settings-tab-content">
          <div v-if="activeTab === 'generic'" class="tab-pane">
            <div class="field">
              <label class="label">主题</label>
              <select v-model="theme" data-test="select-theme" class="input" @change="changeTheme">
                <option value="pink">少女粉</option>
                <option value="dark">深色</option>
                <option value="light">浅色</option>
              </select>
            </div>
            <div class="field query-dir-section" data-test="query-dir-section">
              <label class="label" for="input-query-dir">查询文件目录</label>
              <div class="query-dir-row">
                <input
                  id="input-query-dir"
                  v-model="queryDir"
                  class="input"
                  type="text"
                  data-test="input-query-dir"
                  placeholder="~/.db-client/queries"
                />
                <button class="query-dir-reset" type="button" data-test="btn-query-dir-reset" @click="resetQueryDir">恢复默认</button>
              </div>
              <p class="query-dir-hint">SQL 控制台的保存查询将存放为 .sql 文件</p>
            </div>
          </div>

          <div v-else-if="activeTab === 'audit'" class="tab-pane audit-section" data-test="audit-section">
            <div class="audit-header">
              <span class="audit-title">操作审计</span>
              <button class="audit-refresh" type="button" data-test="audit-refresh" @click="loadAudit">刷新</button>
            </div>
            <div v-if="auditLoading" class="audit-state" data-test="audit-loading">加载中…</div>
            <div v-else-if="auditError" class="audit-state err" data-test="audit-error">{{ auditError }}</div>
            <div v-else-if="audit.length === 0" class="audit-state" data-test="audit-empty">暂无操作记录</div>
            <table v-else class="audit-table" data-test="audit-table">
              <thead>
                <tr>
                  <th>时间</th>
                  <th>动作</th>
                  <th>对象</th>
                  <th>结果</th>
                  <th>详情</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="e in audit" :key="e.id ?? e.timestamp" class="audit-row" data-test="audit-row">
                  <td class="mono" data-test="audit-time">{{ formatTime(e.timestamp) }}</td>
                  <td>{{ actionLabel(e.action) }}</td>
                  <td class="mono">{{ e.target }}</td>
                  <td :class="e.result === 'ok' ? 'result-ok' : 'result-err'">{{ e.result === 'ok' ? '成功' : '失败' }}</td>
                  <td class="detail">{{ e.detail || '-' }}</td>
                </tr>
              </tbody>
            </table>
          </div>

          <div v-else-if="activeTab === 'drivers'" class="tab-pane drivers-section" data-test="drivers-section">
            <div class="audit-header">
              <span class="audit-title">驱动管理</span>
            </div>
            <p class="drivers-hint" data-test="drivers-hint">驱动为内置原生实现,无需外部路径</p>
            <table class="audit-table drivers-table" data-test="drivers-table">
              <thead>
                <tr>
                  <th>名称</th>
                  <th>驱动库</th>
                  <th>版本</th>
                  <th>默认端口</th>
                  <th>说明</th>
                  <th>外部路径</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="d in drivers" :key="d.name" class="audit-row" data-test="driver-row">
                  <td data-test="driver-name">{{ d.name }}</td>
                  <td class="mono" data-test="driver-library">{{ d.library }}</td>
                  <td class="mono" data-test="driver-version">{{ d.version }}</td>
                  <td class="mono" data-test="driver-port">{{ d.default_port }}</td>
                  <td class="detail" data-test="driver-desc">{{ d.description }}</td>
                  <td>
                    <input class="input driver-path" type="text" data-test="driver-path" :placeholder="'外部驱动路径(预留)'" disabled :data-driver="d.name" />
                  </td>
                </tr>
              </tbody>
            </table>
          </div>

          <div v-else class="tab-pane about-section">
            <p class="about-intro" data-test="about-intro">多数据源数据库管理客户端 · 支持 Kafka / MySQL / TiDB / PostgreSQL / Hive / ClickHouse / Elasticsearch / Redis</p>
            <div class="about-row"><span class="about-label">版本</span><span class="about-value mono" data-test="about-version">{{ APP_VERSION }}</span></div>
            <div class="about-row"><span class="about-label">作者</span><span class="about-value" data-test="about-author">By Mr Zp</span></div>
            <div class="about-actions">
              <button class="btn-check" type="button" data-test="btn-about-check" :disabled="checkingUpdate" @click="checkForUpdate">
                {{ checkingUpdate ? '检查中…' : '检查更新' }}
              </button>
              <span class="about-hint">将在 Gitee 检查最新版本</span>
            </div>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
/* 遮罩/容器基础样式走全局 .modal-backdrop/.modal 基座,这里只保留尺寸。 */
.modal {
  width: 680px; height: 500px; max-width: 94vw; max-height: 90vh;
}
.modal-header { display: flex; justify-content: space-between; align-items: center; padding: 16px 18px; border-bottom: 1px solid var(--border); }
.modal-title { font-weight: 600; font-size: 15px; }
.modal-close { background: none; border: none; color: var(--text-tertiary); font-size: 16px; cursor: pointer; border-radius: 6px; padding: 1px 6px; }
.modal-close:hover { background: var(--bg-hover); color: var(--text); }
.modal-body { flex: 1; min-height: 0; display: flex; }

/* 左侧竖排导航 */
.settings-nav { width: 170px; flex: none; display: flex; flex-direction: column; gap: 2px; padding: 12px 10px; border-right: 1px solid var(--border); background: var(--bg-subtle); overflow-y: auto; }
.nav-item { display: flex; align-items: center; gap: 8px; width: 100%; text-align: left; border: none; background: transparent; color: var(--text); font-size: 13px; font-family: var(--font); padding: 8px 10px; border-radius: 7px; cursor: pointer; transition: background 0.15s ease; }
.nav-item:hover { background: var(--bg-hover); }
.nav-item.active { background: var(--accent-soft); color: var(--accent); font-weight: 600; }
.nav-icon { flex: none; }

/* 右侧内容区:占余宽、独立滚动 */
.settings-content { flex: 1; min-width: 0; overflow-y: auto; padding: 16px 18px; }

.field { margin-bottom: 14px; }
.label { display: block; font-size: 12px; color: var(--text-secondary); margin-bottom: 4px; }
.input { background: var(--bg-subtle); border: 1px solid var(--border); color: var(--text); border-radius: 7px; padding: 8px 10px; font-size: 13px; width: 100%; box-sizing: border-box; transition: border-color 0.15s ease, background 0.15s ease, box-shadow 0.15s ease; }
.input:focus { outline: none; border-color: var(--accent); background: var(--bg-elevated); box-shadow: var(--focus-ring); }
.mono { font-family: var(--mono); }

/* 查询文件目录 */
.query-dir-row { display: flex; gap: 8px; }
.query-dir-row .input { flex: 1; }
.query-dir-reset, .audit-refresh { background: var(--bg-subtle); border: 1px solid var(--border); color: var(--text-secondary); font-size: 12px; border-radius: 6px; padding: 3px 10px; cursor: pointer; transition: border-color 0.15s ease, color 0.15s ease; }
.query-dir-reset:hover, .audit-refresh:hover { border-color: var(--accent); color: var(--text); }
.query-dir-hint { margin: 4px 0 0; font-size: 12px; color: var(--text-tertiary); }

.audit-section { padding-bottom: 12px; }
.audit-header { display: flex; justify-content: space-between; align-items: center; margin-bottom: 8px; }
.audit-title { font-weight: 600; font-size: 13px; }
.audit-refresh { background: var(--bg-subtle); border: 1px solid var(--border); color: var(--text-secondary); font-size: 12px; border-radius: 6px; padding: 3px 10px; cursor: pointer; transition: border-color 0.15s ease, color 0.15s ease; }
.audit-refresh:hover { border-color: var(--accent); color: var(--text); }
.audit-state { color: var(--text-secondary); font-size: 12px; padding: 8px 0; }
.audit-state.err { color: var(--danger); }
.audit-table { width: 100%; border-collapse: collapse; font-size: 12px; max-height: 300px; overflow-y: auto; display: block; }
.audit-table th { position: sticky; top: 0; background: var(--bg-elevated); text-align: left; font-weight: 600; color: var(--text-secondary); padding: 5px 8px; border-bottom: 1px solid var(--border); white-space: nowrap; }
.audit-table td { padding: 5px 8px; border-bottom: 1px solid var(--border); color: var(--text); vertical-align: top; white-space: nowrap; }
.audit-table td.detail { white-space: normal; min-width: 120px; word-break: break-all; }
.audit-table tr:last-child td { border-bottom: none; }
.audit-table tr:hover td { background: var(--bg-hover); }
.result-ok { color: var(--ok); }
.result-err { color: var(--danger); }

.drivers-section { padding-bottom: 12px; }
.drivers-hint { margin: 0 0 8px; font-size: 12px; color: var(--text-tertiary); }
.drivers-table { display: table; max-height: none; overflow: visible; }
.driver-path { width: 160px; padding: 4px 8px; font-size: 12px; opacity: 0.6; cursor: not-allowed; }

/* 关于 */
.about-row { display: flex; justify-content: space-between; align-items: center; font-size: 13px; padding: 8px 0; border-bottom: 1px solid var(--border); }
.about-label { color: var(--text-secondary); }
.about-actions { display: flex; align-items: center; gap: 10px; margin-top: 16px; }
.btn-check { background: var(--primary-gradient); color: var(--on-primary); border: none; border-radius: 7px; padding: 7px 14px; font-size: 13px; font-family: var(--font); cursor: pointer; transition: filter 0.15s ease; }
.btn-check:hover:not(:disabled) { filter: brightness(0.95); }
.btn-check:disabled { opacity: 0.5; cursor: not-allowed; }
.about-hint { font-size: 12px; color: var(--text-tertiary); }

.about-intro {
  margin: 0 0 12px;
  font-size: 12px;
  color: var(--text-secondary);
  line-height: 1.6;
}
</style>
