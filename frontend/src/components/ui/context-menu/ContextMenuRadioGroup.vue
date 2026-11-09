<script setup lang="ts">
import type { ContextMenuRadioGroupProps } from 'reka-ui'
import { ContextMenuRadioGroup, useForwardProps } from 'reka-ui'

const props = defineProps<ContextMenuRadioGroupProps>()

const forwardedProps = useForwardProps(props)
</script>

<template>
  <ContextMenuRadioGroup
    data-slot="context-menu-radio-group"
    v-bind="forwardedProps"
  >
    <slot />
  </ContextMenuRadioGroup>
</template>
