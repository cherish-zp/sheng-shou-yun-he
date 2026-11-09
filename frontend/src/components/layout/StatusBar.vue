<script setup lang="ts">
import { computed } from 'vue'
import { useConnectionsStore } from '@/store/connections'
import { APP_VERSION } from '@/version'
import { Badge } from '@/components/ui/badge'

// 生产安全模式(全局只读)标识:状态由 Layout 加载 App 设置后传入,开启时
// 在状态栏展示「只读」Badge,提醒当前所有写操作已被后端全局拒绝。
const props = defineProps<{ readonly?: boolean }>()

// The bottom status bar reads the reactive connections store directly, so
// counts and the latest error update in place whenever the store changes —
// no manual refresh and no data-source-specific logic (works for Kafka and
// future MySQL/ES backends alike).
const store = useConnectionsStore()

// total counts every saved connection; online only counts connections whose
// status is exactly 'connected' — 'connecting' and other transient states are
// neither online nor offline.
const total = computed(() => store.connections.length)
const online = computed(() => Object.values(store.statusById).filter((s) => s === 'connected').length)
</script>

<template>
  <!-- DBX 化:flat 底(bg-muted/30)+ 语义 token 文字,数字列用 tabular-nums
       对齐;错误消息截断展示,版本/署名恒定不换行。 -->
  <footer
    class="flex flex-none items-center justify-between gap-4 border-t border-border bg-muted/30 px-4 py-1 text-xs text-muted-foreground"
    data-test="status-bar"
  >
    <span class="tabular-nums whitespace-nowrap" data-test="status-count">连接 {{ total }} · 在线 {{ online }}</span>
    <span class="flex min-w-0 items-center gap-2">
      <span v-if="store.error" class="min-w-0 truncate text-destructive" data-test="status-error">最近错误：{{ store.error }}</span>
      <Badge v-if="props.readonly" variant="destructive" class="px-1.5 py-0 text-[10px]" data-test="status-readonly">只读</Badge>
      <span class="tabular-nums whitespace-nowrap text-muted-foreground/70" data-test="status-version">v{{ APP_VERSION }}</span>
      <span class="whitespace-nowrap text-muted-foreground/70" data-test="status-author">By Mr Zp</span>
    </span>
  </footer>
</template>
