<script setup lang="ts">
// Shared export dropdown used by the message browser and the SQL console: a
// toggle button that reveals CSV/JSONL choices and closes on outside clicks.
// The picked format is emitted upward so each caller supplies its own
// filename and content through its exportAs handler.
//
// 弹出组用 DropdownMenu(reka 原语)承载;Content 特意取 reka 原生组件而非
// ui/dropdown-menu 的封装——封装内置 Portal 会把菜单 DOM 移到 body,而既有
// 测试(MessageBrowser.spec)用 wrapper.find 断言菜单项存在性,原地渲染对
// 既有测试破坏最小。菜单开关/点外关闭/选中后收起全部交给 reka。
import { ref } from 'vue'
import { DropdownMenu, DropdownMenuTrigger } from '@/components/ui/dropdown-menu'
import { DropdownMenuItem } from '@/components/ui/dropdown-menu'
import { Button } from '@/components/ui/button'
import { ChevronDown, FileDown, FileJson, FileSpreadsheet } from 'lucide-vue-next'
// reka 原生 Content:原地渲染,不用 ui 封装的 Portal 版(见上)。
import { DropdownMenuContent } from 'reka-ui'

defineProps<{ disabled?: boolean }>()

// 导出格式:xlsx 走后端 BuildResultXLSX 绑定生成工作簿字节,CSV/JSONL 仍由
// 前端拼字符串;picked 格式向上 emit,文件名与保存通道由调用方决定。
const emit = defineEmits<{ export: [format: 'csv' | 'jsonl' | 'xlsx'] }>()

// 开关状态受控:选中后同 tick 同步收起(reka 的 select→onClose 走下一个
// 渲染周期,既有测试在单次 await 后即断言菜单已收起,故显式置 false)。
const open = ref(false)

// pick reports the chosen format and closes the menu synchronously.
function pick(format: 'csv' | 'jsonl' | 'xlsx'): void {
  open.value = false
  emit('export', format)
}
</script>

<template>
  <div class="export-menu relative" data-test="export-menu">
    <DropdownMenu v-model:open="open">
      <DropdownMenuTrigger as-child>
        <Button variant="ghost" size="sm" type="button" class="h-7 px-2 text-xs" data-test="export-toggle" :disabled="disabled">
          导出
          <ChevronDown class="size-3" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent
        align="end"
        :side-offset="6"
        class="z-30 min-w-[110px] rounded-md border border-border bg-popover p-1 shadow-md"
      >
        <DropdownMenuItem class="cursor-pointer gap-2 text-[13px]" data-test="export-csv" @select="pick('csv')">
          <FileDown class="size-3.5 text-muted-foreground" />
          CSV
        </DropdownMenuItem>
        <DropdownMenuItem class="cursor-pointer gap-2 text-[13px]" data-test="export-jsonl" @select="pick('jsonl')">
          <FileJson class="size-3.5 text-muted-foreground" />
          JSONL
        </DropdownMenuItem>
        <DropdownMenuItem class="cursor-pointer gap-2 text-[13px]" data-test="result-export-xlsx" @select="pick('xlsx')">
          <FileSpreadsheet class="size-3.5 text-muted-foreground" />
          XLSX
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  </div>
</template>
