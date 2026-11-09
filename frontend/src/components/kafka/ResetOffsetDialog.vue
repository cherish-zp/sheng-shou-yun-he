<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { getApi } from '@/api/client'
import type { ConsumerGroup, ResetOffsetMode } from '@/api/types'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'

// 原生 select 保留(spec 以 value+change 事件驱动);ui Input 以 :value/@input
// 接线(不透传 v-model);Teleport to body 保留(既有用例按 document.body 查询)。
const SELECT_CLS =
  'h-8 w-full min-w-0 rounded-md border border-input bg-transparent px-2.5 text-[13px] shadow-xs outline-none transition-[color,box-shadow] focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50 disabled:cursor-not-allowed disabled:opacity-50'

const props = withDefaults(
  defineProps<{ show: boolean; connectionId: string; topic?: string | null; group?: string | null }>(),
  { topic: null, group: null },
)

const emit = defineEmits<{ (e: 'close'): void }>()

// mode 'rewind' is a UI-only convenience: it resolves to an explicit-offset
// reset of current - N per partition (clamped to the log start offset).
type DialogMode = ResetOffsetMode | 'rewind'

const groups = ref<ConsumerGroup[]>([])
const loading = ref(false)
const loadError = ref<string | null>(null)
const selectedGroup = ref<string | null>(null)
const selectedTopic = ref<string | null>(null)
const mode = ref<DialogMode>('latest')
const timestampMs = ref<number>(Date.now())
const rewindN = ref<number>(1000)

interface PreviewRow {
  partition: number
  current: number
  target: number | null
}
const previewRows = ref<PreviewRow[] | null>(null)
// editTargets backs the per-partition offset inputs of the 指定 Offset mode.
const editTargets = ref<Record<number, number>>({})
// rewindTargets holds the clamped targets computed by the last rewind preview.
const rewindTargets = ref<Record<number, number> | null>(null)
const resetting = ref(false)
const resetError = ref<string | null>(null)

const group = computed(() => groups.value.find((g) => g.name === selectedGroup.value) ?? null)
const isStable = computed(() => group.value?.state === 'Stable')
const topicRows = computed(() => {
  const t = selectedTopic.value
  if (!t) return []
  return group.value?.topics?.[t] ?? []
})

const topicOptions = computed<string[]>(() => {
  if (props.topic) return [props.topic]
  const set = new Set<string>()
  for (const g of groups.value) {
    for (const t of Object.keys(g.topics ?? {})) set.add(t)
  }
  return [...set].sort()
})

// dryRunReady: a preview exists (or is not needed because confirm recomputes
// everything) and a group/topic pair is picked.
const canConfirm = computed((): boolean => {
  if (!selectedGroup.value || !selectedTopic.value) return false
  if (mode.value === 'rewind') return rewindTargets.value !== null
  return previewRows.value !== null
})

// Any input that changes the reset target invalidates a shown preview.
watch([selectedGroup, selectedTopic, mode, timestampMs, rewindN], () => {
  previewRows.value = null
  editTargets.value = {}
  rewindTargets.value = null
  resetError.value = null
})

function pickGroup(name: string): void {
  selectedGroup.value = name || null
  const g = groups.value.find((x) => x.name === name)
  const ts = g ? Object.keys(g.topics ?? {}) : []
  if (props.topic) {
    selectedTopic.value = props.topic
  } else {
    selectedTopic.value = ts[0] ?? null
  }
}

watch(
  () => props.show,
  (show) => {
    if (!show) return
    groups.value = []
    previewRows.value = null
    editTargets.value = {}
    rewindTargets.value = null
    resetError.value = null
    mode.value = 'latest'
    void open()
  },
  { immediate: true },
)

async function open(): Promise<void> {
  loading.value = true
  loadError.value = null
  try {
    groups.value = await getApi().listConsumerGroups(props.connectionId)
    const pre = props.group ?? groups.value[0]?.name ?? null
    selectedGroup.value = pre
    const g = groups.value.find((x) => x.name === pre)
    const ts = g ? Object.keys(g.topics ?? {}) : []
    selectedTopic.value = props.topic ?? ts[0] ?? null
  } catch (e) {
    loadError.value = e instanceof Error ? e.message : String(e)
  } finally {
    loading.value = false
  }
}

// previewReset builds the dry-run table. latest targets come straight from the
// lag data; earliest/timestamp targets come from the read-only preview API;
// rewind clamps current - N to the log start; offset prefills editable inputs
// with the current offsets.
async function previewReset(): Promise<void> {
  if (!selectedGroup.value || !selectedTopic.value) return
  const rows: PreviewRow[] = topicRows.value.map((r) => ({
    partition: r.partition,
    current: r.current_offset,
    target: null,
  }))
  previewRows.value = rows
  const gid = selectedGroup.value
  const tid = selectedTopic.value
  try {
    if (mode.value === 'latest') {
      for (const row of rows) {
        const src = topicRows.value.find((r) => r.partition === row.partition)
        if (src) row.target = src.log_end_offset
      }
    } else if (mode.value === 'rewind') {
      const starts = await getApi().previewResetOffset({
        connection_id: props.connectionId,
        group: gid,
        topic: tid,
        mode: 'earliest',
      })
      const targets: Record<number, number> = {}
      for (const row of rows) {
        const start = starts[row.partition] ?? 0
        const target = Math.max(start, row.current - (rewindN.value || 0))
        targets[row.partition] = target
        row.target = target
      }
      rewindTargets.value = targets
    } else if (mode.value === 'offset') {
      const targets: Record<number, number> = {}
      for (const row of rows) targets[row.partition] = row.current
      editTargets.value = targets
      for (const row of rows) row.target = row.current
    } else {
      const offsets = await getApi().previewResetOffset({
        connection_id: props.connectionId,
        group: gid,
        topic: tid,
        mode: mode.value,
        timestamp_ms: mode.value === 'timestamp' ? timestampMs.value || Date.now() : undefined,
      })
      for (const row of rows) {
        const t = offsets[row.partition]
        if (t !== undefined) row.target = t
      }
    }
  } catch {
    // Targets degrade to "—"; the preview table itself stays visible.
  }
  // A mode/group switch while the preview was in flight cleared the table.
  if (previewRows.value !== rows) return
}

async function confirmReset(): Promise<void> {
  if (!selectedGroup.value || !selectedTopic.value || resetting.value) return
  resetting.value = true
  resetError.value = null
  try {
    if (mode.value === 'offset') {
      await getApi().resetConsumerGroupOffset({
        connection_id: props.connectionId,
        group: selectedGroup.value,
        topic: selectedTopic.value,
        mode: 'offset',
        per_partition_offsets: { ...editTargets.value },
      })
    } else if (mode.value === 'rewind') {
      if (!rewindTargets.value) return
      await getApi().resetConsumerGroupOffset({
        connection_id: props.connectionId,
        group: selectedGroup.value,
        topic: selectedTopic.value,
        mode: 'offset',
        per_partition_offsets: { ...rewindTargets.value },
      })
    } else {
      await getApi().resetConsumerGroupOffset({
        connection_id: props.connectionId,
        group: selectedGroup.value,
        topic: selectedTopic.value,
        mode: mode.value,
        timestamp_ms: mode.value === 'timestamp' ? timestampMs.value || Date.now() : undefined,
      })
    }
    emit('close')
  } catch (e) {
    resetError.value = e instanceof Error ? e.message : String(e)
  } finally {
    resetting.value = false
  }
}
</script>

<template>
  <Teleport to="body">
    <div v-if="show" class="fixed inset-0 z-[1200] flex items-center justify-center bg-black/50" @click.self="emit('close')">
      <div
        class="dialog flex max-h-[calc(100vh-64px)] w-[560px] max-w-[calc(100vw-48px)] flex-col gap-3 overflow-auto rounded-lg border border-border bg-background p-4 text-foreground shadow-lg"
        data-test="reset-offset-dialog"
      >
        <div class="flex items-center justify-between">
          <span class="text-[15px] font-semibold">重置消费位点</span>
          <Button variant="ghost" size="sm" class="h-6 w-6 p-0" data-test="btn-reset-x" @click="emit('close')">✕</Button>
        </div>

        <div v-if="loadError" class="rounded-md bg-destructive/10 px-2.5 py-2 text-[13px] text-destructive" data-test="reset-load-error">{{ loadError }}</div>
        <div v-else-if="loading" class="text-[13px] text-muted-foreground" data-test="reset-loading">加载消费组…</div>

        <template v-else>
          <div class="flex flex-wrap items-end gap-2.5">
            <div class="field flex min-w-[150px] flex-1 flex-col gap-1">
              <Label class="text-xs font-normal text-muted-foreground">Topic</Label>
              <select
                :class="SELECT_CLS"
                data-test="select-reset-topic"
                :disabled="!!props.topic"
                :value="selectedTopic ?? ''"
                @change="selectedTopic = ($event.target as HTMLSelectElement).value || null"
              >
                <option v-for="t in topicOptions" :key="t" :value="t">{{ t }}</option>
              </select>
            </div>
            <div class="field flex min-w-[150px] flex-1 flex-col gap-1">
              <Label class="text-xs font-normal text-muted-foreground">消费组</Label>
              <select
                :class="SELECT_CLS"
                data-test="select-reset-group"
                :value="selectedGroup ?? ''"
                @change="pickGroup(($event.target as HTMLSelectElement).value)"
              >
                <option v-for="g in groups" :key="g.name" :value="g.name" data-test="reset-group-option">
                  {{ g.name }}（{{ g.state }}）
                </option>
              </select>
            </div>
          </div>

          <div v-if="isStable" class="rounded-md bg-warning/15 px-2.5 py-2 text-[13px] text-warning" data-test="reset-stable-warning">
            ⚠ 该消费组仍在运行（Stable），broker 将拒绝提交。请先停止消费者，再执行重置。
          </div>

          <div class="flex flex-wrap items-end gap-2.5">
            <div class="field flex min-w-[150px] flex-1 flex-col gap-1">
              <Label class="text-xs font-normal text-muted-foreground">目标位置</Label>
              <select v-model="mode" :class="SELECT_CLS" data-test="select-reset-mode">
                <option value="earliest">Earliest（最早）</option>
                <option value="latest">Latest（最新）</option>
                <option value="timestamp">指定时间戳</option>
                <option value="offset">指定 Offset</option>
                <option value="rewind">回退 N 条</option>
              </select>
            </div>
            <div v-if="mode === 'timestamp'" class="field flex min-w-[150px] flex-1 flex-col gap-1">
              <Label class="text-xs font-normal text-muted-foreground">时间戳 (unix ms)</Label>
              <Input
                type="number"
                :value="timestampMs"
                data-test="input-reset-timestamp"
                @input="timestampMs = Number(($event.target as HTMLInputElement).value)"
              />
            </div>
            <div v-if="mode === 'rewind'" class="field flex min-w-[150px] flex-1 flex-col gap-1">
              <Label class="text-xs font-normal text-muted-foreground">回退条数 N</Label>
              <Input
                type="number"
                min="1"
                :value="rewindN"
                data-test="input-rewind-n"
                @input="rewindN = Number(($event.target as HTMLInputElement).value)"
              />
            </div>
            <Button variant="outline" size="sm" data-test="btn-reset-dry-run" :disabled="!selectedGroup || !selectedTopic" @click="previewReset">
              预览 (Dry-run)
            </Button>
          </div>

          <div v-if="previewRows" class="overflow-hidden rounded-md border border-border bg-muted/30">
            <table class="data-table" data-test="reset-preview-table">
              <thead>
                <tr>
                  <th>Partition</th>
                  <th>当前 Offset</th>
                  <th>目标 Offset</th>
                  <th>变化</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="row in previewRows" :key="row.partition" data-test="reset-row">
                  <td class="font-mono">{{ row.partition }}</td>
                  <td class="font-mono">{{ row.current }}</td>
                  <td v-if="mode === 'offset'" class="font-mono">
                    <Input
                      :value="editTargets[row.partition]"
                      class="h-7 px-1.5"
                      type="number"
                      :data-test="`reset-row-input-${row.partition}`"
                      @input="editTargets[row.partition] = Number(($event.target as HTMLInputElement).value)"
                    />
                  </td>
                  <td v-else class="font-mono" data-test="reset-row-target">{{ row.target ?? '—' }}</td>
                  <td class="font-mono text-muted-foreground">{{ row.target == null ? '—' : row.target - row.current }}</td>
                </tr>
                <tr v-if="previewRows.length === 0">
                  <td colspan="4" class="py-3 text-center text-[13px] text-muted-foreground">该消费组在此 Topic 上没有已提交的位点</td>
                </tr>
              </tbody>
            </table>
          </div>

          <div v-if="resetError" class="rounded-md bg-destructive/10 px-2.5 py-2 text-[13px] text-destructive" data-test="reset-error">{{ resetError }}</div>

          <div class="flex justify-end gap-2">
            <Button variant="outline" size="sm" data-test="btn-reset-cancel" :disabled="resetting" @click="emit('close')">
              取消
            </Button>
            <Button
              variant="destructive"
              size="sm"
              data-test="btn-reset-confirm"
              :disabled="!canConfirm || resetting"
              :title="isStable ? '消费组 Stable 时 broker 将拒绝提交' : ''"
              @click="confirmReset"
            >
              {{ resetting ? '提交中…' : '确认重置' }}
            </Button>
          </div>
        </template>
      </div>
    </div>
  </Teleport>
</template>

