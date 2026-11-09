<script setup lang="ts">
import { computed, onActivated, onBeforeUnmount, onDeactivated, onMounted, ref, watch } from 'vue'
import { getApi } from '@/api/client'
import type { EsClusterStats } from '@/api/types'
import { useTabsStore } from '@/store/tabs'
import { formatBytes } from '@/utils/bytes'
import { Button } from '@/components/ui/button'
import { RefreshCw } from 'lucide-vue-next'

const props = defineProps<{ connectionId: string }>()
const tabs = useTabsStore()

const stats = ref<EsClusterStats | null>(null)
const loading = ref(false)
const error = ref<string | null>(null)
const updated = ref<Date | null>(null)
const auto = ref(true)

// requestSeq 防止快速刷新/轮询下的乱序覆盖:慢响应绝不能覆盖更新的快照,
// 每次拉取自带序号,过期结果在触碰面板状态前丢弃(ClusterHealthPanel 同款)。
let requestSeq = 0

// load 拉取连接的 ES 集群监控快照。失败写入面板内错误区(不弹全局提示);
// 已有数据时失败保留旧数据,成功后错误与数据一并刷新。
async function load(): Promise<void> {
  const seq = ++requestSeq
  loading.value = true
  error.value = null
  try {
    const data = await getApi().esClusterStats?.({ connection_id: props.connectionId })
    if (seq !== requestSeq) return
    if (!data) {
      error.value = '当前客户端暂不支持集群监控,请升级应用'
      return
    }
    stats.value = data
    updated.value = new Date()
  } catch (e) {
    if (seq !== requestSeq) return
    error.value = e instanceof Error ? e.message : String(e)
  } finally {
    if (seq === requestSeq) loading.value = false
  }
}

function refresh(): void {
  void load()
}

// 自动轮询:10 秒一次;开关关闭即停、打开即恢复,卸载清理定时器。
const POLL_MS = 10_000
let timer: number | null = null

function startPolling(): void {
  if (timer !== null) return
  timer = window.setInterval(() => void load(), POLL_MS)
}

function stopPolling(): void {
  if (timer === null) return
  window.clearInterval(timer)
  timer = null
}

watch(auto, (on) => {
  if (on) startPolling()
  else stopPolling()
})

onMounted(() => {
  void load()
  if (auto.value) startPolling()
})

onBeforeUnmount(stopPolling)

// KeepAlive 保活(Layout 内容区缓存 tab 视图):切走 tab 时暂停 10s 轮询、
// 切回恢复,后台 tab 不再持续打 ES 接口;startPolling 幂等,与 mounted 后的
// 首次 activated 重入安全。非 KeepAlive 场景(独立挂载)钩子不触发,行为不变。
onActivated(() => {
  if (auto.value) startPolling()
})
onDeactivated(stopPolling)

// status-card 的 status-green/yellow/red 为 spec hook 类,与语义 token 并存。
const statusClasses = computed(() => {
  const tone =
    stats.value?.status === 'green'
      ? 'border-l-success'
      : stats.value?.status === 'yellow'
        ? 'border-l-warning'
        : 'border-l-destructive'
  const dot =
    stats.value?.status === 'green'
      ? 'bg-success'
      : stats.value?.status === 'yellow'
        ? 'bg-warning'
        : 'bg-destructive'
  return {
    card: [`status-${stats.value?.status ?? ''}`, tone],
    dot: [dot],
  }
})

const statusText = computed(() => {
  switch (stats.value?.status) {
    case 'green':
      return '健康'
    case 'yellow':
      return '部分副本未分配'
    case 'red':
      return '主分片未分配'
    default:
      return stats.value?.status ?? ''
  }
})

const updatedText = computed(() =>
  updated.value ? `最近更新 ${updated.value.toLocaleTimeString('zh-CN', { hour12: false })}` : '尚未更新',
)
</script>

<template>
  <div class="h-full overflow-y-auto px-0.5 py-1 text-foreground" data-test="es-cluster-monitor">
    <div v-if="loading && !stats" class="py-1 text-[13px] text-muted-foreground" data-test="monitor-loading">加载中…</div>
    <template v-else>
      <div v-if="error" class="py-1 text-[13px] text-destructive" data-test="monitor-error">{{ error }}</div>
      <template v-if="stats">
        <div class="flex flex-wrap items-stretch gap-2.5">
          <div
            class="status-card flex min-w-[220px] flex-1 items-center gap-2.5 rounded-lg border border-border border-l-4 bg-card px-3.5 py-3"
            :class="statusClasses.card"
            :data-status="stats.status"
            data-test="monitor-status"
          >
            <span class="size-2.5 shrink-0 rounded-full" :class="statusClasses.dot" aria-hidden="true"></span>
            <div class="flex min-w-0 flex-col gap-0.5">
              <span class="truncate font-mono text-sm font-semibold">{{ stats.cluster_name }}</span>
              <span class="text-xs text-muted-foreground">{{ statusText }}（{{ stats.status }}）</span>
            </div>
          </div>
          <div class="flex flex-none items-center gap-3">
            <span class="text-xs text-muted-foreground" data-test="monitor-updated">{{ updatedText }}</span>
            <label class="inline-flex cursor-pointer items-center gap-1.5 text-xs text-muted-foreground">
              <input v-model="auto" type="checkbox" data-test="monitor-auto" class="cursor-pointer accent-primary" />
              自动刷新
            </label>
            <Button variant="outline" size="sm" class="text-xs" type="button" data-test="monitor-refresh" @click="refresh">
              <RefreshCw />
              刷新
            </Button>
          </div>
        </div>

        <h3 class="mb-2 mt-3.5 text-xs font-semibold uppercase tracking-wider text-muted-foreground">概览</h3>
        <div class="grid grid-cols-6 gap-2" data-test="monitor-tiles">
          <div class="tile flex min-w-0 flex-col gap-0.5 rounded-lg border border-border bg-card px-3 py-2.5" data-test="tile-nodes">
            <span class="text-[11px] text-muted-foreground">节点数</span>
            <span class="truncate font-mono text-[13px] tabular-nums">{{ stats.number_of_nodes }}</span>
            <span class="truncate text-[11px] text-muted-foreground">数据节点 {{ stats.number_of_data_nodes }}</span>
          </div>
          <div class="tile flex min-w-0 flex-col gap-0.5 rounded-lg border border-border bg-card px-3 py-2.5" data-test="tile-indices">
            <span class="text-[11px] text-muted-foreground">索引数</span>
            <span class="truncate font-mono text-[13px] tabular-nums">{{ stats.indices_count }}</span>
          </div>
          <div class="tile flex min-w-0 flex-col gap-0.5 rounded-lg border border-border bg-card px-3 py-2.5" data-test="tile-shards">
            <span class="text-[11px] text-muted-foreground">分片</span>
            <span class="truncate font-mono text-[13px] tabular-nums" data-test="tile-shards-active">{{ stats.active_shards }}</span>
            <span class="truncate font-mono text-[11px] text-muted-foreground" data-test="tile-shards-primary">主分片 {{ stats.active_primary_shards }}</span>
            <span class="truncate text-[11px] text-muted-foreground">迁移 {{ stats.relocating_shards }} · 未分配 {{ stats.unassigned_shards }}</span>
          </div>
          <div class="tile flex min-w-0 flex-col gap-0.5 rounded-lg border border-border bg-card px-3 py-2.5" data-test="tile-docs">
            <span class="text-[11px] text-muted-foreground">文档数</span>
            <span class="truncate font-mono text-[13px] tabular-nums">{{ stats.docs_count }}</span>
          </div>
          <div class="tile flex min-w-0 flex-col gap-0.5 rounded-lg border border-border bg-card px-3 py-2.5" data-test="tile-store">
            <span class="text-[11px] text-muted-foreground">存储大小</span>
            <span class="truncate font-mono text-[13px] tabular-nums">{{ formatBytes(stats.store_size_bytes) }}</span>
          </div>
          <button
            class="tile flex min-w-0 cursor-pointer flex-col gap-0.5 rounded-lg border border-border bg-card px-3 py-2.5 text-left text-foreground transition-colors hover:border-ring hover:bg-accent/50"
            type="button"
            data-test="tile-templates"
            title="打开索引模板管理"
            @click="tabs.openEsTemplates(connectionId)"
          >
            <span class="text-[11px] text-muted-foreground">模板数</span>
            <span class="truncate font-mono text-[13px] tabular-nums">{{ stats.templates_count }}</span>
            <span class="truncate text-[11px] text-muted-foreground">去管理 →</span>
          </button>
        </div>

        <h3 class="mb-2 mt-3.5 text-xs font-semibold uppercase tracking-wider text-muted-foreground">节点</h3>
        <div data-test="monitor-nodes">
          <div v-if="stats.nodes.length === 0" class="py-1 text-[13px] text-muted-foreground" data-test="nodes-empty">无节点信息</div>
          <table v-else class="w-full overflow-hidden rounded-lg border border-border bg-card text-xs">
            <thead>
              <tr>
                <th class="border-b border-border bg-muted/50 px-2.5 py-1.5 text-left text-[11px] font-semibold text-muted-foreground">名称</th>
                <th class="border-b border-border bg-muted/50 px-2.5 py-1.5 text-left text-[11px] font-semibold text-muted-foreground">IP</th>
                <th class="border-b border-border bg-muted/50 px-2.5 py-1.5 text-left text-[11px] font-semibold text-muted-foreground">角色</th>
                <th class="border-b border-border bg-muted/50 px-2.5 py-1.5 text-left text-[11px] font-semibold text-muted-foreground">堆%</th>
                <th class="border-b border-border bg-muted/50 px-2.5 py-1.5 text-left text-[11px] font-semibold text-muted-foreground">磁盘%</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="n in stats.nodes" :key="`${n.name}:${n.ip}`" class="border-b border-border last:border-b-0" data-test="node-row">
                <td class="px-2.5 py-1.5 text-left">{{ n.name }}</td>
                <td class="px-2.5 py-1.5 text-left font-mono">{{ n.ip }}</td>
                <td class="max-w-[220px] truncate px-2.5 py-1.5 text-left text-muted-foreground">{{ n.roles }}</td>
                <td class="px-2.5 py-1.5 text-left font-mono tabular-nums" :class="n.heap_percent >= 90 ? 'danger font-semibold text-destructive' : ''" data-test="node-heap">{{ n.heap_percent }}%</td>
                <td class="px-2.5 py-1.5 text-left font-mono tabular-nums" :class="n.disk_percent >= 90 ? 'danger font-semibold text-destructive' : ''" data-test="node-disk">{{ n.disk_percent }}%</td>
              </tr>
            </tbody>
          </table>
        </div>
      </template>
    </template>
  </div>
</template>
