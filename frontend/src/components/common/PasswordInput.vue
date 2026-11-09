<script setup lang="ts">
import { ref } from 'vue'
import { Eye, EyeOff } from 'lucide-vue-next'

// toggleTestid 渲染为眼睛按钮上的 data-test;其余未声明属性
// (data-test、placeholder、autocomplete 等)经 $attrs 透传到内部 input。
defineOptions({ inheritAttrs: false })

defineProps<{ toggleTestid?: string }>()

// defineModel:父组件用 v-model 绑定密码值,输入/回填/重置双向同步。
const model = defineModel<string>({ default: '' })

// 显示/隐藏状态由组件内部管理:各实例独立切换,默认密文。
const show = ref(false)
</script>

<template>
  <div class="relative">
    <input
      v-bind="$attrs"
      v-model="model"
      :type="show ? 'text' : 'password'"
      class="password-input w-full rounded-md border border-input bg-transparent px-3 py-1 pr-9 text-[13px] shadow-xs outline-none transition-[color,box-shadow] placeholder:text-muted-foreground focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50 disabled:pointer-events-none disabled:cursor-not-allowed disabled:opacity-50"
    />
    <!-- 右侧留白加大(pr-9),避免输入文字被眼睛图标遮挡。 -->
    <button
      type="button"
      class="absolute right-1.5 top-1/2 flex size-6 -translate-y-1/2 items-center justify-center rounded-[5px] p-0 text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
      tabindex="-1"
      :data-test="toggleTestid"
      :aria-label="show ? '隐藏密码' : '显示密码'"
      @click="show = !show"
    >
      <EyeOff v-if="show" class="size-4" aria-hidden="true" />
      <Eye v-else class="size-4" aria-hidden="true" />
    </button>
  </div>
</template>
