<script setup lang="ts">
import type { PartitionLag, ResetPreviewRow } from '@/api/types'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'

defineProps<{ rows: PartitionLag[]; loading?: boolean; preview?: ResetPreviewRow[] | null }>()

defineEmits<{ (e: 'confirm-reset'): void; (e: 'cancel-preview'): void }>()

// 表格基座 token 类;lagBadgeClass 输出语义色 Badge(lag-high 为 spec hook 类)。
const TH_CLS = 'border-b border-border bg-muted/50 px-3 py-2 text-left text-xs font-medium text-muted-foreground'
const TD_CLS = 'px-3 py-1.5'

function lagBadgeClass(lag: number): string {
  return lag > 0 ? 'lag-high border-transparent bg-warning-bg text-warning' : 'border-transparent bg-muted text-muted-foreground'
}
</script>

<template>
  <div class="mt-3.5 overflow-hidden rounded-lg border border-border bg-card">
    <div class="border-b border-border px-3 py-2.5 font-semibold text-foreground">消费滞后（Lag）</div>
    <table class="w-full border-collapse text-[13px]" data-test="lag-table">
      <thead>
        <tr>
          <th :class="TH_CLS">Partition</th>
          <th :class="TH_CLS">Current Offset</th>
          <th :class="TH_CLS">Log End Offset</th>
          <th :class="TH_CLS">Lag</th>
          <th :class="TH_CLS">Host</th>
          <th :class="TH_CLS">Consumer ID</th>
          <th :class="TH_CLS">Client ID</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="l in rows" :key="l.partition" class="border-b border-border last:border-b-0" data-test="lag-row">
          <td class="tabular-nums" :class="TD_CLS">{{ l.partition }}</td>
          <td class="font-mono tabular-nums" :class="TD_CLS">{{ l.current_offset }}</td>
          <td class="font-mono tabular-nums" :class="TD_CLS">{{ l.log_end_offset }}</td>
          <td :class="TD_CLS">
            <Badge class="font-mono tabular-nums" :class="lagBadgeClass(l.lag)" data-test="lag-value">{{ l.lag }}</Badge>
          </td>
          <td :class="TD_CLS" data-test="lag-client-host">{{ l.client_host || '—' }}</td>
          <td class="font-mono" :class="TD_CLS" data-test="lag-member-id">{{ l.member_id || '—' }}</td>
          <td class="font-mono" :class="TD_CLS" data-test="lag-client-id">{{ l.client_id || '—' }}</td>
        </tr>
        <tr v-if="rows.length === 0 && !loading">
          <td colspan="7" class="px-3 py-5 text-center text-muted-foreground" data-test="lag-empty">暂无 lag 数据</td>
        </tr>
      </tbody>
    </table>

    <div v-if="preview" class="border-t border-border" data-test="dry-run">
      <div class="px-3 py-2.5 text-[13px] font-semibold text-warning">重置预览（Dry-run）— 请确认以下分区将被重置</div>
      <table class="w-full border-collapse text-[13px]" data-test="dry-run-table">
        <thead>
          <tr>
            <th :class="TH_CLS">Partition</th>
            <th :class="TH_CLS">Current Offset</th>
            <th :class="TH_CLS">New Offset</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="p in preview" :key="p.partition" class="border-b border-border last:border-b-0" data-test="dry-run-row">
            <td class="tabular-nums" :class="TD_CLS">{{ p.partition }}</td>
            <td class="font-mono tabular-nums" :class="TD_CLS">{{ p.current_offset }}</td>
            <td class="font-mono tabular-nums" :class="TD_CLS" data-test="dry-run-new-offset">{{ p.new_offset ?? '—' }}</td>
          </tr>
        </tbody>
      </table>
      <div class="flex gap-2 px-3 py-3">
        <Button variant="destructive" size="sm" type="button" data-test="btn-confirm-reset" :disabled="!preview || preview.length === 0" @click="$emit('confirm-reset')">确认重置</Button>
        <Button variant="ghost" size="sm" type="button" data-test="btn-cancel-preview" @click="$emit('cancel-preview')">取消</Button>
      </div>
    </div>
  </div>
</template>
