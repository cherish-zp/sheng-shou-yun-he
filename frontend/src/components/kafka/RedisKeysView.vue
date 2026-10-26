<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { getApi } from '@/api/client'
import type { RedisKeyInfo, RedisListPushRequest, RedisServerInfo, RedisValue } from '@/api/types'
import { formatBytes } from '@/utils/bytes'
import { isJsonContainer, prettyJSON } from '@/utils/format'
import { useToastStore } from '@/store/toast'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'

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
  <div class="redis-keys" data-test="redis-keys-view">
    <div class="toolbar">
      <span class="title">键空间</span>
      <span class="db-badge" data-test="redis-db-badge">DB{{ props.db }}</span>
      <div class="spacer"></div>
      <input
        v-model="match"
        class="search-input"
        type="search"
        data-test="redis-match"
        placeholder="模式过滤,如 user:*"
        autocapitalize="off"
        autocorrect="off"
        autocomplete="off"
        spellcheck="false"
        @keydown.enter="scan(true)"
      />
      <button class="btn ghost" type="button" data-test="btn-redis-scan" :disabled="loading" @click="scan(true)">
        {{ loading ? '扫描中…' : '扫描' }}
      </button>
      <button class="btn ghost danger" type="button" data-test="btn-flushdb" @click="confirmFlush = true">清空当前 DB</button>
    </div>

    <div v-if="summary" class="summary" data-test="redis-summary">
      <span>{{ summary.mode === 'cluster' ? `集群 · ${summary.nodes?.length ?? 0} 节点` : '单机' }}</span>
      <span class="sep">·</span>
      <span>内存 {{ summary.used_memory_human || '—' }}</span>
      <span class="sep">·</span>
      <span>键 {{ summary.total_keys }}</span>
      <span class="sep">·</span>
      <span>客户端 {{ summary.connected_clients }}</span>
      <template v-if="summary.hit_rate !== null && summary.hit_rate !== undefined">
        <span class="sep">·</span>
        <span>命中率 {{ (summary.hit_rate * 100).toFixed(1) }}%</span>
      </template>
    </div>

    <div v-if="error" class="msg err" data-test="redis-error">{{ error }}</div>

    <div v-if="limitReached" class="msg warn" data-test="redis-limit-notice">
      已达 2 万键展示上限，请使用 match 前缀过滤缩小范围
    </div>

    <div class="columns">
      <div class="keys-col">
        <div class="keys-head">
          <span>键(共 {{ keys.length }})</span>
          <button
            v-if="cursor !== 0"
            class="btn ghost small"
            type="button"
            data-test="btn-redis-more"
            :disabled="loading"
            @click="scan(false)"
          >
            加载更多
          </button>
        </div>
        <div
          v-for="k in keys"
          :key="k.key"
          class="key-row"
          :class="{ selected: selectedKey === k.key }"
          data-test="redis-key-row"
          @click="selectKey(k)"
        >
          <span class="key-type" :data-type="k.type">{{ k.type }}</span>
          <span class="key-name">{{ k.key }}</span>
          <span v-if="k.size_bytes > 0" class="key-size" data-test="redis-key-size">{{ formatBytes(k.size_bytes) }}</span>
          <span v-if="k.ttl_seconds >= 0" class="key-ttl">{{ k.ttl_seconds }}s</span>
        </div>
        <div v-if="keys.length === 0 && !loading" class="empty" data-test="redis-keys-empty">无匹配键</div>
      </div>

      <div class="value-col" data-test="redis-value-panel">
        <div v-if="valueLoading" class="empty">加载中…</div>
        <div v-else-if="!value" class="empty">选择左侧键查看值</div>
        <template v-else>
          <div class="value-head">
            <span class="mono value-key">{{ value.key }}</span>
            <span class="key-type" :data-type="value.type">{{ value.type }}</span>
            <span class="mono ttl" data-test="redis-value-ttl">TTL {{ value.ttl_seconds >= 0 ? value.ttl_seconds + 's' : '∞' }}</span>
            <div class="spacer"></div>
            <button class="btn ghost small danger" type="button" data-test="btn-key-delete" @click="confirmDelete = true">删除</button>
            <button class="btn ghost small" type="button" data-test="btn-key-rename" @click="renameTarget = value.key; renameValue = value.key">重命名</button>
            <button class="btn ghost small" type="button" data-test="btn-key-ttl" @click="ttlTarget = value.key; ttlValue = ttlOfSelected">TTL</button>
          </div>

          <div v-if="renameTarget" class="inline-edit">
            <input v-model="renameValue" class="input" data-test="input-rename" spellcheck="false" />
            <button class="btn primary small" type="button" data-test="btn-rename-ok" @click="doRename">确定</button>
            <button class="btn ghost small" type="button" @click="renameTarget = null">取消</button>
          </div>
          <div v-if="ttlTarget" class="inline-edit">
            <input v-model.number="ttlValue" class="input" type="number" data-test="input-ttl" placeholder="秒(0 = 永久)" />
            <button class="btn primary small" type="button" data-test="btn-ttl-ok" @click="doSetTTL">确定</button>
            <button class="btn ghost small" type="button" @click="ttlTarget = null">取消</button>
          </div>

          <template v-if="value.type === 'string'">
            <div v-if="value.truncated" class="msg warn">
              值过大已截断(共 {{ value.size_bytes }} 字节)。
              <button class="btn ghost small" type="button" data-test="btn-load-full" @click="loadFullString">加载完整</button>
            </div>
            <div class="string-editor" data-test="redis-string-editor">
              <template v-if="prettyString !== null">
                <div class="string-toolbar">
                  <span class="toolbar-hint">JSON 已格式化(只读)</span>
                  <button class="btn ghost small" type="button" data-test="btn-string-copy" @click="copyStringRaw">复制原始值</button>
                  <button class="btn primary small" type="button" data-test="btn-string-edit" @click="startStringEdit">编辑</button>
                </div>
                <pre class="json-view mono" data-test="redis-string-pretty">{{ prettyString }}</pre>
              </template>
              <template v-else>
                <textarea v-model="editedString" class="input textarea mono" rows="12" spellcheck="false"></textarea>
                <button class="btn primary small" type="button" data-test="btn-string-save" @click="doSaveString">保存</button>
              </template>
            </div>
          </template>
          <template v-else-if="value.type === 'hash'">
            <table class="table data-table kv-table">
              <thead><tr><th class="field-col">Field</th><th>Value</th><th class="actions-col"></th></tr></thead>
              <tbody>
                <tr v-for="(f, i) in value.hash" :key="f.field" data-test="redis-hash-row">
                  <td class="mono field-cell">{{ f.field }}</td>
                  <td class="value-cell mono">
                    <textarea
                      v-if="hashEditField === f.field"
                      v-model="hashEditValue"
                      class="input mono edit-area"
                      rows="4"
                      data-test="input-hash-edit-value"
                      autocapitalize="off"
                      autocomplete="off"
                      spellcheck="false"
                    ></textarea>
                    <template v-else-if="hashPretty(f.value)">
                      <pre class="json-view mono" data-test="redis-hash-pretty">{{ hashPretty(f.value) }}</pre>
                    </template>
                    <template v-else><span class="val-text">{{ f.value }}</span></template>
                  </td>
                  <td class="row-actions">
                    <template v-if="hashEditField === f.field">
                      <button class="btn primary small" type="button" data-test="btn-hash-edit-ok" @click="doHashSetField">确定</button>
                      <button class="btn ghost small" type="button" @click="hashEditField = null">取消</button>
                    </template>
                    <template v-else>
                      <button class="btn ghost small" type="button" :data-test="`redis-hash-edit-${i}`" @click="hashEditField = f.field; hashEditValue = f.value">编辑</button>
                      <button class="btn ghost small danger" type="button" :data-test="`redis-hash-del-${i}`" @click="doHashDeleteField(f.field)">删除</button>
                    </template>
                  </td>
                </tr>
              </tbody>
            </table>
            <div class="add-row">
              <input v-model="hashAddFieldName" class="input mono" data-test="input-hash-add-field" placeholder="field" autocapitalize="off" autocomplete="off" spellcheck="false" @keydown.enter="doHashAddField" />
              <input v-model="hashAddFieldValue" class="input mono" data-test="input-hash-add-value" placeholder="value" autocapitalize="off" autocomplete="off" spellcheck="false" @keydown.enter="doHashAddField" />
              <button class="btn primary small" type="button" data-test="redis-hash-add-field" @click="doHashAddField">添加字段</button>
            </div>
          </template>
          <template v-else-if="value.type === 'list'">
            <ul class="coll" data-test="redis-list">
              <li v-for="(item, i) in value.list" :key="i" class="coll-row" :class="{ editing: listEditIdx === i }">
                <template v-if="listEditIdx === i">
                  <span class="idx mono">{{ i }}</span>
                  <!-- 与值显示区同宽的多行编辑器:长日志/JSON 也能完整查看与修改 -->
                  <textarea
                    v-model="listEditValue"
                    class="input mono edit-area"
                    rows="6"
                    data-test="input-list-edit-value"
                    autocapitalize="off"
                    autocomplete="off"
                    spellcheck="false"
                  ></textarea>
                  <div class="edit-actions">
                    <button class="btn primary small" type="button" data-test="btn-list-edit-ok" @click="doListSetIndex">确定</button>
                    <button class="btn ghost small" type="button" @click="listEditIdx = null">取消</button>
                  </div>
                </template>
                <template v-else>
                  <span class="idx mono">{{ i }}</span>
                  <span class="mono grow coll-value">{{ item }}</span>
                  <button class="btn ghost small" type="button" :data-test="`redis-list-edit-${i}`" @click="listEditIdx = i; listEditValue = item">编辑</button>
                  <button class="btn ghost small danger" type="button" :data-test="`redis-list-del-${i}`" @click="doListDeleteIndex(i)">删除</button>
                </template>
              </li>
            </ul>
            <div class="add-row">
              <input v-model="listAddValue" class="input mono" data-test="input-list-add-value" placeholder="追加元素" autocapitalize="off" autocomplete="off" spellcheck="false" @keydown.enter="doListPush" />
              <label class="check"><input v-model="listAddAtHead" type="checkbox" data-test="input-list-add-at-head" /> 插到头部</label>
              <button class="btn primary small" type="button" data-test="redis-list-add-value" @click="doListPush">追加元素</button>
            </div>
          </template>
          <template v-else-if="value.type === 'set'">
            <ul class="coll" data-test="redis-set">
              <li v-for="(item, i) in value.set" :key="item" class="coll-row">
                <span class="mono grow">{{ item }}</span>
                <button class="btn ghost small danger" type="button" :data-test="`redis-set-del-${i}`" @click="doSetRemove(item)">删除</button>
              </li>
            </ul>
            <div class="add-row">
              <input v-model="setAddMember" class="input mono" data-test="input-set-add-member" placeholder="添加成员" autocapitalize="off" autocomplete="off" spellcheck="false" @keydown.enter="doSetAdd" />
              <button class="btn primary small" type="button" data-test="redis-set-add-member" @click="doSetAdd">添加成员</button>
            </div>
          </template>
          <template v-else-if="value.type === 'zset'">
            <table class="table data-table kv-table">
              <thead><tr><th class="score-col">Score</th><th>Member</th><th class="actions-col"></th></tr></thead>
              <tbody>
                <tr v-for="(z, i) in value.zset" :key="z.member" data-test="redis-zset-row">
                  <td class="mono">
                    <input v-if="zsetEditIdx === i" v-model.number="zsetEditScore" class="input mono" type="number" step="any" data-test="input-zset-edit-score" />
                    <template v-else>{{ z.score }}</template>
                  </td>
                  <td class="mono">
                    <input v-if="zsetEditIdx === i" v-model="zsetEditMember" class="input mono" data-test="input-zset-edit-member" autocapitalize="off" autocomplete="off" spellcheck="false" @keydown.enter="doZSetEdit" />
                    <template v-else>{{ z.member }}</template>
                  </td>
                  <td class="row-actions">
                    <template v-if="zsetEditIdx === i">
                      <button class="btn primary small" type="button" data-test="btn-zset-edit-ok" @click="doZSetEdit">确定</button>
                      <button class="btn ghost small" type="button" @click="zsetEditIdx = null">取消</button>
                    </template>
                    <template v-else>
                      <button class="btn ghost small" type="button" :data-test="`redis-zset-edit-${i}`" @click="zsetEditIdx = i; zsetEditMember = z.member; zsetEditScore = z.score">编辑</button>
                      <button class="btn ghost small danger" type="button" :data-test="`redis-zset-del-${i}`" @click="doZSetRemove(z.member)">删除</button>
                    </template>
                  </td>
                </tr>
              </tbody>
            </table>
            <div class="add-row">
              <input v-model="zsetAddMember" class="input mono" data-test="input-zset-add-member" placeholder="member" autocapitalize="off" autocomplete="off" spellcheck="false" @keydown.enter="doZSetAdd" />
              <input v-model.number="zsetAddScore" class="input mono" type="number" step="any" data-test="input-zset-add-score" placeholder="score" @keydown.enter="doZSetAdd" />
              <button class="btn primary small" type="button" data-test="redis-zset-add-member" @click="doZSetAdd">添加成员</button>
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

<style scoped>
.redis-keys { padding: 16px; color: var(--text); font-family: var(--font); }
.toolbar { display: flex; gap: 10px; align-items: center; flex-wrap: wrap; }
.title { font-weight: 600; font-size: 14px; }
.db-badge {
  background: var(--accent-soft); color: var(--accent);
  border-radius: 6px; padding: 2px 8px; font-size: 12px; font-weight: 600;
}
.spacer { flex: 1; }
.search-input {
  background: var(--bg-subtle); border: 1px solid var(--border); color: var(--text);
  border-radius: 7px; padding: 6px 9px; font-size: 13px; width: 220px;
}
.search-input:focus { outline: none; border-color: var(--accent); }
.summary {
  display: flex; gap: 8px; flex-wrap: wrap; align-items: center;
  margin-top: 10px; padding: 8px 12px;
  background: var(--bg-subtle); border: 1px solid var(--border); border-radius: 8px;
  font-size: 12px; color: var(--text-secondary);
}
.sep { color: var(--text-tertiary); }
.columns { display: grid; grid-template-columns: 300px 1fr; gap: 12px; margin-top: 12px; }
.keys-col { border: 1px solid var(--border); border-radius: 8px; overflow: hidden; background: var(--bg-elevated); }
.keys-head {
  display: flex; justify-content: space-between; align-items: center;
  padding: 6px 10px; font-size: 12px; color: var(--text-secondary);
  border-bottom: 1px solid var(--border); background: var(--bg-subtle);
}
.key-row {
  display: flex; align-items: center; gap: 8px;
  padding: 6px 10px; cursor: pointer; font-size: 13px;
  border-bottom: 1px solid var(--border);
}
.key-row:hover { background: var(--bg-hover); }
.key-row.selected { background: var(--accent-soft); }
.key-type {
  flex: none; font-size: 10px; text-transform: uppercase;
  background: var(--bg-hover); border-radius: 4px; padding: 1px 5px;
  color: var(--text-secondary);
}
.key-name { flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-family: var(--mono); }
.key-ttl { flex: none; font-size: 11px; color: var(--text-tertiary); }
.key-size { flex: none; font-size: 11px; color: var(--text-secondary); font-family: var(--mono); }
.value-col {
  border: 1px solid var(--border); border-radius: 8px;
  background: var(--bg-elevated); padding: 10px 12px; min-height: 200px;
}
.value-head {
  display: flex; align-items: center; gap: 8px; flex-wrap: wrap;
  margin-bottom: 12px; padding-bottom: 10px; border-bottom: 1px solid var(--border);
}
.value-key { font-size: 14px; font-weight: 600; word-break: break-all; }
.ttl { color: var(--text-secondary); font-size: 12px; }
.inline-edit { display: flex; gap: 8px; margin: 8px 0; }
.string-editor { display: flex; flex-direction: column; gap: 8px; align-items: flex-end; }
.string-toolbar { display: flex; gap: 8px; align-items: center; align-self: flex-end; }
/* JSON 格式化只读视图:等宽、可滚动,底色与输入框同族但不可编辑。 */
.json-view {
  width: 100%; box-sizing: border-box; max-height: 50vh; overflow: auto;
  margin: 0; padding: 10px 12px; font-size: 12px; line-height: 1.6;
  background: var(--bg-subtle); border: 1px solid var(--border);
  border-radius: 8px; color: var(--text);
  /* pre-wrap + anywhere:长 token 行折行铺平,绝不出现横向滚动条 */
  white-space: pre-wrap; overflow-wrap: anywhere;
}
/* KV 表(hash/zset):固定列宽——长 token 值(如无空格 JSON)在自动布局下
   会把值列撑爆、把 Field 列挤成竖排单字符;fixed + anywhere 双保险。 */
.kv-table { table-layout: fixed; width: 100%; }
.kv-table .field-col { width: 170px; }
.kv-table .score-col { width: 110px; }
.kv-table .actions-col { width: 118px; }
.kv-table th, .kv-table td { overflow-wrap: anywhere; vertical-align: top; }
/* 操作列按钮顶对齐贴齐 JSON 块上缘(td 上的 flex 会让 vertical-align 失效)。 */
.kv-table .row-actions { align-items: flex-start; }
.value-cell { display: flex; flex-direction: column; align-items: stretch; gap: 4px; }
.val-text { white-space: pre-wrap; }
.input {
  background: var(--bg-subtle); border: 1px solid var(--border); color: var(--text);
  border-radius: 7px; padding: 6px 9px; font-size: 13px;
}
.input:focus { outline: none; border-color: var(--accent); }
.textarea { width: 100%; box-sizing: border-box; resize: vertical; }
.mono { font-family: var(--mono); }
/* 表基座走全局 .data-table;这里留紧凑 padding 差异。 */
.table th { padding: 6px 10px; }
.table td { padding: 5px 10px; word-break: break-all; }
.coll { list-style: none; margin: 0; padding: 0; font-size: 13px; }
.coll li { padding: 4px 6px; border-bottom: 1px solid var(--border); word-break: break-all; }
.coll-row { display: flex; align-items: center; gap: 8px; }
.coll-row .idx { flex: none; min-width: 24px; text-align: right; color: var(--text-tertiary); }
.grow { flex: 1; min-width: 0; word-break: break-all; }
.row-actions { display: flex; gap: 6px; justify-content: flex-end; white-space: nowrap; }
.add-row { display: flex; gap: 8px; align-items: center; margin-top: 10px; flex-wrap: wrap; }
.add-row .input { flex: 1; min-width: 120px; }
.check { display: flex; align-items: center; gap: 4px; font-size: 12px; color: var(--text-secondary); white-space: nowrap; }
.empty { text-align: center; color: var(--text-tertiary); padding: 20px; font-size: 13px; }
.coll-row { flex-wrap: nowrap; }
.coll-row.editing { flex-wrap: wrap; }
.coll-row.editing .edit-area { width: 100%; min-height: 120px; resize: vertical; }
.coll-row.editing .edit-actions { display: flex; gap: 8px; width: 100%; justify-content: flex-end; }
.coll-value { white-space: pre-wrap; word-break: break-all; }
td .edit-area { width: 100%; min-height: 90px; resize: vertical; }
.msg { padding: 8px 10px; font-size: 13px; border-radius: 7px; margin: 8px 0; }
.msg.err { background: var(--danger-soft); color: var(--danger); }
.msg.warn { background: var(--warn-soft); color: var(--warn); display: flex; gap: 8px; align-items: center; }
/* 基座走全局 .btn;键值编辑小按钮更紧凑(局部修饰)。红边删除按钮走全局 ghost.danger。 */
.btn.small { padding: 3px 9px; }
</style>
