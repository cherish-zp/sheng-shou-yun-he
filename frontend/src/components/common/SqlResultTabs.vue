<script setup lang="ts">
// 结果区 Tab 条:三个 SQL 控制台(MySQL/CH/ES)共用。每个语句结果一个 tab,
// Tab 条下方只渲染 active 对应的那张结果卡(由调用方负责)。
// label:语句前置注释文本(无注释回退「结果 N」,由调用方生成);
// status:running(发起时)/ ok / fail / none,驱动状态点配色;
// title:悬浮提示(语句单行摘要,由调用方生成)。
export interface ResultTabItem {
  label: string
  status: 'ok' | 'fail' | 'running' | 'none'
  title?: string
}

defineProps<{ tabs: ResultTabItem[]; active: number }>()

const emit = defineEmits<{
  (e: 'select', index: number): void
}>()

// 状态点配色:status 类名(ok/fail/running/none)保留为测试锚定,配色走
// 语义 token;running 用 animate-pulse 呼吸提示进行中。
function dotClass(status: ResultTabItem['status']): string {
  switch (status) {
    case 'ok':
      return 'bg-success'
    case 'fail':
      return 'bg-destructive'
    case 'running':
      return 'bg-info animate-pulse'
    default:
      return 'bg-muted-foreground/50'
  }
}
</script>

<template>
  <!-- DBX 皮肤:active tab bg-accent + border-b-2 border-primary,hover 反馈
       bg-accent/50;active/状态类名保留为测试锚定。 -->
  <div class="result-tabs flex flex-none items-stretch gap-0.5 overflow-x-auto border-b border-border px-2 pt-1 [scrollbar-width:thin]" data-test="result-tabs">
    <button
      v-for="(t, i) in tabs"
      :key="i"
      type="button"
      class="result-tab inline-flex flex-none items-center gap-1.5 text-xs transition-colors whitespace-nowrap"
      :class="
        i === active
          ? 'active rounded-t-md border-b-2 border-primary bg-accent/60 px-2.5 py-1 font-semibold text-foreground'
          : 'rounded-t-md border-b-2 border-transparent px-2.5 py-1 text-muted-foreground hover:bg-accent/50 hover:text-foreground'
      "
      :data-test="`result-tab-${i}`"
      :title="t.title"
      @click="emit('select', i)"
    >
      <span class="tab-dot size-[7px] shrink-0 rounded-full" :class="[t.status, dotClass(t.status)]"></span>
      <span class="min-w-0 overflow-hidden text-ellipsis">{{ t.label }}</span>
    </button>
  </div>
</template>
