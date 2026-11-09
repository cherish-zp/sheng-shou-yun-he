<script setup lang="ts">
import { computed, nextTick, onMounted, ref, watch } from 'vue'
import { useBrowseStore } from '@/store/browse'
import type { MessageQuery } from '@/store/browse'
import { OffsetEarliest, OffsetLatest } from '@/api/types'
import type { Message } from '@/api/types'
import { formatTime, truncateCell } from '@/utils/format'
import { CSV_MIME, JSONL_MIME, MESSAGE_EXPORT_COLUMNS, exportCsv, exportJsonl, saveFile } from '@/utils/export'
import ExportDropdown from '@/components/common/ExportDropdown.vue'
import MessageDetailDrawer from './MessageDetailDrawer.vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { ArrowRightToLine, ChevronDown, Search, Send } from 'lucide-vue-next'

const props = withDefaults(
  defineProps<{
    tabId: string
    connectionId: string
    topic: string
    partitions: number[]
    refreshRequest?: number
  }>(),
  { refreshRequest: 0 },
)
const emit = defineEmits<{ (e: 'open-producer'): void }>()

const store = useBrowseStore()
const st = computed(() => store.stateFor(props.tabId))

const mode = ref<'earliest' | 'latest' | 'timestamp'>('earliest')
const partition = ref(-1)
const limit = ref(500)
const timestampMs = ref<number | null>(null)
const startTime = ref('')
const endTime = ref('')
const detailOpen = ref(false)

const partitionOptions = computed(() => [
  { label: 'All', value: -1 },
  ...props.partitions.map((p) => ({ label: String(p), value: p })),
])

// 原生 select 保留(spec 以 setValue 驱动);ui Input 以 :value/@input 接线
// (不透传 v-model),数值输入沿用 v-model.number 的宽松转换语义。
const SELECT_CLS =
  'h-8 min-w-0 rounded-md border border-input bg-transparent px-2.5 text-[13px] shadow-xs outline-none transition-[color,box-shadow] focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50'

// looseToNumber 与 v-model.number 同语义:不可解析时保留原串。
function looseToNumber(v: string): number | string {
  const n = Number.parseFloat(v)
  return Number.isNaN(n) ? v : n
}

function onTimestampInput(e: Event): void {
  timestampMs.value = looseToNumber((e.target as HTMLInputElement).value) as number | null
}

function onLimitInput(e: Event): void {
  limit.value = looseToNumber((e.target as HTMLInputElement).value) as number
}

function onStartTimeInput(e: Event): void {
  startTime.value = (e.target as HTMLInputElement).value
}

function onEndTimeInput(e: Event): void {
  endTime.value = (e.target as HTMLInputElement).value
}

function onJumpOffsetInput(e: Event): void {
  // 非法输入(非数字/负数/小数)一律回退为空串,jumpValid 据此禁用跳转按钮。
  const n = Number.parseFloat((e.target as HTMLInputElement).value)
  jumpOffsetInput.value = Number.isNaN(n) ? '' : n
}

// dtToMs converts a datetime-local value ('') to unix ms; empty input = unset.
function dtToMs(v: string): number | null {
  if (!v) return null
  const ms = new Date(v).getTime()
  return Number.isFinite(ms) ? ms : null
}

function buildQuery(): Partial<MessageQuery> {
  const q: Partial<MessageQuery> = { partition: partition.value, limit: limit.value }
  const startMs = dtToMs(startTime.value)
  if (startMs != null) {
    // A filled start time takes precedence over the 起点 mode select.
    q.offset = OffsetEarliest
    q.timestampMs = startMs
    q.endTimeMs = dtToMs(endTime.value)
    return q
  }
  q.endTimeMs = null
  if (mode.value === 'earliest') {
    q.offset = OffsetEarliest
    q.timestampMs = null
  } else if (mode.value === 'latest') {
    q.offset = OffsetLatest
    q.timestampMs = null
  } else {
    q.offset = OffsetEarliest
    q.timestampMs = timestampMs.value ?? Date.now()
  }
  return q
}

async function runQuery(): Promise<void> {
  await store.fetch(props.tabId, props.connectionId, props.topic, buildQuery())
}

async function loadMore(): Promise<void> {
  await store.fetchMore(props.tabId, props.connectionId, props.topic)
}

// Offset jump: fetch one batch starting at the entered offset and highlight
// the record at that offset. The 时间戳跳转 entry is the existing 起始时间
// input — a time query highlights its first returned record (see targetKey).
const jumpOffsetInput = ref<number | ''>('')
const jumpValid = computed(
  () => typeof jumpOffsetInput.value === 'number' && Number.isInteger(jumpOffsetInput.value) && jumpOffsetInput.value >= 0,
)

async function jumpToOffset(): Promise<void> {
  const offset = jumpOffsetInput.value
  if (typeof offset !== 'number' || !Number.isInteger(offset) || offset < 0) return
  await store.jumpToOffset(props.tabId, props.connectionId, props.topic, offset, {
    partition: partition.value,
    limit: limit.value,
  })
}

// targetKey identifies the highlighted jump-target row; null = no highlight.
const targetKey = computed(() => store.targetKey(props.tabId))

function isTarget(m: Message): boolean {
  return targetKey.value === `${m.partition}:${m.offset}`
}

// Scroll the jump-target row into view whenever the target changes or a jump
// completes. The second part of the key (scrollRequest) makes a repeat jump to
// the same offset re-scroll even though the resolved target key is unchanged.
// Concatenating into a primitive string compares by value, so pagination
// appends that leave the target untouched do not re-trigger a scroll. The
// optional call keeps environments without scrollIntoView (jsdom) safe; the
// highlight itself survives load-more appends without re-scrolling.
const tableWrap = ref<HTMLElement | null>(null)

watch(
  () => `${targetKey.value}|${st.value.scrollRequest}`,
  async () => {
    const key = targetKey.value
    if (!key) return
    await nextTick()
    tableWrap.value?.querySelector('[data-test="target-row"]')?.scrollIntoView?.({ block: 'center' })
  },
)

function openDetail(m: Message): void {
  store.select(props.tabId, m)
  detailOpen.value = true
}

function closeDetail(): void {
  detailOpen.value = false
  store.select(props.tabId, null)
}

// exportAs stores the loaded messages in the picked format via the native
// save dialog. The dropdown component owns its open state and outside-click
// closing.
function exportAs(format: 'csv' | 'jsonl'): void {
  if (format === 'csv') {
    void saveFile(`messages-${props.topic}`, exportCsv(st.value.messages, MESSAGE_EXPORT_COLUMNS), CSV_MIME)
  } else {
    void saveFile(`messages-${props.topic}`, exportJsonl(st.value.messages), JSONL_MIME)
  }
}

watch(
  () => [props.connectionId, props.topic],
  () => runQuery(),
)

// Unified refresh: Layout bumps refreshRequest (topbar button / tab context
// menu) and each data panel re-runs its own fetch path. The watcher only
// fires on increments, so mount keeps its single initial runQuery.
watch(
  () => props.refreshRequest,
  () => {
    if (props.refreshRequest > 0) void runQuery()
  },
)

onMounted(runQuery)
</script>

<template>
  <div class="flex h-full flex-col text-foreground" data-test="message-browser">
    <div class="flex flex-wrap items-end gap-3 border-b border-border bg-muted/50 px-4 py-3">
      <label class="flex flex-col gap-1 text-xs text-muted-foreground">
        分区
        <select v-model="partition" data-test="filter-partition" :class="SELECT_CLS">
          <option v-for="opt in partitionOptions" :key="opt.value" :value="opt.value">{{ opt.label }}</option>
        </select>
      </label>
      <label class="flex flex-col gap-1 text-xs text-muted-foreground">
        起点
        <select v-model="mode" data-test="filter-mode" :class="SELECT_CLS">
          <option value="earliest">最早 Earliest</option>
          <option value="latest">最新 Latest</option>
          <option value="timestamp">指定时间戳</option>
        </select>
      </label>
      <label v-if="mode === 'timestamp'" class="flex flex-col gap-1 text-xs text-muted-foreground">
        时间戳(ms)
        <Input
          :value="timestampMs"
          data-test="filter-timestamp"
          type="number"
          class="h-8 w-[150px] text-[13px]"
          placeholder="毫秒时间戳"
          @input="onTimestampInput"
        />
      </label>
      <label class="flex flex-col gap-1 text-xs text-muted-foreground">
        起始时间
        <Input :value="startTime" data-test="filter-start-time" type="datetime-local" class="h-8 w-[190px] text-[13px]" @input="onStartTimeInput" />
      </label>
      <label class="flex flex-col gap-1 text-xs text-muted-foreground">
        结束时间
        <Input
          :value="endTime"
          data-test="filter-end-time"
          type="datetime-local"
          class="h-8 w-[190px] text-[13px]"
          :disabled="!startTime"
          :title="startTime ? undefined : '起始时间为空时结束时间无效'"
          @input="onEndTimeInput"
        />
      </label>
      <label class="flex flex-col gap-1 text-xs text-muted-foreground">
        条数
        <Input :value="limit" data-test="filter-limit" type="number" min="1" max="10000" class="h-8 w-[84px] text-[13px]" @input="onLimitInput" />
      </label>
      <label class="flex flex-col gap-1 text-xs text-muted-foreground">
        跳转 Offset
        <Input
          :value="jumpOffsetInput"
          data-test="jump-offset-input"
          type="number"
          min="0"
          class="h-8 w-[130px] text-[13px]"
          placeholder="输入 offset"
          @input="onJumpOffsetInput"
        />
      </label>
      <Button variant="ghost" size="sm" type="button" data-test="btn-jump-offset" :disabled="!jumpValid || st.loading" @click="jumpToOffset">
        <ArrowRightToLine />
        跳转
      </Button>
      <Button variant="default" size="sm" type="button" data-test="btn-query" :disabled="st.loading" @click="runQuery">
        <Search />
        {{ st.loading ? '查询中…' : '查询' }}
      </Button>
      <div class="ml-auto flex items-end gap-2">
        <Button variant="outline" size="sm" type="button" data-test="btn-open-producer" @click="emit('open-producer')">
          <Send />
          生产消息
        </Button>
        <ExportDropdown :disabled="st.messages.length === 0" @export="exportAs" />
      </div>
    </div>

    <div v-if="st.error" class="bg-destructive/10 px-4 py-2 text-[13px] text-destructive" data-test="browse-error">{{ st.error }}</div>

    <div ref="tableWrap" class="min-h-0 flex-1 overflow-auto bg-card" data-test="browse-table">
      <table class="w-full border-collapse text-[13px]">
        <thead>
          <tr>
            <th class="sticky top-0 z-[1] border-b border-border bg-muted px-3 py-2 text-left text-xs font-medium text-muted-foreground">Partition</th>
            <th class="sticky top-0 z-[1] border-b border-border bg-muted px-3 py-2 text-left text-xs font-medium text-muted-foreground">Offset</th>
            <th class="sticky top-0 z-[1] border-b border-border bg-muted px-3 py-2 text-left text-xs font-medium text-muted-foreground">Timestamp</th>
            <th class="sticky top-0 z-[1] border-b border-border bg-muted px-3 py-2 text-left text-xs font-medium text-muted-foreground">Key</th>
            <th class="sticky top-0 z-[1] border-b border-border bg-muted px-3 py-2 text-left text-xs font-medium text-muted-foreground">Value</th>
          </tr>
        </thead>
        <tbody>
          <tr
            v-for="m in st.messages"
            :key="`${m.partition}:${m.offset}`"
            class="cursor-pointer border-b border-border transition-colors last:border-b-0"
            :class="isTarget(m) ? 'bg-info-bg shadow-[inset_2px_0_0_var(--info)] hover:bg-info-bg' : 'hover:bg-accent/50'"
            :data-test="isTarget(m) ? 'target-row' : 'message-row'"
            @click="openDetail(m)"
          >
            <td class="px-3 py-1.5 tabular-nums">{{ m.partition }}</td>
            <td class="px-3 py-1.5 font-mono tabular-nums">{{ m.offset }}</td>
            <td class="whitespace-nowrap px-3 py-1.5 font-mono tabular-nums">{{ formatTime(m.timestamp) }}</td>
            <td class="max-w-[320px] truncate px-3 py-1.5 font-mono">{{ truncateCell(m.key) }}</td>
            <td class="max-w-[320px] truncate px-3 py-1.5">{{ truncateCell(m.value) }}</td>
          </tr>
          <tr v-if="st.messages.length === 0 && !st.loading">
            <td colspan="5" class="px-3 py-6 text-center text-muted-foreground" data-test="browse-empty">暂无消息</td>
          </tr>
        </tbody>
      </table>
    </div>

    <div class="flex items-center gap-3 border-t border-border bg-muted/50 px-4 py-2">
      <Button
        v-if="st.hasMore"
        variant="ghost"
        size="sm"
        type="button"
        data-test="btn-load-more"
        :disabled="st.loading"
        @click="loadMore"
      >
        <ChevronDown />
        {{ st.loading ? '加载中…' : '加载更多' }}
      </Button>
      <span class="text-xs tabular-nums text-muted-foreground" data-test="message-count">{{ st.messages.length }} 条</span>
    </div>

    <MessageDetailDrawer :message="st.selected" :show="detailOpen" @close="closeDetail" />
  </div>
</template>
