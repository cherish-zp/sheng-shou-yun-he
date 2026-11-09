<script setup lang="ts">
import type { DropdownMenuRadioGroupProps } from 'reka-ui'
import { DropdownMenuRadioGroup, useForwardProps } from 'reka-ui'

const props = defineProps<DropdownMenuRadioGroupProps>()

const forwardedProps = useForwardProps(props)
</script>

<template>
  <DropdownMenuRadioGroup
    data-slot="dropdown-menu-radio-group"
    v-bind="forwardedProps"
  >
    <slot />
  </DropdownMenuRadioGroup>
</template>
