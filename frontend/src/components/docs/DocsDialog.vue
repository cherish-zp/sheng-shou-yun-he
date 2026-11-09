<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { docEntries, readLastDocId, renderDoc, writeLastDocId } from '@/docs'

const props = defineProps<{ show: boolean }>()
const emit = defineEmits<{ (e: 'close'): void }>()

// 当前篇章:打开时恢复上次看过的篇(localStorage 记忆),无效或缺失回落第一篇。
const currentId = ref(docEntries[0]?.id ?? '')

watch(
  () => props.show,
  (show) => {
    if (!show) return
    const last = readLastDocId()
    currentId.value = docEntries.some((d) => d.id === last) ? last : (docEntries[0]?.id ?? '')
  },
  { immediate: true },
)

const current = computed(() => docEntries.find((d) => d.id === currentId.value) ?? docEntries[0])
// 内容为内置可信文档且渲染器已做 HTML 转义,v-html 安全。
const renderedHtml = computed(() => renderDoc(current.value?.markdown ?? ''))

function select(id: string): void {
  currentId.value = id
  writeLastDocId(id)
}

// Esc 关闭:组件级监听,仅 show 时响应,卸载时移除。
function onKeydown(e: KeyboardEvent): void {
  if (props.show && e.key === 'Escape') emit('close')
}

onMounted(() => window.addEventListener('keydown', onKeydown))
onBeforeUnmount(() => window.removeEventListener('keydown', onKeydown))
</script>

<template>
  <Teleport to="body">
    <div v-if="show" class="fixed inset-0 z-[1200] flex items-center justify-center bg-black/50">
      <div
        class="flex h-[70vh] w-[760px] max-w-[calc(100vw-48px)] flex-col gap-3 rounded-lg border border-border bg-background p-4 text-foreground shadow-lg"
        data-test="docs-dialog"
      >
        <div class="flex items-center justify-between">
          <span class="text-[15px] font-semibold">使用文档</span>
          <button class="rounded-md px-1.5 py-0.5 text-muted-foreground transition-colors hover:bg-muted hover:text-foreground" type="button" data-test="btn-docs-close" @click="emit('close')">✕</button>
        </div>
        <div class="flex min-h-0 flex-1 gap-3.5">
          <aside class="flex w-[150px] flex-none flex-col gap-1 border-r border-border pr-3" data-test="docs-toc">
            <button
              v-for="d in docEntries"
              :key="d.id"
              class="rounded-md px-2.5 py-[7px] text-left text-[13px] transition-colors"
              :class="d.id === currentId ? 'active bg-accent font-semibold text-accent-foreground' : 'text-muted-foreground hover:bg-muted hover:text-foreground'"
              type="button"
              :data-test="`docs-toc-item-${d.id}`"
              @click="select(d.id)"
            >
              {{ d.title }}
            </button>
          </aside>
          <!-- 内置可信文档,渲染前已转义。 -->
          <!-- eslint-disable-next-line vue/no-v-html -->
          <article class="docs-content min-w-0 flex-1 overflow-auto text-[13px] leading-relaxed" data-test="docs-content" v-html="renderedHtml"></article>
        </div>
      </div>
    </div>
  </Teleport>
</template>

<style scoped>
/* 弹窗骨架走 Tailwind 语义 token;v-html 注入的 Markdown 元素无法挂 Tailwind
   类,保留 :deep 规则并全部改用 shadcn 语义令牌。 */
.docs-content :deep(h1) { font-size: 18px; font-weight: 600; margin: 4px 0 10px; }
.docs-content :deep(h2) {
  font-size: 15px;
  font-weight: 600;
  margin: 18px 0 8px;
  padding-bottom: 4px;
  border-bottom: 1px solid var(--border);
}
.docs-content :deep(h3) { font-size: 13.5px; font-weight: 600; margin: 14px 0 6px; }
.docs-content :deep(p) { margin: 6px 0; }
.docs-content :deep(ul),
.docs-content :deep(ol) { margin: 6px 0; padding-left: 20px; }
.docs-content :deep(li) { margin: 3px 0; }
.docs-content :deep(strong) { font-weight: 600; }
.docs-content :deep(code) {
  font-family: var(--font-mono);
  font-size: 12px;
  background: var(--muted);
  border: 1px solid var(--border);
  border-radius: 4px;
  padding: 1px 5px;
}
.docs-content :deep(pre) {
  background: var(--muted);
  border: 1px solid var(--border);
  border-radius: 8px;
  padding: 10px 12px;
  overflow: auto;
}
.docs-content :deep(pre code) { background: none; border: none; padding: 0; }
.docs-content :deep(table) {
  border-collapse: collapse;
  width: 100%;
  margin: 8px 0;
  font-size: 12.5px;
}
.docs-content :deep(th),
.docs-content :deep(td) {
  border: 1px solid var(--border);
  padding: 6px 10px;
  text-align: left;
}
.docs-content :deep(th) { background: var(--muted); font-weight: 600; }
</style>
