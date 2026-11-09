<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { getApi } from '@/api/client'
import type { RedisKeyInfo, RedisListPushRequest, RedisServerInfo, RedisValue } from '@/api/types'
import { formatBytes } from '@/utils/bytes'
import { isJsonContainer, prettyJSON } from '@/utils/format'
import { useToastStore } from '@/store/toast'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'

const props = defineProps<{ connectionId: string; db: number; keyLimit?: number }>()

// 键列表状态(SCAN 游标分页)
const keys = ref<RedisKeyInfo[]>([])
const cursor = ref(0)
const match = ref('')
const loading = ref(false)
const error = ref<string | null>(null)
// SCAN 累积上限:无 match 全库扫描时防止结果无限累积撑爆响应式数组与 DOM。
// 达到上限后停止追加与续扫;下一次 scan(true)(切换库/改 match/重扫)即重置。
// 上限可经 keyLimit prop 注入(spec 用小上限全路径验证,不渲染两万行)。
const keyLimit = computed(() => props.keyLimit ?? 20000)
const limitReached = ref(false)
// 选中键与其值
const selectedKey = ref<string | null>(null)
const value = ref<RedisValue | null>(null)
const valueLoading = ref(false)
const editedString = ref('')
// JSON 字符串的格式化只读态:合法 JSON 默认格式化展示(只读),「编辑」
// 切回原始可编辑文本——保存永远写原始字节,不把格式化结果悄悄写回。
const stringPretty = ref(false)
const stringEditing = ref(false)
// 摘要与确认
const summary = ref<RedisServerInfo | null>(null)
const confirmDelete = ref(false)
const confirmFlush = ref(false)

async function scan(reset: boolean): Promise<void> {
  // 已达上限时拒绝继续加载更多(reset 语义的重扫不受限,会先重置上限)。
  if (!reset && limitReached.value) return
  loading.value = true
  error.value = null
  if (reset) limitReached.value = false
  try {
    const res = await getApi().redisScan({
      connection_id: props.connectionId,
      db: props.db,
      cursor: reset ? 0 : cursor.value,
      match: match.value.trim() || '*',
      count: 50,
    })
    const merged = reset ? res.keys : [...keys.value, ...res.keys]
    if (merged.length >= keyLimit.value) {
      // 截断到上限并清零游标:隐藏「加载更多」,界面给出缩小范围的提示。
      keys.value = merged.slice(0, keyLimit.value)
      limitReached.value = true
      cursor.value = 0
    } else {
      keys.value = merged
      cursor.value = res.cursor
    }
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e)
  } finally {
    loading.value = false
  }
}

async function loadSummary(): Promise<void> {
  try {
    summary.value = await getApi().redisServerInfo(props.connectionId)
  } catch {
    summary.value = null
  }
}

onMounted(() => {
  void scan(true)
  void loadSummary()
})

async function selectKey(k: RedisKeyInfo): Promise<void> {
  selectedKey.value = k.key
  valueLoading.value = true
  value.value = null
  try {
    value.value = await getApi().redisGetKey({
      connection_id: props.connectionId,
      db: props.db,
      key: k.key,
    })
    if (value.value.string !== undefined) applyString(value.value.string)
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e)
  } finally {
    valueLoading.value = false
  }
}

async function loadFullString(): Promise<void> {
  if (!selectedKey.value) return
  try {
    const full = await getApi().redisGetKey({
      connection_id: props.connectionId,
      db: props.db,
      key: selectedKey.value,
    })
    // 显式全量:忽略截断标记,直接使用完整字符串(后端此时仍会截断,
    // 因此这里通过 SetString 后再读取不可行;改为提示用户值过大)。
    if (full.truncated) {
      error.value = `值过大(${full.size_bytes} 字节),无法完整加载`
      return
    }
    value.value = full
    if (full.string !== undefined) applyString(full.string)
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e)
  }
}

// applyString 载入字符串原始值并决定默认展示形态:合法 JSON 对象/数组
// 默认进格式化只读视图,其余保持可编辑 textarea。
function applyString(raw: string): void {
  editedString.value = raw
  stringPretty.value = isJsonContainer(raw)
  stringEditing.value = false
}

// prettyString 非空 = 当前应渲染格式化只读视图。
const prettyString = computed(() =>
  stringPretty.value && !stringEditing.value && isJsonContainer(editedString.value)
    ? prettyJSON(editedString.value)
    : null,
)

function startStringEdit(): void {
  stringEditing.value = true
}

// hashPretty 字段值为完整 JSON 对象/数组时返回格式化文本,否则 null 走原文。
function hashPretty(raw: string): string | null {
  return isJsonContainer(raw) ? prettyJSON(raw) : null
}

// copyStringRaw 复制原始字节(而非格式化结果),保证与存储内容逐字一致。
async function copyStringRaw(): Promise<void> {
  try {
    await navigator.clipboard.writeText(editedString.value)
    useToastStore().show('已复制原始值')
  } catch {
    useToastStore().show('复制失败:浏览器未授权剪贴板')
  }
}

const renameTarget = ref<string | null>(null)
const renameValue = ref('')
async function doRename(): Promise<void> {
  if (!renameTarget.value) return
  await getApi().redisRenameKey({
    connection_id: props.connectionId,
    db: props.db,
    key: renameTarget.value,
    new_key: renameValue.value.trim(),
  })
  renameTarget.value = null
  await refreshAfterMutation()
}

const ttlTarget = ref<string | null>(null)
const ttlValue = ref<number | null>(null)
async function doSetTTL(): Promise<void> {
  if (!ttlTarget.value || ttlValue.value === null) return
  await getApi().redisSetTTL({
    connection_id: props.connectionId,
    db: props.db,
    key: ttlTarget.value,
    ttl_seconds: ttlValue.value,
  })
  ttlTarget.value = null
  await refreshAfterMutation()
}

async function doDelete(): Promise<void> {
  if (!selectedKey.value) return
  await getApi().redisDeleteKeys({
    connection_id: props.connectionId,
    db: props.db,
    keys: [selectedKey.value],
  })
  selectedKey.value = null
  value.value = null
  await refreshAfterMutation()
}

async function doSaveString(): Promise<void> {
  if (!selectedKey.value) return
  await getApi().redisSetString({
    connection_id: props.connectionId,
    db: props.db,
    key: selectedKey.value,
    value: editedString.value,
    ttl_seconds: -1,
  })
  await loadKeySilently(selectedKey.value)
  // 保存后按最新值重判展示形态:仍是 JSON 则回格式化只读态。
  stringPretty.value = isJsonContainer(editedString.value)
  stringEditing.value = false
}

async function loadKeySilently(key: string): Promise<void> {
  value.value = await getApi().redisGetKey({
    connection_id: props.connectionId,
    db: props.db,
    key,
  })
}

// —— hash 字段编辑 ——
const hashEditField = ref<string | null>(null)
const hashEditValue = ref('')
const hashAddFieldName = ref('')
const hashAddFieldValue = ref('')

async function doHashSetField(): Promise<void> {
  if (!selectedKey.value || !hashEditField.value) return
  await getApi().redisHashSetField({
    connection_id: props.connectionId,
    db: props.db,
    key: selectedKey.value,
    field: hashEditField.value,
    value: hashEditValue.value,
  })
  hashEditField.value = null
  await loadKeySilently(selectedKey.value)
}

async function doHashDeleteField(field: string): Promise<void> {
  if (!selectedKey.value) return
  await getApi().redisHashDeleteField({
    connection_id: props.connectionId,
    db: props.db,
    key: selectedKey.value,
    field,
  })
  await loadKeySilently(selectedKey.value)
}

async function doHashAddField(): Promise<void> {
  if (!selectedKey.value || !hashAddFieldName.value.trim()) return
  await getApi().redisHashSetField({
    connection_id: props.connectionId,
    db: props.db,
    key: selectedKey.value,
    field: hashAddFieldName.value.trim(),
    value: hashAddFieldValue.value,
  })
  hashAddFieldName.value = ''
  hashAddFieldValue.value = ''
  await loadKeySilently(selectedKey.value)
}

// —— list 元素编辑 ——
const listEditIdx = ref<number | null>(null)
const listEditValue = ref('')
const listAddValue = ref('')
const listAddAtHead = ref(false)

async function doListSetIndex(): Promise<void> {
  if (!selectedKey.value || listEditIdx.value === null) return
  await getApi().redisListSetIndex({
    connection_id: props.connectionId,
    db: props.db,
    key: selectedKey.value,
    index: listEditIdx.value,
    value: listEditValue.value,
  })
  listEditIdx.value = null
  await loadKeySilently(selectedKey.value)
}

async function doListDeleteIndex(index: number): Promise<void> {
  if (!selectedKey.value) return
  await getApi().redisListDeleteIndex({
    connection_id: props.connectionId,
    db: props.db,
    key: selectedKey.value,
    index,
  })
  await loadKeySilently(selectedKey.value)
}

async function doListPush(): Promise<void> {
  if (!selectedKey.value || !listAddValue.value) return
  const req: RedisListPushRequest = {
    connection_id: props.connectionId,
    db: props.db,
    key: selectedKey.value,
    value: listAddValue.value,
  }
  // 不勾选则省略 at_head(默认尾插)
  if (listAddAtHead.value) req.at_head = true
  await getApi().redisListPush(req)
  listAddValue.value = ''
  listAddAtHead.value = false
  await loadKeySilently(selectedKey.value)
}

// —— set 成员编辑 ——
const setAddMember = ref('')

async function doSetAdd(): Promise<void> {
  if (!selectedKey.value || !setAddMember.value.trim()) return
  await getApi().redisSetAdd({
    connection_id: props.connectionId,
    db: props.db,
    key: selectedKey.value,
    member: setAddMember.value.trim(),
  })
  setAddMember.value = ''
  await loadKeySilently(selectedKey.value)
}

async function doSetRemove(member: string): Promise<void> {
  if (!selectedKey.value) return
  await getApi().redisSetRemove({
    connection_id: props.connectionId,
    db: props.db,
    key: selectedKey.value,
    member,
  })
  await loadKeySilently(selectedKey.value)
}

// —— zset 成员编辑(添加与编辑同为 ZADD,同名覆盖) ——
const zsetEditIdx = ref<number | null>(null)
const zsetEditMember = ref('')
const zsetEditScore = ref(0)
const zsetAddMember = ref('')
const zsetAddScore = ref(0)

async function doZSetAdd(): Promise<void> {
  if (!selectedKey.value || !zsetAddMember.value.trim()) return
  await getApi().redisZSetAdd({
    connection_id: props.connectionId,
    db: props.db,
    key: selectedKey.value,
    member: zsetAddMember.value.trim(),
    score: zsetAddScore.value,
  })
  zsetAddMember.value = ''
  zsetAddScore.value = 0
  await loadKeySilently(selectedKey.value)
}

async function doZSetEdit(): Promise<void> {
  if (!selectedKey.value || zsetEditIdx.value === null) return
  await getApi().redisZSetAdd({
    connection_id: props.connectionId,
    db: props.db,
    key: selectedKey.value,
    member: zsetEditMember.value,
    score: zsetEditScore.value,
  })
  zsetEditIdx.value = null
  await loadKeySilently(selectedKey.value)
}

async function doZSetRemove(member: string): Promise<void> {
  if (!selectedKey.value) return
  await getApi().redisZSetRemove({
    connection_id: props.connectionId,
    db: props.db,
    key: selectedKey.value,
    member,
  })
  await loadKeySilently(selectedKey.value)
}

async function doFlushDB(): Promise<void> {
  // 成败都先收确认框:卡住的确认框正是「点了确定没反应」的观感来源;
  // 失败经 toast 给出后端原文,绝不静默吞错。
  confirmFlush.value = false
  try {
    await getApi().redisFlushDB({ connection_id: props.connectionId, db: props.db })
  } catch (e) {
    useToastStore().show(`清空 DB${props.db} 失败:${e instanceof Error ? e.message : String(e)}`)
    return
  }
  selectedKey.value = null
  value.value = null
  await refreshAfterMutation()
  await loadSummary()
  useToastStore().show(`DB${props.db} 已清空`)
}

async function refreshAfterMutation(): Promise<void> {
  await scan(true)
  await loadSummary()
}

const ttlOfSelected = computed(() => value.value?.ttl_seconds ?? -1)
</script>

<template>
  <div class="p-4 text-foreground" data-test="redis-keys-view">
    <!-- 工具栏:标题 + DB 徽章 + match 过滤 + 扫描/清空 -->
    <div class="flex flex-wrap items-center gap-2.5">
      <span class="text-sm font-semibold">键空间</span>
      <Badge variant="secondary" data-test="redis-db-badge">DB{{ props.db }}</Badge>
      <div class="flex-1"></div>
      <Input
        :value="match"
        type="search"
        class="h-8 w-[220px] bg-muted/50 text-[13px]"
        data-test="redis-match"
        placeholder="模式过滤,如 user:*"
        autocapitalize="off"
        autocorrect="off"
        autocomplete="off"
        spellcheck="false"
        @input="match = ($event.target as HTMLInputElement).value"
        @keydown.enter="scan(true)"
      />
      <Button variant="outline" size="sm" class="h-8" data-test="btn-redis-scan" :disabled="loading" @click="scan(true)">
        {{ loading ? '扫描中…' : '扫描' }}
      </Button>
      <Button
        variant="ghost"
        size="sm"
        class="h-8 text-destructive hover:bg-destructive/10 hover:text-destructive"
        data-test="btn-flushdb"
        @click="confirmFlush = true"
      >
        清空当前 DB
      </Button>
    </div>

    <!-- 服务摘要 -->
    <div
      v-if="summary"
      class="mt-2.5 flex flex-wrap items-center gap-2 rounded-md border border-border bg-muted/50 px-3 py-2 text-xs text-muted-foreground"
      data-test="redis-summary"
    >
      <span>{{ summary.mode === 'cluster' ? `集群 · ${summary.nodes?.length ?? 0} 节点` : '单机' }}</span>
      <span class="text-muted-foreground/50">·</span>
      <span>内存 {{ summary.used_memory_human || '—' }}</span>
      <span class="text-muted-foreground/50">·</span>
      <span class="tabular-nums">键 {{ summary.total_keys }}</span>
      <span class="text-muted-foreground/50">·</span>
      <span class="tabular-nums">客户端 {{ summary.connected_clients }}</span>
      <template v-if="summary.hit_rate !== null && summary.hit_rate !== undefined">
        <span class="text-muted-foreground/50">·</span>
        <span class="tabular-nums">命中率 {{ (summary.hit_rate * 100).toFixed(1) }}%</span>
      </template>
    </div>

    <div v-if="error" class="my-2 rounded-md bg-destructive/10 px-2.5 py-2 text-[13px] text-destructive" data-test="redis-error">{{ error }}</div>

    <div v-if="limitReached" class="my-2 rounded-md bg-warning/15 px-2.5 py-2 text-[13px] text-warning" data-test="redis-limit-notice">
      已达 2 万键展示上限，请使用 match 前缀过滤缩小范围
    </div>

    <div class="mt-3 grid grid-cols-[300px_1fr] gap-3">
      <!-- 键列表 -->
      <div class="overflow-hidden rounded-md border border-border bg-card">
        <div class="flex items-center justify-between border-b border-border bg-muted/50 px-2.5 py-1.5 text-xs text-muted-foreground">
          <span class="tabular-nums">键(共 {{ keys.length }})</span>
          <Button
            v-if="cursor !== 0"
            variant="ghost"
            size="sm"
            class="h-6 px-2 text-xs"
            data-test="btn-redis-more"
            :disabled="loading"
            @click="scan(false)"
          >
            加载更多
          </Button>
        </div>
        <div
          v-for="k in keys"
          :key="k.key"
          class="flex items-center gap-2 border-b border-border px-2.5 py-1.5 text-[13px] hover:bg-muted"
          :class="{ 'bg-accent': selectedKey === k.key }"
          data-test="redis-key-row"
          @click="selectKey(k)"
        >
          <Badge
            variant="outline"
            class="shrink-0 rounded-[4px] px-1.5 py-0 text-[10px] font-medium uppercase text-muted-foreground"
            :data-type="k.type"
          >
            {{ k.type }}
          </Badge>
          <span class="min-w-0 flex-1 truncate font-mono">{{ k.key }}</span>
          <span v-if="k.size_bytes > 0" class="shrink-0 font-mono text-xs tabular-nums text-muted-foreground" data-test="redis-key-size">{{ formatBytes(k.size_bytes) }}</span>
          <span v-if="k.ttl_seconds >= 0" class="shrink-0 text-[11px] tabular-nums text-muted-foreground/70">{{ k.ttl_seconds }}s</span>
        </div>
        <div v-if="keys.length === 0 && !loading" class="py-5 text-center text-[13px] text-muted-foreground" data-test="redis-keys-empty">无匹配键</div>
      </div>

      <!-- 值面板 -->
      <div class="min-h-[200px] rounded-md border border-border bg-card p-3" data-test="redis-value-panel">
        <div v-if="valueLoading" class="py-5 text-center text-[13px] text-muted-foreground">加载中…</div>
        <div v-else-if="!value" class="py-5 text-center text-[13px] text-muted-foreground">选择左侧键查看值</div>
        <template v-else>
          <div class="mb-3 flex flex-wrap items-center gap-2 border-b border-border pb-2.5">
            <span class="break-all font-mono text-sm font-semibold">{{ value.key }}</span>
            <Badge
              variant="outline"
              class="rounded-[4px] px-1.5 py-0 text-[10px] font-medium uppercase text-muted-foreground"
              :data-type="value.type"
            >
              {{ value.type }}
            </Badge>
            <span class="font-mono text-xs tabular-nums text-muted-foreground" data-test="redis-value-ttl">TTL {{ value.ttl_seconds >= 0 ? value.ttl_seconds + 's' : '∞' }}</span>
            <div class="flex-1"></div>
            <Button variant="ghost" size="sm" class="h-7 text-destructive hover:bg-destructive/10 hover:text-destructive" data-test="btn-key-delete" @click="confirmDelete = true">删除</Button>
            <Button variant="outline" size="sm" class="h-7" data-test="btn-key-rename" @click="renameTarget = value.key; renameValue = value.key">重命名</Button>
            <Button variant="outline" size="sm" class="h-7" data-test="btn-key-ttl" @click="ttlTarget = value.key; ttlValue = ttlOfSelected">TTL</Button>
          </div>

          <div v-if="renameTarget" class="my-2 flex items-center gap-2">
            <Input
              :value="renameValue"
              class="h-8 font-mono text-[13px]"
              data-test="input-rename"
              spellcheck="false"
              @input="renameValue = ($event.target as HTMLInputElement).value"
            />
            <Button size="sm" class="h-8" data-test="btn-rename-ok" @click="doRename">确定</Button>
            <Button variant="outline" size="sm" class="h-8" @click="renameTarget = null">取消</Button>
          </div>
          <div v-if="ttlTarget" class="my-2 flex items-center gap-2">
            <Input
              :value="ttlValue ?? ''"
              type="number"
              class="h-8 text-[13px]"
              data-test="input-ttl"
              placeholder="秒(0 = 永久)"
              @input="ttlValue = ($event.target as HTMLInputElement).value === '' ? null : Number(($event.target as HTMLInputElement).value)"
            />
            <Button size="sm" class="h-8" data-test="btn-ttl-ok" @click="doSetTTL">确定</Button>
            <Button variant="outline" size="sm" class="h-8" @click="ttlTarget = null">取消</Button>
          </div>

          <!-- string:JSON 格式化只读视图或可编辑 textarea -->
          <template v-if="value.type === 'string'">
            <div v-if="value.truncated" class="my-2 flex items-center gap-2 rounded-md bg-warning/15 px-2.5 py-2 text-[13px] text-warning">
              值过大已截断(共 {{ value.size_bytes }} 字节)。
              <Button variant="ghost" size="sm" class="h-7" data-test="btn-load-full" @click="loadFullString">加载完整</Button>
            </div>
            <div class="flex flex-col items-end gap-2" data-test="redis-string-editor">
              <template v-if="prettyString !== null">
                <div class="flex items-center gap-2">
                  <span class="text-xs text-muted-foreground">JSON 已格式化(只读)</span>
                  <Button variant="outline" size="sm" class="h-7" data-test="btn-string-copy" @click="copyStringRaw">复制原始值</Button>
                  <Button size="sm" class="h-7" data-test="btn-string-edit" @click="startStringEdit">编辑</Button>
                </div>
                <pre
                  class="wrap-anywhere max-h-[50vh] w-full overflow-auto rounded-md border border-border bg-muted/50 p-2.5 text-xs leading-relaxed whitespace-pre-wrap text-foreground"
                  data-test="redis-string-pretty"
                >{{ prettyString }}</pre>
              </template>
              <template v-else>
                <textarea
                  v-model="editedString"
                  class="w-full resize-y rounded-md border border-input bg-transparent px-3 py-2 font-mono text-[13px] shadow-xs outline-none focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50 dark:bg-input/30"
                  rows="12"
                  spellcheck="false"
                ></textarea>
                <Button size="sm" class="h-7" data-test="btn-string-save" @click="doSaveString">保存</Button>
              </template>
            </div>
          </template>

          <!-- hash:固定列宽 KV 表,JSON 字段值格式化展示 -->
          <template v-else-if="value.type === 'hash'">
            <table class="w-full table-fixed wrap-anywhere text-[13px]">
              <thead>
                <tr>
                  <th class="w-[170px] border-b border-border px-2.5 py-1.5 text-left text-xs font-semibold text-muted-foreground">Field</th>
                  <th class="border-b border-border px-2.5 py-1.5 text-left text-xs font-semibold text-muted-foreground">Value</th>
                  <th class="w-[118px] border-b border-border"></th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="(f, i) in value.hash" :key="f.field" class="border-b border-border align-top hover:bg-muted/60" data-test="redis-hash-row">
                  <td class="break-all px-2.5 py-1.5 font-mono">{{ f.field }}</td>
                  <td class="px-2.5 py-1.5 font-mono">
                    <textarea
                      v-if="hashEditField === f.field"
                      v-model="hashEditValue"
                      class="min-h-[90px] w-full resize-y rounded-md border border-input bg-transparent px-3 py-2 font-mono text-[13px] shadow-xs outline-none focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50 dark:bg-input/30"
                      rows="4"
                      data-test="input-hash-edit-value"
                      autocapitalize="off"
                      autocomplete="off"
                      spellcheck="false"
                    ></textarea>
                    <pre
                      v-else-if="hashPretty(f.value)"
                      class="wrap-anywhere max-h-[50vh] w-full overflow-auto rounded-md border border-border bg-muted/50 p-2.5 text-xs leading-relaxed whitespace-pre-wrap text-foreground"
                      data-test="redis-hash-pretty"
                    >{{ hashPretty(f.value) }}</pre>
                    <span v-else class="whitespace-pre-wrap">{{ f.value }}</span>
                  </td>
                  <td class="px-2 py-1.5">
                    <div class="flex justify-end gap-1.5 whitespace-nowrap">
                      <template v-if="hashEditField === f.field">
                        <Button size="sm" class="h-6 px-2 text-xs" data-test="btn-hash-edit-ok" @click="doHashSetField">确定</Button>
                        <Button variant="outline" size="sm" class="h-6 px-2 text-xs" @click="hashEditField = null">取消</Button>
                      </template>
                      <template v-else>
                        <Button variant="outline" size="sm" class="h-6 px-2 text-xs" :data-test="`redis-hash-edit-${i}`" @click="hashEditField = f.field; hashEditValue = f.value">编辑</Button>
                        <Button variant="ghost" size="sm" class="h-6 px-2 text-xs text-destructive hover:bg-destructive/10 hover:text-destructive" :data-test="`redis-hash-del-${i}`" @click="doHashDeleteField(f.field)">删除</Button>
                      </template>
                    </div>
                  </td>
                </tr>
              </tbody>
            </table>
            <div class="mt-2.5 flex flex-wrap items-center gap-2">
              <Input
                :value="hashAddFieldName"
                class="h-8 min-w-[120px] flex-1 font-mono text-[13px]"
                data-test="input-hash-add-field"
                placeholder="field"
                autocapitalize="off"
                autocomplete="off"
                spellcheck="false"
                @input="hashAddFieldName = ($event.target as HTMLInputElement).value"
                @keydown.enter="doHashAddField"
              />
              <Input
                :value="hashAddFieldValue"
                class="h-8 min-w-[120px] flex-1 font-mono text-[13px]"
                data-test="input-hash-add-value"
                placeholder="value"
                autocapitalize="off"
                autocomplete="off"
                spellcheck="false"
                @input="hashAddFieldValue = ($event.target as HTMLInputElement).value"
                @keydown.enter="doHashAddField"
              />
              <Button size="sm" class="h-8" data-test="redis-hash-add-field" @click="doHashAddField">添加字段</Button>
            </div>
          </template>

          <!-- list:索引行内编辑 + 头/尾追加 -->
          <template v-else-if="value.type === 'list'">
            <ul class="m-0 list-none p-0 text-[13px]" data-test="redis-list">
              <li
                v-for="(item, i) in value.list"
                :key="i"
                class="flex gap-2 break-all border-b border-border px-1.5 py-1"
                :class="listEditIdx === i ? 'flex-wrap items-start' : 'flex-nowrap items-center'"
              >
                <template v-if="listEditIdx === i">
                  <span class="w-6 shrink-0 text-right font-mono text-xs tabular-nums text-muted-foreground/70">{{ i }}</span>
                  <!-- 与值显示区同宽的多行编辑器:长日志/JSON 也能完整查看与修改 -->
                  <textarea
                    v-model="listEditValue"
                    class="min-h-[120px] w-full resize-y rounded-md border border-input bg-transparent px-3 py-2 font-mono text-[13px] shadow-xs outline-none focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50 dark:bg-input/30"
                    rows="6"
                    data-test="input-list-edit-value"
                    autocapitalize="off"
                    autocomplete="off"
                    spellcheck="false"
                  ></textarea>
                  <div class="flex w-full justify-end gap-2">
                    <Button size="sm" class="h-7" data-test="btn-list-edit-ok" @click="doListSetIndex">确定</Button>
                    <Button variant="outline" size="sm" class="h-7" @click="listEditIdx = null">取消</Button>
                  </div>
                </template>
                <template v-else>
                  <span class="w-6 shrink-0 text-right font-mono text-xs tabular-nums text-muted-foreground/70">{{ i }}</span>
                  <span class="min-w-0 whitespace-pre-wrap break-all font-mono grow">{{ item }}</span>
                  <Button variant="outline" size="sm" class="h-6 px-2 text-xs" :data-test="`redis-list-edit-${i}`" @click="listEditIdx = i; listEditValue = item">编辑</Button>
                  <Button variant="ghost" size="sm" class="h-6 px-2 text-xs text-destructive hover:bg-destructive/10 hover:text-destructive" :data-test="`redis-list-del-${i}`" @click="doListDeleteIndex(i)">删除</Button>
                </template>
              </li>
            </ul>
            <div class="mt-2.5 flex flex-wrap items-center gap-2">
              <Input
                :value="listAddValue"
                class="h-8 min-w-[120px] flex-1 font-mono text-[13px]"
                data-test="input-list-add-value"
                placeholder="追加元素"
                autocapitalize="off"
                autocomplete="off"
                spellcheck="false"
                @input="listAddValue = ($event.target as HTMLInputElement).value"
                @keydown.enter="doListPush"
              />
              <label class="flex shrink-0 items-center gap-1.5 text-xs text-muted-foreground">
                <input v-model="listAddAtHead" type="checkbox" class="size-3.5 accent-primary" data-test="input-list-add-at-head" />
                插到头部
              </label>
              <Button size="sm" class="h-8" data-test="redis-list-add-value" @click="doListPush">追加元素</Button>
            </div>
          </template>

          <!-- set:成员列表 + 添加 -->
          <template v-else-if="value.type === 'set'">
            <ul class="m-0 list-none p-0 text-[13px]" data-test="redis-set">
              <li v-for="(item, i) in value.set" :key="item" class="flex flex-nowrap items-center gap-2 break-all border-b border-border px-1.5 py-1">
                <span class="min-w-0 font-mono grow">{{ item }}</span>
                <Button variant="ghost" size="sm" class="h-6 px-2 text-xs text-destructive hover:bg-destructive/10 hover:text-destructive" :data-test="`redis-set-del-${i}`" @click="doSetRemove(item)">删除</Button>
              </li>
            </ul>
            <div class="mt-2.5 flex flex-wrap items-center gap-2">
              <Input
                :value="setAddMember"
                class="h-8 min-w-[120px] flex-1 font-mono text-[13px]"
                data-test="input-set-add-member"
                placeholder="添加成员"
                autocapitalize="off"
                autocomplete="off"
                spellcheck="false"
                @input="setAddMember = ($event.target as HTMLInputElement).value"
                @keydown.enter="doSetAdd"
              />
              <Button size="sm" class="h-8" data-test="redis-set-add-member" @click="doSetAdd">添加成员</Button>
            </div>
          </template>

          <!-- zset:Score/Member KV 表 + 添加 -->
          <template v-else-if="value.type === 'zset'">
            <table class="w-full table-fixed wrap-anywhere text-[13px]">
              <thead>
                <tr>
                  <th class="w-[110px] border-b border-border px-2.5 py-1.5 text-left text-xs font-semibold text-muted-foreground">Score</th>
                  <th class="border-b border-border px-2.5 py-1.5 text-left text-xs font-semibold text-muted-foreground">Member</th>
                  <th class="w-[118px] border-b border-border"></th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="(z, i) in value.zset" :key="z.member" class="border-b border-border align-top hover:bg-muted/60" data-test="redis-zset-row">
                  <td class="px-2.5 py-1.5 font-mono tabular-nums">
                    <Input
                      v-if="zsetEditIdx === i"
                      :value="zsetEditScore"
                      type="number"
                      step="any"
                      class="h-7 font-mono text-[13px]"
                      data-test="input-zset-edit-score"
                      @input="zsetEditScore = ($event.target as HTMLInputElement).value === '' ? 0 : Number(($event.target as HTMLInputElement).value)"
                    />
                    <template v-else>{{ z.score }}</template>
                  </td>
                  <td class="px-2.5 py-1.5 font-mono">
                    <Input
                      v-if="zsetEditIdx === i"
                      :value="zsetEditMember"
                      class="h-7 font-mono text-[13px]"
                      data-test="input-zset-edit-member"
                      autocapitalize="off"
                      autocomplete="off"
                      spellcheck="false"
                      @input="zsetEditMember = ($event.target as HTMLInputElement).value"
                      @keydown.enter="doZSetEdit"
                    />
                    <template v-else>{{ z.member }}</template>
                  </td>
                  <td class="px-2 py-1.5">
                    <div class="flex justify-end gap-1.5 whitespace-nowrap">
                      <template v-if="zsetEditIdx === i">
                        <Button size="sm" class="h-6 px-2 text-xs" data-test="btn-zset-edit-ok" @click="doZSetEdit">确定</Button>
                        <Button variant="outline" size="sm" class="h-6 px-2 text-xs" @click="zsetEditIdx = null">取消</Button>
                      </template>
                      <template v-else>
                        <Button variant="outline" size="sm" class="h-6 px-2 text-xs" :data-test="`redis-zset-edit-${i}`" @click="zsetEditIdx = i; zsetEditMember = z.member; zsetEditScore = z.score">编辑</Button>
                        <Button variant="ghost" size="sm" class="h-6 px-2 text-xs text-destructive hover:bg-destructive/10 hover:text-destructive" :data-test="`redis-zset-del-${i}`" @click="doZSetRemove(z.member)">删除</Button>
                      </template>
                    </div>
                  </td>
                </tr>
              </tbody>
            </table>
            <div class="mt-2.5 flex flex-wrap items-center gap-2">
              <Input
                :value="zsetAddMember"
                class="h-8 min-w-[120px] flex-1 font-mono text-[13px]"
                data-test="input-zset-add-member"
                placeholder="member"
                autocapitalize="off"
                autocomplete="off"
                spellcheck="false"
                @input="zsetAddMember = ($event.target as HTMLInputElement).value"
                @keydown.enter="doZSetAdd"
              />
              <Input
                :value="zsetAddScore"
                type="number"
                step="any"
                class="h-8 w-[110px] font-mono text-[13px]"
                data-test="input-zset-add-score"
                placeholder="score"
                @input="zsetAddScore = ($event.target as HTMLInputElement).value === '' ? 0 : Number(($event.target as HTMLInputElement).value)"
                @keydown.enter="doZSetAdd"
              />
              <Button size="sm" class="h-8" data-test="redis-zset-add-member" @click="doZSetAdd">添加成员</Button>
            </div>
          </template>
        </template>
      </div>
    </div>

    <ConfirmDialog
      :show="confirmDelete"
      :message="`确认删除键「${selectedKey}」？此操作不可恢复。`"
      confirm-text="删除"
      @confirm="doDelete"
      @cancel="confirmDelete = false"
    />
    <ConfirmDialog
      :show="confirmFlush"
      :message="`确认清空 DB${props.db} 的全部键？此操作不可恢复。`"
      confirm-text="清空"
      @confirm="doFlushDB"
      @cancel="confirmFlush = false"
    />
  </div>
</template>
