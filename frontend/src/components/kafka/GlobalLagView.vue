<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { getApi } from '@/api/client'
import type { ConsumerGroup } from '@/api/types'
import { flattenGroupLag } from '@/utils/lag'
import { fuzzyScore } from '@/utils/fuzzy'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { RefreshCw } from 'lucide-vue-next'

const props = withDefaults(
  defineProps<{ connectionId: string; refreshRequest?: number }>(),
  { refreshRequest: 0 },
)

// 行点击跳转:组定位消费组 tab,Topic 作为初始选中项带入(消费组详情)。
const emit = defineEmits<{ (e: 'open-group-lag', group: string, topic: string): void }>()

const groups = ref<ConsumerGroup[]>([])
const loading = ref(false)
const error = ref<string | null>(null)
const query = ref('')

// ui Input 以 :value/@input 接线(不透传 v-model)。
function onSearchInput(e: Event): void {
  query.value = (e.target as HTMLInputElement).value
}

// lagBadgeClass 输出语义色 Badge 类:lag-danger(>1000)/lag-warn(>100)为
// spec 的 hook 类,与语义 token 并存。
function lagBadgeClass(lag: number): string {
  if (lag > 1000) return 'lag-danger border-transparent bg-destructive/10 text-destructive'
  if (lag > 100) return 'lag-warn border-transparent bg-warning-bg text-warning'
  return 'border-transparent bg-muted text-muted-foreground'
}

// requestSeq guards against last-write-wins on a quick connection switch: a
// slow response from the previous connection must never overwrite the data of
// the current one, so each fetch tags itself and stale results are dropped.
let requestSeq = 0

async function refresh(): Promise<void> {
  const seq = ++requestSeq
  loading.value = true
  error.value = null
  try {
    const data = await getApi().listConsumerGroups(props.connectionId)
    if (seq !== requestSeq) return
    groups.value = data
  } catch (e) {
    if (seq !== requestSeq) return
    error.value = e instanceof Error ? e.message : String(e)
  } finally {
    if (seq === requestSeq) loading.value = false
  }
}

onMounted(refresh)

// Unified refresh from the top bar / tab context menu: re-run the local
// refresh() (listConsumerGroups). Only fires on increments, so mount keeps
// its single initial refresh.
watch(
  () => props.refreshRequest,
  () => {
    if (props.refreshRequest > 0) void refresh()
  },
)

// Switching lag tabs patches this component instance in place (Layout does not
// key its workspace), so connectionId changes must drop the previous
// connection's data and refetch — same idiom as MessageBrowser/SqlConsole.
watch(
  () => props.connectionId,
  () => {
    groups.value = []
    error.value = null
    query.value = ''
    refresh()
  },
)

// rows re-flattens the fetched groups and keeps them sorted by Lag descending
// even while filtering, so the biggest backlogs stay on top while searching.
// A row matches when either its group or its topic name fuzzy-matches.
const rows = computed(() => {
  const q = query.value.trim()
  const all = flattenGroupLag(groups.value)
  if (!q) return all
  return all.filter(
    (r) => fuzzyScore(q, r.group) !== Infinity || fuzzyScore(q, r.topic) !== Infinity,
  )
})

function emptyText(): string {
  return query.value.trim() ? '无匹配 Group / Topic' : '暂无 lag 数据'
}
</script>

<template>
  <div class="p-4 text-foreground" data-test="global-lag-view">
    <div class="flex flex-wrap items-center gap-3">
      <span class="text-sm font-semibold">Lag 总览</span>
      <Input
        :value="query"
        class="h-8 w-[220px] text-[13px]"
        type="search"
        data-test="global-lag-search"
        placeholder="🔍 模糊搜索 Group / Topic…"
        autocapitalize="off"
        autocorrect="off"
        autocomplete="off"
        spellcheck="false"
        @input="onSearchInput"
      />
      <Button variant="ghost" size="sm" class="text-xs" type="button" data-test="btn-refresh" :disabled="loading" @click="refresh">
        <RefreshCw />
        {{ loading ? '加载中…' : '刷新' }}
      </Button>
    </div>

    <div v-if="error" class="mt-2.5 rounded-md bg-destructive/10 px-2.5 py-1.5 text-[13px] text-destructive" data-test="lag-error">{{ error }}</div>

    <div class="mt-3 overflow-hidden rounded-lg border border-border bg-card">
      <table class="w-full border-collapse text-[13px]" data-test="global-lag-table">
        <thead>
          <tr>
            <th class="border-b border-border bg-muted/50 px-3 py-2 text-left text-xs font-medium text-muted-foreground">Group</th>
            <th class="border-b border-border bg-muted/50 px-3 py-2 text-left text-xs font-medium text-muted-foreground">Topic</th>
            <th class="border-b border-border bg-muted/50 px-3 py-2 text-left text-xs font-medium text-muted-foreground">Total Lag</th>
          </tr>
        </thead>
        <tbody>
          <tr
            v-for="r in rows"
            :key="`${r.group}:${r.topic}`"
            class="cursor-pointer border-b border-border transition-colors last:border-b-0 hover:bg-accent/50"
            data-test="lag-row"
            :title="`查看 ${r.group} / ${r.topic} 消费详情`"
            @click="emit('open-group-lag', r.group, r.topic)"
          >
            <td class="px-3 py-1.5 font-mono" data-test="row-group">{{ r.group }}</td>
            <td class="px-3 py-1.5 font-mono" data-test="row-topic">{{ r.topic }}</td>
            <td class="px-3 py-1.5">
              <Badge class="font-mono tabular-nums" :class="lagBadgeClass(r.lag)" data-test="lag-value">{{ r.lag }}</Badge>
            </td>
          </tr>
          <tr v-if="rows.length === 0 && !loading && !error">
            <td colspan="3" class="px-3 py-5 text-center text-muted-foreground" data-test="lag-empty">{{ emptyText() }}</td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>
