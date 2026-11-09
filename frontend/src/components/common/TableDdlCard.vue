<script setup lang="ts">
import { nextTick, onMounted, ref, watch } from 'vue'

// Ctrl+表名查看建表语句的浮层卡片:hover 预览态(无按钮)与 pinned 固定态
// (复制 + 关闭)共用同一组件,由 pinned prop 区分。定位为 client 坐标的
// fixed 浮层,挂载与内容变化后按实测尺寸做视口钳位(不超右/下边缘)。
const props = defineProps<{
  pinned: boolean
  loading: boolean
  error: string
  ddl: string
  tableName: string
  x: number
  y: number
}>()

const emit = defineEmits<{
  (e: 'close'): void
  (e: 'copy'): void
}>()

const rootEl = ref<HTMLElement | null>(null)
const pos = ref({ left: props.x, top: props.y })

async function clampToViewport(): Promise<void> {
  pos.value = { left: props.x, top: props.y }
  await nextTick()
  const w = rootEl.value?.offsetWidth ?? 0
  const h = rootEl.value?.offsetHeight ?? 0
  const margin = 8
  pos.value = {
    left: Math.max(margin, Math.min(props.x, window.innerWidth - w - margin)),
    top: Math.max(margin, Math.min(props.y, window.innerHeight - h - margin)),
  }
}

onMounted(() => {
  void clampToViewport()
})

watch(
  () => [props.x, props.y, props.tableName, props.ddl, props.error, props.loading],
  () => {
    void clampToViewport()
  },
)
</script>

<template>
  <div
    ref="rootEl"
    class="ddl-card bg-background/95 shadow-lg backdrop-blur-md"
    data-test="ddl-card"
    :style="{ left: `${pos.left}px`, top: `${pos.top}px` }"
  >
    <div class="ddl-head">
      <span class="ddl-title" data-test="ddl-title">{{ tableName }} 建表语句</span>
      <span v-if="pinned" class="ddl-actions">
        <button type="button" class="ddl-copy" data-test="ddl-copy" @click="emit('copy')">复制 DDL</button>
        <button type="button" class="ddl-close" data-test="ddl-close" title="关闭" @click="emit('close')">×</button>
      </span>
    </div>
    <div v-if="loading" class="ddl-state" data-test="ddl-loading">加载中…</div>
    <div v-else-if="error" class="ddl-state err" data-test="ddl-error">{{ error }}</div>
    <pre v-else class="ddl-body" data-test="ddl-body">{{ ddl }}</pre>
  </div>
</template>

<style scoped>
/* 玻璃壳(bg-background/95 + backdrop-blur-md + shadow-lg)走模板 Tailwind 类;
   其余为布局/边框与语义 token 取色。 */
.ddl-card {
  position: fixed;
  z-index: 60;
  max-width: 640px;
  max-height: 60vh;
  display: flex;
  flex-direction: column;
  border: 1px solid var(--input);
  border-radius: var(--radius);
  overflow: hidden;
  font-family: var(--font-sans);
  color: var(--foreground);
}
.ddl-head {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 7px 12px;
  border-bottom: 1px solid var(--border);
  flex: none;
}
.ddl-title {
  font-size: 12px;
  font-weight: 600;
  color: var(--muted-foreground);
  font-family: var(--font-mono);
}
.ddl-actions {
  margin-left: auto;
  display: inline-flex;
  align-items: center;
  gap: 4px;
}
.ddl-copy {
  border: 1px solid var(--border);
  background: transparent;
  color: var(--foreground);
  font-size: 12px;
  font-family: var(--font-sans);
  padding: 2px 8px;
  border-radius: 6px;
  cursor: pointer;
  transition: background 0.12s ease, border-color 0.12s ease, color 0.12s ease;
}
.ddl-copy:hover {
  background: color-mix(in srgb, var(--primary) 12%, transparent);
  border-color: var(--primary);
  color: var(--primary);
}
.ddl-close {
  border: none;
  background: transparent;
  color: var(--muted-foreground);
  font-size: 14px;
  line-height: 1;
  cursor: pointer;
  padding: 2px 7px;
  border-radius: 5px;
}
.ddl-close:hover {
  background: var(--primary);
  color: var(--foreground);
}
.ddl-state {
  padding: 12px;
  font-size: 13px;
  color: color-mix(in srgb, var(--muted-foreground) 70%, transparent);
  flex: 1;
  min-height: 0;
}
.ddl-state.err {
  background: color-mix(in srgb, var(--destructive) 12%, transparent);
  color: var(--destructive);
  font-family: var(--font-mono);
  font-size: 12px;
  white-space: pre-wrap;
  word-break: break-all;
}
/* DDL 滚动展示:复用 SqlResultCard .stmt-full 的 pre 风格 token(mono/12px/pre-wrap) */
.ddl-body {
  margin: 0;
  padding: 10px 12px;
  overflow: auto;
  font-family: var(--font-mono);
  font-size: 12px;
  line-height: 1.6;
  white-space: pre-wrap;
  word-break: break-all;
  color: var(--foreground);
  flex: 1;
  min-height: 0;
}
</style>
