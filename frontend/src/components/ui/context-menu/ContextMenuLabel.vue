<script setup lang="ts">
import type { ContextMenuLabelProps } from 'reka-ui'
import type { HTMLAttributes } from 'vue'
import { computed } from 'vue'
import { ContextMenuLabel, useForwardProps } from 'reka-ui'
import { cn } from '@/lib/utils'

const props = defineProps<ContextMenuLabelProps & {
  class?: HTMLAttributes['class']
  inset?: boolean
}>()

const delegatedProps = computed(() => {
  const { class: _, inset: _inset, ...delegated } = props
  return delegated
})

const forwardedProps = useForwardProps(delegatedProps)
</script>

<template>
  <ContextMenuLabel
    data-slot="context-menu-label"
    :data-inset="inset"
    v-bind="forwardedProps"
    :class="cn('text-foreground px-2 py-1.5 text-sm font-medium data-[inset]:pl-8', props.class)"
  >
    <slot />
  </ContextMenuLabel>
</template>
