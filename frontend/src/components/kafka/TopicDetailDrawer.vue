<script setup lang="ts">
import { ref, watch } from 'vue'
import { getApi } from '@/api/client'
import type { TopicDetail, TopicMessageCounts } from '@/api/types'
import { formatCount } from '@/utils/format'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'

const props = defineProps<{ connectionId: string; topic: string | null; show: boolean; edit?: boolean }>()
const emit = defineEmits<{ (e: 'close'): void }>()

const detail = ref<TopicDetail | null>(null)
const loading = ref(false)
const error = ref<string | null>(null)

// Config editing state: drafts holds one editable value per whitelisted key.
const editing = ref(false)
const drafts = ref<Record<string, string>>({})
const saving = ref(false)
const cfgError = ref<string | null>(null)
// pendingEdit consumes the `edit` prop exactly once per drawer opening, so a
// save's trailing reload returns to the read-only view instead of flipping
// back into edit mode.
const pendingEdit = ref(false)

// counts holds the offset-derived message stats for the open topic (retained
// within retention + total ever produced); null renders nothing.
const counts = ref<TopicMessageCounts | null>(null)

// ui Input 以 :value/@input 接线(不透传 v-model);spec 的 setInput 以原生
// input 事件驱动,事件目标即真实 input 元素。
function onDraftInput(key: string, e: Event): void {
  drafts.value[key] = (e.target as HTMLInputElement).value
}

// 表格基座 token 类:卡片化容器 + 粘性表头差异全部并入工具类。
const TH_CLS = 'border-b border-border bg-muted/50 px-3 py-2 text-left text-xs font-medium text-muted-foreground'
const TD_CLS = 'px-3 py-1.5'

async function loadCounts(): Promise<void> {
  if (!props.connectionId || !props.topic) return
  try {
    const map = await getApi().getTopicMessageCounts(props.connectionId, [props.topic])
    counts.value = map[props.topic] ?? null
  } catch {
    counts.value = null
  }
}

// requestSeq guards against last-write-wins on a quick topic/connection switch
// and makes close/timeout void any in-flight callbacks: every load tags itself
// with a fresh sequence and a response whose seq is no longer current (a stale
// topic, or a save whose drawer was already closed) is dropped before it can
// touch the drawer state.
let requestSeq = 0

// load fetches the partition topology and key configs of the selected topic.
async function load(): Promise<void> {
  if (!props.topic) return
  const seq = ++requestSeq
  loading.value = true
  error.value = null
  detail.value = null
  try {
    const data = await getApi().describeTopic(props.connectionId, props.topic)
    if (seq !== requestSeq) return
    detail.value = data
    if (pendingEdit.value && data.configs.length > 0) {
      pendingEdit.value = false
      startEdit()
    }
  } catch (e) {
    if (seq !== requestSeq) return
    error.value = e instanceof Error ? e.message : String(e)
  } finally {
    if (seq === requestSeq) loading.value = false
  }
}

watch(
  () => [props.show, props.connectionId, props.topic] as const,
  ([show]) => {
    if (!show) {
      // Closing the drawer voids any in-flight load/save so a late callback
      // can never render on the hidden drawer (reopening re-fetches).
      requestSeq += 1
      detail.value = null
      error.value = null
      loading.value = false
      editing.value = false
      saving.value = false
      cfgError.value = null
      drafts.value = {}
      pendingEdit.value = false
      return
    }
    pendingEdit.value = !!props.edit
    counts.value = null
    void load()
    void loadCounts()
  },
  { immediate: true },
)

// startEdit copies the described values into per-key drafts so edits start
// from the current broker state.
function startEdit(): void {
  if (!detail.value) return
  drafts.value = Object.fromEntries(detail.value.configs.map((c) => [c.key, c.value]))
  cfgError.value = null
  editing.value = true
}

function cancelEdit(): void {
  editing.value = false
  cfgError.value = null
}

// save sends every whitelisted row and, on success, re-describes the topic so
// the table reflects the broker's accepted values. On failure the error is
// shown and the user's edits are kept in place. If the drawer was closed or
// the topic switched while the save was in flight, the trailing load() is
// dropped (requestSeq was bumped in the meantime); the reopened drawer pulls
// fresh data on its own.
async function save(): Promise<void> {
  if (!detail.value || !props.topic || saving.value) return
  saving.value = true
  cfgError.value = null
  const seq = requestSeq
  try {
    const entries = detail.value.configs.map((c) => ({ key: c.key, value: drafts.value[c.key] ?? '' }))
    await getApi().alterTopicConfig({ connection_id: props.connectionId, topic: props.topic, entries })
    if (seq !== requestSeq) return
    editing.value = false
    await load()
  } catch (e) {
    if (seq !== requestSeq) return
    cfgError.value = e instanceof Error ? e.message : String(e)
  } finally {
    saving.value = false
  }
}

function listText(nums: number[]): string {
  return nums.join(', ')
}
</script>

<template>
  <Teleport to="body">
    <!-- Teleported: the sidebar's backdrop-filter would otherwise become the
         containing block for position:fixed and pin the drawer to the sidebar
         edge instead of the window's right side. -->
    <div v-if="show && topic" class="fixed inset-y-0 right-0 z-[1000] flex w-96 flex-col border-l border-border bg-background text-foreground shadow-lg" data-test="topic-detail-drawer">
    <div class="flex items-center justify-between gap-2 border-b border-border px-4 py-3">
      <span class="min-w-0 truncate text-[15px] font-semibold">Topic 详情 <span class="ml-1 font-mono text-[13px] text-muted-foreground">{{ topic }}</span></span>
      <Button variant="ghost" size="sm" class="h-7 w-7 flex-none p-0 text-muted-foreground" data-test="drawer-close" type="button" @click="emit('close')">✕</Button>
    </div>
    <div class="overflow-auto p-4">
      <div v-if="loading" class="text-[13px] text-muted-foreground" data-test="detail-loading">加载中…</div>
      <div v-else-if="error" class="text-[13px] text-destructive" data-test="detail-error">{{ error }}</div>
      <template v-else-if="detail">
        <div v-if="counts" class="mb-1 flex flex-wrap items-baseline gap-2 rounded-lg border border-border bg-muted/50 px-3 py-2 text-xs text-muted-foreground" data-test="drawer-stats">
          <span><span class="font-mono text-[13px] font-semibold tabular-nums text-foreground" data-test="stat-retained">{{ formatCount(counts.retained) }}</span> 保留期内</span>
          <span class="text-muted-foreground/60">·</span>
          <span><span class="font-mono text-[13px] font-semibold tabular-nums text-foreground" data-test="stat-total">{{ formatCount(counts.total) }}</span> 累计生产</span>
          <span class="text-muted-foreground/60">·</span>
          <span><span class="font-mono text-[13px] font-semibold tabular-nums text-foreground">{{ detail.partitions.length }}</span> 分区</span>
        </div>
        <h3 class="mb-2 mt-3.5 text-xs font-semibold uppercase tracking-wider text-muted-foreground first:mt-0">分区拓扑</h3>
        <div class="overflow-hidden rounded-lg border border-border bg-card">
          <table class="w-full border-collapse text-[13px]" data-test="topo-table">
            <thead>
              <tr>
                <th :class="TH_CLS">分区</th>
                <th :class="TH_CLS">Leader</th>
                <th :class="TH_CLS">Replicas</th>
                <th :class="TH_CLS">ISR</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="p in detail.partitions" :key="p.id" class="border-b border-border last:border-b-0" data-test="topo-row">
                <td class="font-mono tabular-nums" :class="TD_CLS" data-test="row-partition">{{ p.id }}</td>
                <td class="font-mono tabular-nums" :class="TD_CLS" data-test="row-leader">{{ p.leader }}</td>
                <td class="font-mono tabular-nums" :class="TD_CLS" data-test="row-replicas">{{ listText(p.replicas) }}</td>
                <td class="font-mono tabular-nums" :class="TD_CLS" data-test="row-isr">{{ listText(p.isr) }}</td>
              </tr>
            </tbody>
          </table>
        </div>

        <div class="mt-3.5 flex items-center justify-between">
          <h3 class="text-xs font-semibold uppercase tracking-wider text-muted-foreground">关键配置</h3>
          <Button
            v-if="detail.configs.length > 0 && !editing"
            variant="ghost"
            size="sm"
            class="text-xs"
            type="button"
            data-test="cfg-edit"
            @click="startEdit"
          >
            编辑
          </Button>
        </div>
        <template v-if="editing">
          <div class="mt-2 overflow-hidden rounded-lg border border-border bg-card">
            <table class="w-full border-collapse text-[13px]" data-test="cfg-table">
              <thead>
                <tr>
                  <th :class="TH_CLS">配置项</th>
                  <th :class="TH_CLS">值</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="c in detail.configs" :key="c.key" class="border-b border-border last:border-b-0" data-test="cfg-row">
                  <td class="font-mono" :class="TD_CLS" data-test="cfg-key">{{ c.key }}</td>
                  <td :class="TD_CLS">
                    <Input
                      :value="drafts[c.key]"
                      class="h-8 w-full font-mono text-[13px]"
                      data-test="cfg-input"
                      @input="onDraftInput(c.key, $event)"
                    />
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
          <div v-if="cfgError" class="mt-2.5 rounded-md bg-destructive/10 px-2.5 py-1.5 text-[13px] text-destructive" data-test="cfg-error">{{ cfgError }}</div>
          <div class="mt-2.5 flex justify-end gap-2">
            <Button variant="ghost" size="sm" class="text-xs" type="button" data-test="cfg-cancel" :disabled="saving" @click="cancelEdit">
              取消
            </Button>
            <Button variant="default" size="sm" class="text-xs" type="button" data-test="cfg-save" :disabled="saving" @click="save">
              {{ saving ? '保存中…' : '保存' }}
            </Button>
          </div>
        </template>
        <template v-else>
          <div v-if="detail.configs.length" class="mt-2 overflow-hidden rounded-lg border border-border bg-card">
            <table class="w-full border-collapse text-[13px]" data-test="cfg-table">
              <thead>
                <tr>
                  <th :class="TH_CLS">配置项</th>
                  <th :class="TH_CLS">值</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="c in detail.configs" :key="c.key" class="border-b border-border last:border-b-0" data-test="cfg-row">
                  <td class="font-mono" :class="TD_CLS" data-test="cfg-key">{{ c.key }}</td>
                  <td class="font-mono" :class="TD_CLS" data-test="cfg-value">{{ c.value }}</td>
                </tr>
              </tbody>
            </table>
          </div>
          <div v-else class="text-[13px] text-muted-foreground" data-test="config-empty">（无配置）</div>
        </template>
      </template>
    </div>
    </div>
  </Teleport>
</template>
