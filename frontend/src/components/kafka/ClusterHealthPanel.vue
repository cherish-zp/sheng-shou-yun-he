<script setup lang="ts">
import { onMounted, ref, watch } from 'vue'
import { getApi } from '@/api/client'
import type { ClusterHealth } from '@/api/types'
import { Badge } from '@/components/ui/badge'

const props = withDefaults(
  defineProps<{ connectionId: string; refreshRequest?: number }>(),
  { refreshRequest: 0 },
)

const health = ref<ClusterHealth | null>(null)
const loading = ref(false)
const error = ref<string | null>(null)

// requestSeq guards against last-write-wins on a quick connection switch or
// refresh burst: a slow response must never overwrite the snapshot belonging
// to the current request, so each load tags itself and stale results are
// dropped before they touch the panel state.
let requestSeq = 0

// load fetches the cluster health snapshot of the connection.
async function load(): Promise<void> {
  const seq = ++requestSeq
  loading.value = true
  error.value = null
  try {
    const data = await getApi().describeCluster(props.connectionId)
    if (seq !== requestSeq) return
    health.value = data
  } catch (e) {
    if (seq !== requestSeq) return
    health.value = null
    error.value = e instanceof Error ? e.message : String(e)
  } finally {
    if (seq === requestSeq) loading.value = false
  }
}

onMounted(load)

// Unified refresh from the top bar / tab context menu: re-run the local
// load() (describeCluster). Only fires on increments, so mount keeps its
// single initial load.
watch(
  () => props.refreshRequest,
  () => {
    if (props.refreshRequest > 0) void load()
  },
)

// Switching tabs patches this component instance in place (Layout does not
// key its workspace), so connectionId changes must drop the previous
// connection's data and refetch — same idiom as MessageBrowser/SqlConsole.
watch(
  () => props.connectionId,
  () => {
    health.value = null
    error.value = null
    load()
  },
)
</script>

<template>
  <div class="max-w-[860px] px-0.5 py-1 text-foreground" data-test="cluster-health-panel">
    <div v-if="loading" class="py-1 text-[13px] text-muted-foreground" data-test="health-loading">加载中…</div>
    <div v-else-if="error" class="py-1 text-[13px] text-destructive" data-test="health-error">{{ error }}</div>
    <template v-else-if="health">
      <div v-if="health.brokers.length === 0" class="py-1 text-[13px] text-muted-foreground" data-test="health-empty">
        未获取到任何 Broker，请确认连接是否可用后重试。
      </div>
      <template v-else>
        <h3 class="mb-2 mt-3.5 text-xs font-semibold uppercase tracking-wider text-muted-foreground first:mt-0">概览</h3>
        <div class="grid grid-cols-4 gap-2" data-test="health-summary">
          <div class="flex min-w-0 flex-col gap-0.5 rounded-lg border border-border bg-card px-3 py-2.5">
            <span class="text-[11px] text-muted-foreground">Kafka 版本</span>
            <span class="truncate font-mono text-[13px]" data-test="summary-version">{{ health.kafka_version || '未知' }}</span>
          </div>
          <div class="flex min-w-0 flex-col gap-0.5 rounded-lg border border-border bg-card px-3 py-2.5">
            <span class="text-[11px] text-muted-foreground">Controller</span>
            <span class="truncate font-mono text-[13px]" data-test="summary-controller">broker {{ health.controller_id }}</span>
          </div>
          <div class="flex min-w-0 flex-col gap-0.5 rounded-lg border border-border bg-card px-3 py-2.5">
            <span class="text-[11px] text-muted-foreground">Broker 数</span>
            <span class="truncate font-mono text-[13px] tabular-nums" data-test="summary-brokers">{{ health.brokers.length }}</span>
          </div>
          <div class="flex min-w-0 flex-col gap-0.5 rounded-lg border border-border bg-card px-3 py-2.5">
            <span class="text-[11px] text-muted-foreground">Under-Replicated 分区</span>
            <span
              class="truncate font-mono text-[13px] tabular-nums"
              :class="health.under_replicated_partitions > 0 ? 'danger font-semibold text-destructive' : ''"
              data-test="urp-value"
            >{{ health.under_replicated_partitions }}</span>
          </div>
          <div class="col-span-2 flex min-w-0 flex-col gap-0.5 rounded-lg border border-border bg-card px-3 py-2.5">
            <span class="text-[11px] text-muted-foreground">Cluster ID</span>
            <span class="truncate font-mono text-[13px]" data-test="summary-cluster-id">{{ health.cluster_id || '未知' }}</span>
          </div>
        </div>

        <h3 class="mb-2 mt-3.5 text-xs font-semibold uppercase tracking-wider text-muted-foreground">Broker 拓扑</h3>
        <div
          v-for="b in health.brokers"
          :key="b.id"
          class="mb-2 rounded-lg border border-border bg-card px-3 py-2.5"
          :class="!b.online ? 'offline border-destructive shadow-[inset_0_0_0_1px_var(--destructive)]' : ''"
          data-test="broker-card"
        >
          <div class="flex items-center gap-1.5">
            <span class="font-mono text-[13px] font-semibold" data-test="broker-id">broker {{ b.id }}</span>
            <Badge
              v-if="b.id === health.controller_id"
              class="rounded-full border-transparent bg-info-bg px-1.5 text-[10px] uppercase tracking-wide text-info"
              data-test="controller-badge"
            >controller</Badge>
            <Badge
              class="ml-auto rounded-full border-transparent"
              :class="b.online ? 'bg-success-bg text-success' : 'bg-destructive/10 text-destructive'"
              :data-status="b.online ? 'online' : 'offline'"
              data-test="broker-state"
            >{{ b.online ? '在线' : '离线' }}</Badge>
          </div>
          <div class="mt-1 font-mono text-xs text-muted-foreground" data-test="broker-host">{{ b.host }}:{{ b.port }}</div>
          <div class="mt-1 flex flex-wrap gap-3 text-xs text-muted-foreground">
            <span v-if="b.rack" data-test="broker-rack">机架 {{ b.rack }}</span>
            <span data-test="broker-version">{{ b.version || '版本未知' }}</span>
          </div>
        </div>
      </template>
    </template>
  </div>
</template>
