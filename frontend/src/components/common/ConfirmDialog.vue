<script setup lang="ts">
import { Button } from '@/components/ui/button'

const props = withDefaults(
  defineProps<{
    show: boolean
    message: string
    confirmText?: string
    danger?: boolean
    /** true 时禁用确认按钮(如删除条件命中多行、定位不唯一的危险兜底)。 */
    confirmDisabled?: boolean
  }>(),
  { confirmText: '删除', danger: true, confirmDisabled: false },
)
const emit = defineEmits<{ (e: 'confirm'): void; (e: 'cancel'): void }>()

function close(): void {
  emit('cancel')
}

// 禁用态兜底:合成事件/重复触发绕过原生 disabled 时也不 emit confirm。
function onConfirm(): void {
  if (props.confirmDisabled) return
  emit('confirm')
}
</script>

<template>
  <!-- Teleport to <body>: the sidebar uses backdrop-filter, which would
       otherwise become the containing block for position:fixed and confine
       the dialog to the scrolled sidebar box. Teleporting keeps the dialog
       viewport-centered no matter where the delete action was triggered. -->
  <Teleport to="body">
    <div v-if="show" class="fixed inset-0 z-[100] flex items-center justify-center bg-black/50 backdrop-blur-[2px]" data-test="confirm-dialog" @click.self="close">
      <div class="flex w-[380px] max-w-[calc(100vw-48px)] flex-col overflow-hidden rounded-lg border border-border bg-background text-foreground shadow-lg">
        <div class="flex items-center justify-between border-b border-border px-[18px] py-3.5">
          <span class="text-sm font-semibold">确认操作</span>
          <button class="rounded-md p-0.5 text-muted-foreground transition-colors hover:bg-muted hover:text-foreground" type="button" data-test="confirm-dialog-close" @click="close">✕</button>
        </div>
        <div class="px-[18px] py-[18px]">
          <p class="m-0 whitespace-pre-wrap break-all text-[13px] leading-relaxed" data-test="confirm-dialog-message">{{ message }}</p>
        </div>
        <div class="flex justify-end gap-2 px-[18px] pb-4">
          <Button variant="outline" type="button" data-test="confirm-dialog-cancel" @click="close">取消</Button>
          <!-- danger 类保留:既有 spec 以 classList.contains('danger') 断言确认按钮样式。 -->
          <Button
            :variant="props.danger ? 'destructive' : 'default'"
            :class="{ danger: props.danger }"
            type="button"
            data-test="confirm-dialog-ok"
            :disabled="props.confirmDisabled"
            @click="onConfirm"
          >
            {{ confirmText }}
          </Button>
        </div>
      </div>
    </div>
  </Teleport>
</template>
