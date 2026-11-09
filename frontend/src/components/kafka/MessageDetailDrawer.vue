<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import type { Message } from '@/api/types'
import { formatTime, displayValue, prettyJSON } from '@/utils/format'
import { renderJsonView } from '@/utils/jsonview'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'

const props = defineProps<{ message: Message | null; show: boolean }>()
const emit = defineEmits<{ (e: 'close'): void }>()

// Per-open display state: reset whenever the drawer opens or the message
// changes, so each detail view starts formatted (when parseable) and collapsed.
const valueMode = ref<'formatted' | 'raw'>('formatted')
const keyMode = ref<'formatted' | 'raw'>('formatted')
const valueExpanded = ref(false)

watch(
  () => [props.show, props.message] as const,
  () => {
    valueMode.value = 'formatted'
    keyMode.value = 'formatted'
    valueExpanded.value = false
  },
)

const keyRaw = computed(() => displayValue(props.message?.key))
const keyView = computed(() => renderJsonView(keyRaw.value))
const keyFormatted = computed(() => keyView.value.ok && keyMode.value === 'formatted')

const valueRaw = computed(() => displayValue(props.message?.value))
const valueView = computed(() => renderJsonView(valueRaw.value))
const valueFormatted = computed(() => valueView.value.ok && valueMode.value === 'formatted')
const valueText = computed(() => (valueFormatted.value ? valueView.value.text : valueRaw.value))

// Collapse cap: max-h 18em = 12 lines at line-height 1.5; the toggle only
// appears past that threshold so short values stay clean. It is derived from
// the view's canonical text (pretty or raw passthrough) so the control stays
// visible across raw/formatted switches. A character bound catches
// single-line blobs (base64/hex) that never exceed the line cap.
const LONG_LINES = 12
const COLLAPSE_CHAR_THRESHOLD = 2000
const valueLong = computed(() => {
  const text = valueView.value.text
  return text.length > COLLAPSE_CHAR_THRESHOLD || text.split('\n').length > LONG_LINES
})
// collapsed 同时承载两件事:spec 的 hook 类断言与条件裁剪(18em 封顶)。
const valueCollapsed = computed(() => valueLong.value && !valueExpanded.value)

function toggleKeyMode(): void {
  keyMode.value = keyMode.value === 'formatted' ? 'raw' : 'formatted'
}
function toggleValueMode(): void {
  valueMode.value = valueMode.value === 'formatted' ? 'raw' : 'formatted'
}
</script>

<template>
  <div v-if="show && message" class="drawer fixed inset-y-0 right-0 z-[1000] flex w-[460px] flex-col border-l border-border text-foreground" data-test="message-drawer">
    <div class="flex items-center justify-between border-b border-border px-4 py-3">
      <span class="text-[15px] font-semibold">消息详情</span>
      <Button variant="ghost" size="sm" class="h-7 w-7 p-0 text-muted-foreground" data-test="drawer-close" type="button" @click="emit('close')">✕</Button>
    </div>
    <div class="overflow-auto p-4">
      <div class="flex flex-wrap gap-2">
        <Badge class="border-transparent bg-info-bg font-mono text-info" data-test="meta-partition">Partition {{ message.partition }}</Badge>
        <Badge class="border-transparent bg-warning-bg font-mono text-warning" data-test="meta-offset">Offset {{ message.offset }}</Badge>
        <Badge class="border-transparent bg-success-bg font-mono text-success">{{ formatTime(message.timestamp) }}</Badge>
      </div>
      <div class="mt-4">
        <div class="flex items-center justify-between gap-2">
          <span class="text-xs uppercase tracking-wider text-muted-foreground">Key</span>
          <Button v-if="keyView.ok" variant="outline" size="sm" class="h-6 px-2 text-[11px]" data-test="key-mode" type="button" :aria-pressed="keyMode === 'formatted'" @click="toggleKeyMode">
            {{ keyMode === 'formatted' ? '原始' : '格式化' }}
          </Button>
        </div>
        <pre
          class="mt-1.5 whitespace-pre-wrap break-all rounded-md border border-border bg-muted/50 p-2.5 font-mono text-[13px] leading-relaxed text-foreground"
          data-test="key-value"
        ><template v-if="keyFormatted"><span v-for="(t, i) in keyView.tokens" :key="i" :class="'tok-' + t.type">{{ t.text }}</span></template><template v-else>{{ keyRaw }}</template></pre>
      </div>
      <div class="mt-4">
        <div class="flex items-center justify-between gap-2">
          <span class="text-xs uppercase tracking-wider text-muted-foreground">Value</span>
          <span class="inline-flex gap-1.5">
            <Button v-if="valueView.ok" variant="outline" size="sm" class="h-6 px-2 text-[11px]" data-test="value-mode" type="button" :aria-pressed="valueMode === 'formatted'" @click="toggleValueMode">
              {{ valueMode === 'formatted' ? '原始' : '格式化' }}
            </Button>
            <Button v-if="valueLong" variant="outline" size="sm" class="h-6 px-2 text-[11px]" data-test="value-expand" type="button" @click="valueExpanded = !valueExpanded">
              {{ valueExpanded ? '收起' : '展开' }}
            </Button>
          </span>
        </div>
        <pre
          class="mt-1.5 whitespace-pre-wrap break-all rounded-md border border-border bg-muted/50 p-2.5 font-mono text-[13px] leading-relaxed text-foreground"
          :class="valueCollapsed ? 'collapsed max-h-[18em] overflow-hidden' : ''"
          data-test="value-body"
        ><template v-if="valueFormatted"><span v-for="(t, i) in valueView.tokens" :key="i" :class="'tok-' + t.type">{{ t.text }}</span></template><template v-else>{{ valueText }}</template></pre>
      </div>
      <div v-if="message.headers && message.headers.length" class="mt-4">
        <span class="text-xs uppercase tracking-wider text-muted-foreground">Headers</span>
        <pre class="mt-1.5 whitespace-pre-wrap break-all rounded-md border border-border bg-muted/50 p-2.5 font-mono text-[13px] leading-relaxed text-foreground" data-test="headers-body">{{ prettyJSON(JSON.stringify(message.headers)) }}</pre>
      </div>
    </div>
  </div>
</template>

<style scoped>
/* 玻璃抽屉壳:复合 backdrop-filter 与玻璃 token 难以用工具类表达,保留
   最小 scoped 壳(布局/边框/文字色已走语义 token);styles.css 迁移期共存。 */
.drawer {
  background: var(--glass-bg);
  -webkit-backdrop-filter: var(--glass-blur);
  backdrop-filter: var(--glass-blur);
  box-shadow: var(--glass-shadow);
}
/* JSON 语法高亮调色板:无对应语义 token,沿用 --json-* 主题变量。 */
.tok-key { color: var(--json-key); }
.tok-string { color: var(--json-string); }
.tok-number { color: var(--json-number); }
.tok-boolean { color: var(--json-boolean); }
.tok-null { color: var(--json-null); }
</style>
