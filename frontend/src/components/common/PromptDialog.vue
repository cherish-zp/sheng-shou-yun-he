<script setup lang="ts">
import { nextTick, ref, watch } from 'vue'
import { Button } from '@/components/ui/button'

const props = withDefaults(
  defineProps<{
    show: boolean
    title: string
    label: string
    value?: string | number
    confirmText?: string
    hint?: string
  }>(),
  { value: '', confirmText: '确认', hint: '' },
)

const emit = defineEmits<{
  (e: 'confirm', value: string): void
  (e: 'cancel'): void
}>()

const draft = ref('')
const inputEl = ref<HTMLInputElement | null>(null)

watch(
  () => props.show,
  (show) => {
    if (show) {
      draft.value = String(props.value ?? '')
      // Autofocus after the teleported dialog mounts so typing can start
      // immediately.
      void nextTick(() => inputEl.value?.focus())
    }
  },
  { immediate: true },
)

const canConfirm = (): boolean => draft.value.trim().length > 0

function confirm(): void {
  if (!canConfirm()) return
  emit('confirm', draft.value.trim())
}

function onKeydown(e: KeyboardEvent): void {
  if (e.key === 'Enter') confirm()
  if (e.key === 'Escape') emit('cancel')
}
</script>

<template>
  <Teleport to="body">
    <!-- 遮罩不负责关闭(与设置面板同一规范):仅按钮与 Esc 取消。 -->
    <div v-if="show" class="fixed inset-0 z-[1200] flex items-center justify-center bg-black/50" data-test="prompt-overlay">
      <div
        class="flex w-[320px] max-w-[calc(100vw-48px)] flex-col gap-3 rounded-lg border border-border bg-background p-4 text-foreground shadow-lg"
        data-test="prompt-dialog"
      >
        <div class="text-sm font-semibold" data-test="prompt-title">{{ title }}</div>
        <label class="flex flex-col gap-1">
          <span class="text-xs text-muted-foreground">{{ label }}</span>
          <input
            ref="inputEl"
            v-model="draft"
            class="w-full rounded-md border border-input bg-transparent px-3 py-1.5 font-mono text-[13px] shadow-xs outline-none transition-[color,box-shadow] placeholder:text-muted-foreground focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50"
            type="text"
            data-test="prompt-input"
            autocapitalize="off"
            autocorrect="off"
            autocomplete="off"
            spellcheck="false"
            @keydown="onKeydown"
          />
        </label>
        <div v-if="hint" class="text-xs text-muted-foreground/70" data-test="prompt-hint">{{ hint }}</div>
        <div class="flex justify-end gap-2">
          <Button variant="outline" size="sm" class="h-8" type="button" data-test="prompt-cancel" @click="emit('cancel')">取消</Button>
          <Button size="sm" class="h-8" type="button" data-test="prompt-confirm" :disabled="!canConfirm()" @click="confirm">
            {{ confirmText }}
          </Button>
        </div>
      </div>
    </div>
  </Teleport>
</template>
