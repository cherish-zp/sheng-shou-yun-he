<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import * as App from '../../../wailsjs/go/backend/App'
import { useToastStore } from '@/store/toast'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Plus, Trash2 } from 'lucide-vue-next'

const props = defineProps<{
  connectionId: string
  // 树分段入口:initialTemplate 指向待选中的模板名(树模板项单击),
  // newMode 表示进入新建态(分区标题 +)。由 watch 响应变化。
  initialTemplate?: string
  newMode?: boolean
}>()

interface EsTemplateMeta {
  name: string
  order: number
}

// Es* 模板绑定尚未由 wails generate module 生成,这里按显式形状断言调用
// (与 EsTableBrowser 同一模式);生成后签名一致,无需改动本文件。
// 走 legacy /_template API(6.1 OSS 可用),非 composable 模板。
const app = App as unknown as {
  ListEsTemplates(id: string): Promise<EsTemplateMeta[]>
  GetEsTemplate(req: { connection_id: string; name: string }): Promise<{ template_json: string }>
  PutEsTemplate(req: { connection_id: string; name: string; template_json: string }): Promise<void>
  DeleteEsTemplate(req: { connection_id: string; name: string }): Promise<void>
}

const toast = useToastStore()

const templates = ref<EsTemplateMeta[]>([])
const loading = ref(false)
const error = ref<string | null>(null)
// 首次列表是否拉取成功 + 在途请求句柄:定位入口在列表未就绪时等待/补拉,
// 避免与挂载期加载重复请求。
let listLoaded = false
let listPromise: Promise<void> = Promise.resolve()

const search = ref('')
// 当前编辑目标:selectedName 指向已有模板;新建态用 isNew 标记(名称可编辑)。
// 两者皆空时右栏显示空态引导。
const selectedName = ref<string | null>(null)
const isNew = ref(false)
const nameDraft = ref('')
const jsonDraft = ref('')
const saving = ref(false)

// 删除确认:待删模板名与弹窗开关(ConfirmDialog Teleport 到 body)。
const confirmDelete = ref(false)
const deleteName = ref('')

// ui Input 以 :value/@input 接线(不透传 v-model);名称/JSON 编辑器仍为原生
// 元素驱动(spec 按 .element.value 断言)。
function onSearchInput(e: Event): void {
  search.value = (e.target as HTMLInputElement).value
}

function onNameInput(e: Event): void {
  nameDraft.value = (e.target as HTMLInputElement).value
}

function onJsonInput(e: Event): void {
  jsonDraft.value = (e.target as HTMLTextAreaElement).value
}

const filtered = computed(() => {
  const q = search.value.trim().toLowerCase()
  if (!q) return templates.value
  return templates.value.filter((t) => t.name.toLowerCase().includes(q))
})

function errMsg(e: unknown): string {
  return e instanceof Error ? e.message : String(e)
}

// prettyJson 将后端返回的 template_json 格式化为两空格缩进;解析失败原样
// 展示(前端不重复做语法校验,保存时由后端拒绝并透出错误)。
function prettyJson(s: string): string {
  try {
    return JSON.stringify(JSON.parse(s), null, 2)
  } catch {
    return s
  }
}

async function loadList(): Promise<void> {
  loading.value = true
  error.value = null
  listPromise = (async () => {
    try {
      const list = await app.ListEsTemplates(props.connectionId)
      // wire 形状防御:后端异常时按空数组渲染,避免模板期 TypeError。
      templates.value = Array.isArray(list) ? list : []
      listLoaded = true
    } catch (e) {
      error.value = errMsg(e)
    } finally {
      loading.value = false
    }
  })()
  return listPromise
}

// ensureList 保证定位前列表已就绪:加载中等待完成,未加载(含首拉失败)则补拉。
async function ensureList(): Promise<void> {
  if (loading.value) {
    await listPromise
    return
  }
  if (!listLoaded) await loadList()
}

onMounted(() => {
  void boot()
})

// applyInitial 应用树定位 props:newMode 优先进入新建态;initialTemplate 非空
// 选中该模板并载入内容(列表无此项时忽略);两者都空维持现状。
async function applyInitial(): Promise<void> {
  if (props.newMode) {
    startNew()
    return
  }
  const name = props.initialTemplate
  if (!name) return
  await ensureList()
  const meta = templates.value.find((t) => t.name === name)
  if (!meta) return
  await selectTemplate(meta)
}

// boot 挂载初始化:先拉列表再应用初始定位 props。
async function boot(): Promise<void> {
  await ensureList()
  await applyInitial()
}

// Layout 以 :key=active.id 挂载面板,同 tab 重复打开不会重挂载:定位/新建
// 意图变化必须由 watch 响应 props(普通打开 → 定位到模板 / 新建态)。
watch([() => props.initialTemplate, () => props.newMode], () => {
  void applyInitial()
})

// selectTemplate 选中已有模板:锁定名称并载入 pretty JSON;重复点击同一
// 模板不重复请求。
async function selectTemplate(t: EsTemplateMeta): Promise<void> {
  if (!isNew.value && selectedName.value === t.name) return
  selectedName.value = t.name
  isNew.value = false
  nameDraft.value = t.name
  error.value = null
  try {
    const res = await app.GetEsTemplate({ connection_id: props.connectionId, name: t.name })
    jsonDraft.value = prettyJson(res.template_json ?? '')
  } catch (e) {
    error.value = errMsg(e)
  }
}

// startNew 进入新建态:名称可编辑、编辑器清空,保存时才下发名称。
function startNew(): void {
  selectedName.value = null
  isNew.value = true
  nameDraft.value = ''
  jsonDraft.value = ''
  error.value = null
}

// 空态引导示例:legacy 模板三段结构(index_patterns/settings/mappings)。
const SAMPLE_JSON = JSON.stringify(
  {
    index_patterns: ['log-*'],
    settings: { number_of_shards: 1 },
    mappings: {
      properties: {
        '@timestamp': { type: 'date' },
        level: { type: 'keyword' },
      },
    },
  },
  null,
  2,
)

// fillSample 一键填入示例模板并进入新建态。
function fillSample(): void {
  selectedName.value = null
  isNew.value = true
  nameDraft.value = ''
  jsonDraft.value = SAMPLE_JSON
  error.value = null
}

// canSave 名称非空才允许保存(新建态名称即模板名,编辑态名称锁定不变)。
const canSave = computed(() => nameDraft.value.trim().length > 0 && !saving.value)

// save 保存模板(PUT /_template/{name}):新建态以输入的名称创建,编辑态
// 整体替换;成功后 toast、刷新列表并停留在该模板。
async function save(): Promise<void> {
  const name = nameDraft.value.trim()
  if (!name || saving.value) return
  saving.value = true
  error.value = null
  try {
    await app.PutEsTemplate({ connection_id: props.connectionId, name, template_json: jsonDraft.value })
    toast.show('模板已保存')
    isNew.value = false
    selectedName.value = name
    nameDraft.value = name
    await loadList()
  } catch (e) {
    error.value = errMsg(e)
  } finally {
    saving.value = false
  }
}

const deleteMessage = computed(() => `确认删除模板「${deleteName.value}」?此操作不可恢复。`)

// askDelete 删除前危险确认:仅已有模板可删(新建态无后端对象)。
function askDelete(): void {
  if (isNew.value || !selectedName.value) return
  deleteName.value = selectedName.value
  confirmDelete.value = true
}

// doDelete 确认删除:清空编辑器回到空态引导并刷新列表;失败时弹窗已关、
// 错误留在错误区,列表保持原状。
async function doDelete(): Promise<void> {
  confirmDelete.value = false
  const name = deleteName.value
  deleteName.value = ''
  if (!name) return
  try {
    await app.DeleteEsTemplate({ connection_id: props.connectionId, name })
    selectedName.value = null
    isNew.value = false
    nameDraft.value = ''
    jsonDraft.value = ''
    await loadList()
  } catch (e) {
    error.value = errMsg(e)
  }
}
</script>

<template>
  <div class="flex h-full gap-3 overflow-hidden p-3 text-foreground" data-test="es-templates-panel">
    <!-- 左栏:模板列表(搜索 + 新建 + 名称/order 徽标);active 为既有 hook 类 -->
    <aside class="flex w-[260px] flex-none flex-col gap-1.5 overflow-auto rounded-lg border border-border bg-card p-2">
      <div class="flex flex-none items-center gap-1.5">
        <Input
          :value="search"
          class="h-8 min-w-0 flex-1 text-xs"
          type="search"
          data-test="es-tpl-search"
          placeholder="🔍 模糊搜索模板…"
          autocapitalize="off"
          autocorrect="off"
          autocomplete="off"
          spellcheck="false"
          @input="onSearchInput"
        />
        <Button variant="ghost" size="sm" class="text-xs" type="button" data-test="es-tpl-new" title="新建模板" @click="startNew">
          <Plus />
          新建
        </Button>
      </div>
      <div v-if="loading" class="px-2 py-1.5 text-xs text-muted-foreground" data-test="es-tpl-loading">加载中…</div>
      <template v-else>
        <div
          v-for="t in filtered"
          :key="t.name"
          class="flex flex-none cursor-pointer items-center gap-1.5 rounded-md px-2 py-1.5 transition-colors"
          :class="!isNew && selectedName === t.name ? 'active bg-accent' : 'hover:bg-accent/50'"
          :data-test="`es-tpl-item-${t.name}`"
          :title="`${t.name}(order ${t.order})`"
          @click="selectTemplate(t)"
        >
          <span
            class="min-w-0 flex-1 truncate font-mono text-[13px]"
            :class="!isNew && selectedName === t.name ? 'active font-semibold text-primary' : ''"
          >{{ t.name }}</span>
          <span
            class="flex-none rounded-full bg-muted px-1.5 font-mono text-[10px] leading-4 text-muted-foreground"
            :class="!isNew && selectedName === t.name ? 'bg-background text-primary' : ''"
            data-test="es-tpl-order"
            title="模板合并顺序(order 越大优先级越高)"
          >{{ t.order }}</span>
        </div>
        <div v-if="filtered.length === 0" class="px-2 py-1.5 text-xs text-muted-foreground" data-test="es-tpl-empty">
          {{ templates.length === 0 ? '（无模板）' : '无匹配模板' }}
        </div>
      </template>
    </aside>

    <!-- 右栏:编辑器 / 空态引导 -->
    <section class="flex min-w-0 flex-1 flex-col overflow-auto rounded-lg border border-border bg-card p-3">
      <div v-if="error" class="mb-2 rounded-md bg-destructive/10 px-2.5 py-1.5 text-[13px] text-destructive" data-test="es-tpl-error">{{ error }}</div>

      <div v-if="!isNew && !selectedName" class="m-auto p-7 text-center text-muted-foreground" data-test="es-tpl-guide">
        <div class="mb-1.5 text-[26px] leading-none">⚙</div>
        <div>索引模板管理</div>
        <div class="mb-3 mt-1.5 break-all font-mono text-xs text-muted-foreground">新建模板:{"index_patterns":["log-*"],"settings":{...},"mappings":{...}}</div>
        <Button variant="outline" size="sm" class="text-xs" type="button" data-test="es-tpl-sample" @click="fillSample">填入示例模板</Button>
      </div>

      <template v-else>
        <div class="mb-2 flex flex-none items-center gap-2">
          <label class="flex min-w-0 flex-1 items-center gap-2 text-xs text-muted-foreground">
            名称
            <Input
              :value="nameDraft"
              class="h-8 min-w-0 flex-1 font-mono text-[13px]"
              type="text"
              data-test="es-tpl-name"
              :disabled="!isNew"
              placeholder="模板名称"
              autocapitalize="off"
              autocorrect="off"
              autocomplete="off"
              spellcheck="false"
              title="新建态可编辑;已有模板名称锁定,改名请另建新模板"
              @input="onNameInput"
            />
          </label>
        </div>
        <textarea
          :value="jsonDraft"
          class="min-h-[240px] w-full flex-1 resize-none rounded-md border border-input bg-transparent p-2 font-mono text-xs leading-relaxed shadow-xs outline-none transition-[color,box-shadow] focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50"
          data-test="es-tpl-editor"
          autocapitalize="off"
          autocorrect="off"
          autocomplete="off"
          spellcheck="false"
          @input="onJsonInput"
        ></textarea>
        <div class="mt-2.5 flex flex-none gap-2">
          <Button variant="default" size="sm" class="text-xs" type="button" data-test="es-tpl-save" :disabled="!canSave" @click="save">
            {{ saving ? '保存中…' : '保存' }}
          </Button>
          <Button
            variant="outline"
            size="sm"
            class="border-destructive/40 text-xs text-destructive hover:bg-destructive/10 hover:text-destructive"
            type="button"
            data-test="es-tpl-delete"
            :disabled="isNew || !selectedName"
            @click="askDelete"
          >
            <Trash2 />
            删除
          </Button>
        </div>
        <div class="mt-2 flex-none text-[11px] text-muted-foreground">保存以该 JSON 整体创建/替换模板(PUT /_template/{名称});JSON 语法错误由后端校验。</div>
      </template>
    </section>

    <!-- 删除模板:危险操作,文案含模板名。 -->
    <ConfirmDialog
      :show="confirmDelete"
      :message="deleteMessage"
      confirm-text="删除"
      danger
      @confirm="doDelete"
      @cancel="confirmDelete = false"
    />
  </div>
</template>
