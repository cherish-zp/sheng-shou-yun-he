// 控制台语句结果共用:语句标记形状与「确认写库后的同页刷新」。
// 刷新语义(与既有 Mysql/Postgres 控制台逐字一致):重放该条语句且与原执行
// 同页(当前页码),仅替换该索引的结果(其他语句结果保持不变),并同步更新
// 该语句的标记与结果 tab 状态(新耗时或错误);刷新请求失败写入页面错误区。
import { type Ref } from 'vue'
import type { ResultTabItem } from '@/components/common/SqlResultTabs.vue'

// 语句级运行状态标记(与各控制台本地定义同形状)。
export interface MarkedStatement {
  text: string
  status: 'ok' | 'fail' | 'running'
  detail?: string
}

export interface StatementRefreshOptions<R extends { duration_ms: number; error?: string }> {
  // 待刷新的结果索引与语句原文(标记按文本 trim 匹配)。
  idx: number
  sql: string
  // 重放执行:与该结果的原执行同页(当前页码)。
  refetch: () => Promise<R[]>
  results: Ref<R[]>
  markedStatements: Ref<MarkedStatement[]>
  resultTabs: Ref<ResultTabItem[]>
  // 刷新失败落点:页面错误展示区(与运行错误共用)。
  onError(msg: string): void
}

export async function refreshStatementInPlace<R extends { duration_ms: number; error?: string }>(
  args: StatementRefreshOptions<R>,
): Promise<void> {
  try {
    const fresh = await args.refetch()
    if (fresh.length > 0) {
      // 仅替换该索引的结果,其他语句结果保持不变。
      args.results.value = args.results.value.map((old, i) => (i === args.idx ? fresh[0] : old))
      // 刷新同样更新该语句的标记与结果 tab 状态(新耗时或错误)。
      const m = args.markedStatements.value.find((x) => x.text.trim() === args.sql.trim())
      if (m) {
        m.status = fresh[0].error ? 'fail' : 'ok'
        m.detail = fresh[0].error ?? `${fresh[0].duration_ms} ms`
      }
      const t = args.resultTabs.value[args.idx]
      if (t) args.resultTabs.value[args.idx] = { ...t, status: fresh[0].error ? 'fail' : 'ok' }
    }
  } catch (e) {
    args.onError(e instanceof Error ? e.message : String(e))
  }
}
