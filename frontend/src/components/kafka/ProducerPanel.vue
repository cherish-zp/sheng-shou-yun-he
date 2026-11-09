<script setup lang="ts">
import { reactive, ref, computed, watch } from 'vue'
import { getApi } from '@/api/client'
import { buildBatchMessages, clampCount, COUNT_MAX, loadTemplates, saveTemplate, type BatchProduceItem } from '@/utils/batchProduce'
import { parseImportFile } from '@/utils/importMessages'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import { Send, Upload } from 'lucide-vue-next'

const props = defineProps<{
  show: boolean
  connectionId: string
  topic: string
  partitions: number[]
}>()
const emit = defineEmits<{ (e: 'close'): void }>()

// 原生 select/checkbox 保留(spec 以 setValue 驱动);ui Input/Textarea 以
// :value/@input 接线(不透传 v-model)。msg/ok/err 为既有 hook 类,与语义
// token 并存(batch-failures 的用例按 .msg.err 选择器定位)。
const SELECT_CLS =
  'h-8 w-full rounded-md border border-input bg-transparent px-2.5 text-[13px] shadow-xs outline-none transition-[color,box-shadow] focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50'

const LABEL_CLS = 'mb-1 block text-xs font-normal text-muted-foreground'

const form = reactive({
  topic: props.topic,
  partition: -1,
  key: '',
  value: '',
  count: 1,
  randomKey: false,
  loop: false,
})
const producing = ref(false)
const ok = ref(false)
const error = ref<string | null>(null)
const batchSummary = ref<{ ok: number; failed: number } | null>(null)
const batchFailures = ref<{ index: number; error: string }[]>([])
const templates = ref<string[]>(loadTemplates())
const showTemplates = ref(false)
const importedMessages = ref<BatchProduceItem[] | null>(null)
const importInput = ref<HTMLInputElement | null>(null)

// 手动修改 value 或切换 topic 后导入的消息即失效：避免新旧内容混淆，也避免
// 面板常驻挂载时把某个 topic 的导入列表误发到另一个 topic。
watch([() => form.value, () => props.topic], () => {
  importedMessages.value = null
})

// 面板随活跃 tab 常驻挂载：打开时用当前 topic 预填；打开状态下切换 tab 时跟随。
// 其余时刻不回写，保证输入框仍可自由编辑。面板常驻挂载，重新打开时旧一轮的
// ok/error 横幅会残留，故 show 变 true 时一并清空。
watch([() => props.show, () => props.topic], ([show]) => {
  if (show) {
    form.topic = props.topic
    ok.value = false
    error.value = null
  }
})

const partitionOptions = computed(() => [
  { label: '自动', value: -1 },
  ...props.partitions.map((p) => ({ label: String(p), value: p })),
])

const valueInvalid = computed(() => form.value.trim() === '')

// 批量行数 > 1 或勾选随机 key 时走批量接口；普通单发保持原接口。
const batchMode = computed(() => form.count > 1 || form.randomKey)

async function produce(): Promise<void> {
  if (valueInvalid.value && !importedMessages.value) return
  producing.value = true
  ok.value = false
  error.value = null
  batchSummary.value = null
  batchFailures.value = []
  try {
    const imported = importedMessages.value
    if (imported) {
      // 导入模式：直接发送解析出的消息，跳过 buildBatchMessages 的 value/count/loop/randomKey 路径。
      // 文件导入的批量值不算手输内容，不写入最近模板。
      await sendBatch(imported, false)
    } else if (!batchMode.value) {
      await getApi().produceMessage({
        connection_id: props.connectionId,
        topic: form.topic.trim(),
        partition: form.partition,
        key: form.key,
        value: form.value,
      })
      ok.value = true
      templates.value = saveTemplate(templates.value, form.value)
      form.key = ''
      form.value = ''
    } else {
      await sendBatch(
        buildBatchMessages({
          value: form.value,
          key: form.key,
          count: clampCount(form.count),
          randomKey: form.randomKey,
          loop: form.loop,
        }),
      )
    }
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e)
  } finally {
    producing.value = false
  }
}

async function sendBatch(messages: BatchProduceItem[], recordTemplates = true): Promise<void> {
  const results = await getApi().produceMessages({
    connection_id: props.connectionId,
    topic: form.topic.trim(),
    partition: form.partition,
    messages,
  })
  const failed = results.filter((r) => r.error !== '')
  batchSummary.value = { ok: results.length - failed.length, failed: failed.length }
  // Cap rendered failures to COUNT_MAX so a huge failing batch cannot freeze the UI.
  batchFailures.value = failed.slice(0, COUNT_MAX).map((r) => ({ index: r.index, error: r.error }))
  // 只把成功发送的 value 记入最近模板（循环模式下值相同，自动去重）。
  // 最近模板属于手输批量值（3.1）；文件导入的批量值不写入。
  if (!recordTemplates) return
  const sent: string[] = []
  for (let i = 0; i < messages.length; i++) {
    if (!failed.some((f) => f.index === i) && !sent.includes(messages[i].value)) {
      sent.push(messages[i].value)
    }
  }
  for (const v of sent) templates.value = saveTemplate(templates.value, v)
}

function openImport(): void {
  importInput.value?.click()
}

function readFileAsText(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => resolve(String(reader.result))
    reader.onerror = () => reject(reader.error ?? new Error('读取文件失败'))
    reader.readAsText(file)
  })
}

async function onImportInput(e: Event): Promise<void> {
  const input = e.target as HTMLInputElement
  const file = input.files?.[0]
  if (!file) return
  try {
    const text = await readFileAsText(file)
    importedMessages.value = parseImportFile(text, file.name)
    error.value = null
    batchSummary.value = null
    batchFailures.value = []
  } catch (err) {
    importedMessages.value = null
    error.value = err instanceof Error ? err.message : String(err)
  } finally {
    // 清空选择，允许再次选择同一文件。
    try {
      input.value = ''
    } catch {
      // jsdom 对 file input 赋值可能抛错，忽略。
    }
  }
}

function fillTemplate(tpl: string): void {
  form.value = tpl
  showTemplates.value = false
}

function onTopicInput(e: Event): void {
  form.topic = (e.target as HTMLInputElement).value
}

function onKeyInput(e: Event): void {
  form.key = (e.target as HTMLInputElement).value
}

// looseToNumber 与 v-model.number 同语义:不可解析时保留原串。
function looseToNumber(v: string): number | string {
  const n = Number.parseFloat(v)
  return Number.isNaN(n) ? v : n
}

function onCountInput(e: Event): void {
  form.count = looseToNumber((e.target as HTMLInputElement).value) as number
}

function onValueInput(e: Event): void {
  form.value = (e.target as HTMLTextAreaElement).value
}

function close(): void {
  emit('close')
}
</script>

<template>
  <div v-if="show" class="fixed inset-0 z-[900] flex items-center justify-center bg-black/50" data-test="producer-panel" @click.self="close">
    <div class="flex max-h-[88vh] w-[480px] max-w-[92vw] flex-col overflow-auto rounded-lg border border-border bg-background text-foreground shadow-lg">
      <div class="flex items-center justify-between border-b border-border px-4 py-3">
        <span class="text-[15px] font-semibold">生产消息</span>
        <Button variant="ghost" size="sm" class="h-7 w-7 p-0 text-muted-foreground" type="button" data-test="modal-close" @click="close">✕</Button>
      </div>
      <div class="p-4">
        <div class="mb-3">
          <Label :class="LABEL_CLS">Topic</Label>
          <Input :value="form.topic" data-test="input-topic" class="text-[13px]" @input="onTopicInput" />
        </div>
        <div class="mb-3">
          <Label :class="LABEL_CLS">Partition</Label>
          <select v-model="form.partition" data-test="input-partition" :class="SELECT_CLS">
            <option v-for="opt in partitionOptions" :key="opt.value" :value="opt.value">{{ opt.label }}</option>
          </select>
        </div>
        <div class="mb-3">
          <Label :class="LABEL_CLS">Key（可选）</Label>
          <Input :value="form.key" data-test="input-key" class="text-[13px]" placeholder="分区键" @input="onKeyInput" />
        </div>
        <div class="mb-3">
          <div class="flex items-center gap-3.5">
            <Label class="text-xs font-normal text-muted-foreground" for="batch-count">批量行数</Label>
            <Input
              id="batch-count"
              :value="form.count"
              data-test="input-count"
              class="h-8 w-[84px] text-[13px]"
              type="number"
              min="1"
              max="1000"
              @input="onCountInput"
            />
            <span v-if="form.count > COUNT_MAX" class="whitespace-nowrap text-xs text-warning" data-test="count-cap-hint">将按上限 {{ COUNT_MAX }} 发送</span>
            <label class="flex cursor-pointer items-center gap-1.5 text-xs text-muted-foreground"><input v-model="form.randomKey" data-test="input-random-key" type="checkbox" class="accent-primary" /> 随机 key</label>
            <label class="flex cursor-pointer items-center gap-1.5 text-xs text-muted-foreground"><input v-model="form.loop" data-test="input-loop" type="checkbox" class="accent-primary" /> 循环发送</label>
          </div>
        </div>
        <div class="mb-3">
          <div class="flex items-center gap-3.5">
            <Button variant="outline" size="sm" type="button" data-test="import-file" @click="openImport">
              <Upload />
              导入文件
            </Button>
            <input ref="importInput" class="hidden" type="file" accept=".jsonl,.ndjson,.json,.csv,text/plain" data-test="import-input" @change="onImportInput" />
          </div>
          <div v-if="importedMessages" class="msg ok mt-2 rounded-md bg-success/10 px-2.5 py-1.5 text-[13px] text-success" data-test="import-summary">
            已导入 {{ importedMessages.length }} 条消息，点击「发送」发送
          </div>
        </div>
        <div class="mb-3">
          <div class="mb-1 flex items-center justify-between">
            <Label :class="LABEL_CLS">Value <span class="text-destructive">*</span></Label>
            <Button v-if="templates.length > 0" variant="ghost" size="sm" class="h-6 px-2 text-xs" type="button" data-test="template-toggle" @click="showTemplates = !showTemplates">
              最近模板 ▾
            </Button>
          </div>
          <div v-if="showTemplates" class="mb-2 flex flex-col gap-1">
            <Button
              v-for="(tpl, i) in templates"
              :key="i"
              variant="ghost"
              size="sm"
              class="h-auto justify-start truncate px-2 py-1 text-left font-normal text-xs text-muted-foreground"
              type="button"
              data-test="template-item"
              :title="tpl"
              @click="fillTemplate(tpl)"
            >
              {{ tpl.length > 48 ? tpl.slice(0, 48) + '…' : tpl }}
            </Button>
          </div>
          <Textarea
            :value="form.value"
            data-test="input-value"
            class="min-h-[140px] resize-y font-mono text-[13px]"
            rows="6"
            placeholder='{"key":"value"}'
            @input="onValueInput"
          ></Textarea>
          <span v-if="valueInvalid && !importedMessages" class="text-xs text-destructive" data-test="produce-value-error">消息内容不能为空</span>
        </div>

        <div v-if="ok" class="msg ok mt-2 rounded-md bg-success/10 px-2.5 py-1.5 text-[13px] text-success" data-test="produce-ok">消息已发送</div>
        <div v-if="batchSummary" class="mt-2 rounded-md px-2.5 py-1.5 text-[13px]" :class="batchSummary.failed > 0 ? 'msg err bg-destructive/10 text-destructive' : 'msg ok bg-success/10 text-success'" data-test="batch-summary">
          成功 {{ batchSummary.ok }} / 失败 {{ batchSummary.failed }}
        </div>
        <div v-if="batchFailures.length > 0" data-test="batch-failures">
          <div v-for="f in batchFailures" :key="f.index" class="msg err mt-2 rounded-md bg-destructive/10 px-2.5 py-1.5 text-[13px] text-destructive">第 {{ f.index + 1 }} 条：{{ f.error }}</div>
        </div>
        <div v-if="error" class="msg err mt-2 rounded-md bg-destructive/10 px-2.5 py-1.5 text-[13px] text-destructive" data-test="produce-error">{{ error }}</div>
      </div>
      <div class="flex justify-end gap-2 border-t border-border px-4 py-3">
        <Button variant="ghost" size="sm" type="button" @click="close">取消</Button>
        <Button variant="default" size="sm" type="button" data-test="btn-produce" :disabled="producing || (valueInvalid && !importedMessages)" @click="produce">
          <Send />
          {{ producing ? '发送中…' : '发送' }}
        </Button>
      </div>
    </div>
  </div>
</template>
