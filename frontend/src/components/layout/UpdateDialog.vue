<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { getApi } from '@/api/client'
import { APP_VERSION } from '@/version'
import { ignoreVersion } from '@/utils/updateIgnore'
import type { UpdateCheckResult } from '@/api/types'
import { Button } from '@/components/ui/button'

const props = defineProps<{ show: boolean }>()
const emit = defineEmits<{ (e: 'close'): void }>()

// 状态机:checking → up-to-date / available → downloading → installing →
// close(installing 期间后端写替换脚本并退出应用,重启后即新版本)。
// 下载进度靠轮询 UpdateProgress(引擎无关,不依赖 Wails 事件绑定);
// 任一环节失败进入 error,提供「重试」与「打开下载页」两个出口。
type Phase = 'checking' | 'uptodate' | 'available' | 'downloading' | 'installing' | 'error'

const phase = ref<Phase>('checking')
const result = ref<UpdateCheckResult | null>(null)
const percent = ref(0)
const errorText = ref('')
let pollTimer: ReturnType<typeof setInterval> | null = null

watch(
  () => props.show,
  (show) => {
    if (!show) {
      stopPolling()
      return
    }
    phase.value = 'checking'
    result.value = null
    errorText.value = ''
    percent.value = 0
    void check()
  },
  { immediate: true },
)

async function check(): Promise<void> {
  phase.value = 'checking'
  try {
    const res = await getApi().checkUpdate({ current_version: APP_VERSION })
    result.value = res
    phase.value = res.has_update ? 'available' : 'uptodate'
  } catch (e) {
    phase.value = 'error'
    errorText.value = e instanceof Error ? e.message : String(e)
  }
}

const downloadURL = computed(() => result.value?.download_url ?? '')

async function install(): Promise<void> {
  // 探测结果没有可下载的资产 = 发布里没有适配当前系统/架构的包。
  if (!downloadURL.value) {
    phase.value = 'error'
    errorText.value = '未找到适配当前系统的安装包'
    return
  }
  phase.value = 'downloading'
  percent.value = 0
  startPolling()
  try {
    // sha256 来自检查结果(发布元数据),透传给后端做完整性校验。
    await getApi().downloadUpdate({ url: downloadURL.value, sha256: result.value?.sha256 })
    // 下载调用返回后再确认终态(轮询与下载并发推进);轮询读到 error 时
    // 已写入具体错误,这里不再覆盖。
    const done = await waitForDone()
    if (!done) return
    emit('close')
  } catch (e) {
    stopPolling()
    phase.value = 'error'
    errorText.value = e instanceof Error ? e.message : String(e)
  }
}

// ignoreThisVersion 记住当前探测到的版本:静默红点不再为它点亮,但手动
// 检查(设置页/顶栏按钮)不受限制,用户主动检查仍会看到该版本。
function ignoreThisVersion(): void {
  if (result.value?.latest_version) ignoreVersion(result.value.latest_version)
  emit('close')
}

// waitForDone 轮询进度到 done;done 时先切到 installing(提示即将重启),
// 再调用 applyUpdate(后端写脚本、原位替换并重启本应用)然后返回 true。
// error 时写入具体错误并返回 false。
async function waitForDone(): Promise<boolean> {
  return await new Promise<boolean>((resolve) => {
    stopPolling()
    pollTimer = setInterval(async () => {
      try {
        const p = await getApi().updateProgress()
        percent.value = p.percent
        if (p.phase === 'done') {
          stopPolling()
          phase.value = 'installing'
          await getApi().applyUpdate({})
          resolve(true)
        } else if (p.phase === 'error') {
          stopPolling()
          phase.value = 'error'
          errorText.value = p.error || '下载失败'
          resolve(false)
        }
      } catch {
        // 单次轮询失败忽略,下一轮再试
      }
    }, 300)
  })
}

function startPolling(): void {
  stopPolling()
  pollTimer = setInterval(async () => {
    try {
      const p = await getApi().updateProgress()
      percent.value = p.percent
    } catch {
      // 忽略单次轮询失败
    }
  }, 300)
}

function stopPolling(): void {
  if (pollTimer) {
    clearInterval(pollTimer)
    pollTimer = null
  }
}

const statusText = computed(() => {
  switch (phase.value) {
    case 'checking':
      return '正在检查更新…'
    case 'uptodate':
      return '已是最新版本'
    case 'available':
      return `发现新版本 ${result.value?.latest_version ?? ''}`
    case 'downloading':
      return `下载中 ${percent.value}%`
    case 'installing':
      return '正在安装，即将重启…'
    default:
      return errorText.value || '更新失败'
  }
})
</script>

<template>
  <Teleport to="body">
    <div v-if="show" class="fixed inset-0 z-[1200] flex items-center justify-center bg-black/50">
      <div
        class="flex w-[380px] max-w-[calc(100vw-48px)] flex-col gap-3 rounded-lg border border-border bg-background p-4 text-foreground shadow-lg"
        data-test="update-dialog"
      >
        <div class="flex items-center justify-between">
          <span class="text-[15px] font-semibold">软件更新</span>
          <Button variant="ghost" size="sm" class="h-6 w-6 p-0 text-muted-foreground" data-test="btn-update-close" @click="emit('close')">✕</Button>
        </div>
        <div class="flex items-center gap-2 text-[13px]" data-test="update-status">
          <span
            v-if="phase === 'checking' || phase === 'downloading' || phase === 'installing'"
            class="size-3 shrink-0 animate-spin rounded-full border-2 border-muted-foreground/40 border-t-primary"
            aria-hidden="true"
          ></span>
          {{ statusText }}
        </div>
        <div
          v-if="phase === 'available' && result?.notes"
          class="max-h-[120px] overflow-auto whitespace-pre-wrap rounded-md border border-border bg-muted/50 px-2.5 py-2 text-xs text-muted-foreground"
          data-test="update-notes"
        >{{ result.notes }}</div>
        <div v-if="phase === 'downloading'" class="h-1.5 overflow-hidden rounded-full bg-muted">
          <div class="h-full bg-primary transition-[width] duration-200" :style="{ width: `${percent}%` }"></div>
        </div>

        <div class="flex justify-end gap-2">
          <Button
            v-if="phase === 'error'"
            variant="outline"
            size="sm"
            type="button"
            data-test="btn-update-open-page"
            @click="getApi().openURL(downloadURL)"
          >
            打开下载页
          </Button>
          <Button
            v-if="phase === 'error'"
            variant="outline"
            size="sm"
            type="button"
            data-test="btn-update-retry"
            @click="install"
          >
            重试
          </Button>
          <Button
            v-if="phase === 'available'"
            size="sm"
            type="button"
            data-test="btn-update-install"
            @click="install"
          >
            立即更新
          </Button>
          <Button
            v-if="phase === 'available'"
            variant="outline"
            size="sm"
            type="button"
            data-test="ignore-update-btn"
            @click="ignoreThisVersion"
          >
            忽略此版本
          </Button>
          <Button
            v-if="phase === 'checking' || phase === 'uptodate' || phase === 'available'"
            variant="outline"
            size="sm"
            type="button"
            data-test="btn-update-later"
            @click="emit('close')"
          >
            {{ phase === 'uptodate' ? '确定' : '以后再说' }}
          </Button>
        </div>
      </div>
    </div>
  </Teleport>
</template>
