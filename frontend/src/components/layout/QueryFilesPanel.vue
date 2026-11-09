<script lang="ts">
// 激活的 SQL 控制台(Kafka 或 ClickHouse)暴露给 SQL文件面板的操作集合。
// 面板只负责触发:确认弹窗与保存/删除逻辑都由控制台内部实现。
export interface SqlConsoleApi {
  requestSave(): void // 保存(未关联文件→弹名称;已关联→覆盖)
  requestSaveAs(): void // 另存为
  loadQueryFile(name: string): void // 载入(组件内部处理未保存确认)
  askRemoveCurrentFile(): void // 删除当前关联文件(内部弹确认)
  currentFile(): string | null // 当前关联文件名(含 .sql)
}
</script>

<script setup lang="ts">
import { onMounted } from 'vue'
import { FileText, Save, SaveAll, Trash2 } from 'lucide-vue-next'
import { Button } from '@/components/ui/button'
import type { ConnectionType } from '@/api/types'
import { useConnectionsStore } from '@/store/connections'
import { useQueryFiles } from '@/composables/queryFiles'

const props = defineProps<{ consoleApi: SqlConsoleApi | null }>()

// 点击条目统一上报 open(由 Layout 按文件归属自动打开/切换 SQL 控制台并载入,
// 无控制台也不例外);面板自身不决定能否打开。
const emit = defineEmits<{
  (e: 'open', name: string, connectionId: string): void
}>()
// 列表-only 用法:面板只消费共享 files/fileError,不需要 getContent/setContent。
const { files, fileError, refreshFiles } = useQueryFiles({ connectionId: () => '' })

// 连接 id → 「类型 · 名称」归属:条目上直观显示 SQL 文件属于哪个数据源。
// 直接读全局连接 store(App.vue 挂载即 load,新建/编辑/删除连接时实时更新),
// 面板常开期间新建的连接也能立刻解析归属,无需本地缓存与重拉。
const connStore = useConnectionsStore()

const TYPE_LABELS: Record<ConnectionType, string> = {
  kafka: 'Kafka',
  mysql: 'MySQL',
  tidb: 'TiDB',
  postgres: 'PostgreSQL',
  es: 'ES',
  redis: 'Redis',
  clickhouse: 'ClickHouse',
  hive: 'Hive',
}

function connLabel(connectionId: string): string {
  if (!connectionId) return '未关联连接'
  const c = connStore.connections.find((x) => x.id === connectionId)
  return c ? `${TYPE_LABELS[c.type] ?? c.type} · ${c.name}` : '未知连接'
}

onMounted(() => {
  void refreshFiles()
})

// 暴露给 Layout:面板展开(含首次展开)时主动刷新文件列表。
// 连接列表无需重拉:归属改读全局 store,连接增删改时 store 实时更新,
// 面板常开期间新建的连接也能正确解析归属。
function refresh(): void {
  void refreshFiles()
}
defineExpose({ refresh })

function onItemSelect(name: string, connectionId: string): void {
  emit('open', name, connectionId)
}

function onSave(): void {
  props.consoleApi?.requestSave()
}

function onSaveAs(): void {
  props.consoleApi?.requestSaveAs()
}

function onDelete(): void {
  props.consoleApi?.askRemoveCurrentFile()
}
</script>

<template>
  <div class="flex h-full flex-col text-[13px]" data-test="files-panel">
    <div class="flex flex-none items-center gap-1.5 px-3 pt-3 pb-2 font-semibold text-foreground">
      <FileText class="size-3.5 text-muted-foreground" />
      SQL文件
    </div>

    <div class="flex flex-none gap-1.5 px-3 pb-2">
      <Button
        variant="outline"
        size="sm"
        type="button"
        class="h-7 flex-1 cursor-pointer text-xs"
        data-test="files-save"
        :disabled="!consoleApi"
        @click="onSave"
      >
        <Save class="size-3" />
        保存
      </Button>
      <Button
        variant="outline"
        size="sm"
        type="button"
        class="h-7 flex-1 cursor-pointer text-xs"
        data-test="files-save-as"
        :disabled="!consoleApi"
        @click="onSaveAs"
      >
        <SaveAll class="size-3" />
        另存为
      </Button>
      <Button
        variant="outline"
        size="sm"
        type="button"
        class="h-7 flex-1 cursor-pointer text-xs"
        data-test="files-file-delete"
        :disabled="!consoleApi"
        @click="onDelete"
      >
        <Trash2 class="size-3" />
        删除
      </Button>
    </div>

    <div v-if="fileError" class="mx-3 mb-2 flex-none rounded-md bg-destructive/10 px-2 py-1.5 text-xs break-all text-destructive" data-test="files-error">
      {{ fileError }}
    </div>

    <div class="flex min-h-0 flex-1 flex-col gap-0.5 overflow-auto px-2 pb-2.5">
      <!-- 文件名与归属行两个 span 必须保持相邻行书写:Vue 模板空白压缩会把
           换行空白节点移除,条目 text() 才能是「文件名+归属」连续文本。 -->
      <button
        v-for="(f, i) in files"
        :key="f.name"
        type="button"
        class="flex w-full flex-none cursor-pointer flex-col items-start gap-px rounded-md px-2 py-1.5 text-left text-xs transition-colors hover:bg-accent"
        :class="
          consoleApi?.currentFile() === f.name
            ? 'active bg-accent font-medium text-foreground'
            : 'text-muted-foreground hover:text-foreground'
        "
        :data-test="`files-item-${i}`"
        :title="connLabel(f.connection_id)"
        @click="onItemSelect(f.name, f.connection_id)"
      >
        <span class="w-full truncate">{{ f.name }}</span>
        <span
          class="w-full truncate text-[11px]"
          :class="consoleApi?.currentFile() === f.name ? 'text-foreground/70' : 'text-muted-foreground/80'"
          :data-test="`files-meta-${i}`"
        >{{ connLabel(f.connection_id) }}</span>
      </button>
      <div v-if="!files.length" class="flex-none px-2 py-3.5 text-center text-xs text-muted-foreground" data-test="files-empty">暂无查询文件</div>
    </div>
  </div>
</template>
