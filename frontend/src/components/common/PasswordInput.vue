<script setup lang="ts">
import { ref } from 'vue'

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
  <div class="password-wrap">
    <input
      v-bind="$attrs"
      v-model="model"
      :type="show ? 'text' : 'password'"
      class="input password-input"
    />
    <button
      type="button"
      class="eye-btn"
      tabindex="-1"
      :data-test="toggleTestid"
      :aria-label="show ? '隐藏密码' : '显示密码'"
      @click="show = !show"
    >
      <svg v-if="show" viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
        <path d="M17.94 17.94A10.07 10.07 0 0 1 12 20c-7 0-11-8-11-8a18.45 18.45 0 0 1 5.06-5.94M9.9 4.24A9.12 9.12 0 0 1 12 4c7 0 11 8 11 8a18.5 18.5 0 0 1-2.16 3.19m-6.72-1.07a3 3 0 1 1-4.24-4.24" />
        <line x1="1" y1="1" x2="23" y2="23" />
      </svg>
      <svg v-else viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
        <path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z" />
        <circle cx="12" cy="12" r="3" />
      </svg>
    </button>
  </div>
</template>

<style scoped>
.password-wrap { position: relative; }
/* 输入框基础样式与各表单的 .input 保持一致(父组件 scoped 样式够不到这里)。 */
.input {
  width: 100%; box-sizing: border-box;
  background: var(--bg-subtle); border: 1px solid var(--border); border-radius: 7px;
  color: var(--text); padding: 8px 10px; font-size: 13px;
  transition: border-color 0.15s ease, background 0.15s ease, box-shadow 0.15s ease;
}
.input:focus { outline: none; border-color: var(--accent); background: var(--bg-elevated); box-shadow: 0 0 0 3px var(--accent-soft); }
/* 右侧留白加大,避免输入文字被眼睛图标遮挡。 */
.password-input { padding-right: 34px; }
.eye-btn {
  position: absolute; right: 6px; top: 50%; transform: translateY(-50%);
  display: flex; align-items: center; justify-content: center;
  width: 24px; height: 24px; padding: 0; border: none; background: none;
  color: var(--text-tertiary); cursor: pointer; border-radius: 5px;
}
.eye-btn:hover { color: var(--text); background: var(--bg-hover); }
</style>
