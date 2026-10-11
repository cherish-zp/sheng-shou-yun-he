// SQL 控制台共用:语句摘要/结果 tab 标签、语句标记(运行中/成功/失败)与
// 光标定位「执行当前语句」。行为与既有五个控制台逐字一致;语句拆分函数
// (split fn)因方言而异,经参数注入。
import { computed, ref, type Ref } from 'vue'
import { commentAbove, type SqlSegment } from '@/utils/sqlSplit'
import type { ResultTabItem } from '@/components/common/SqlResultTabs.vue'
import type { MarkedStatement } from './useConsoleStatementRefresh'

// 语句单行摘要:第一条非空行(trim,超长截断),供 tab 悬浮提示。
export function statementSummary(text: string): string {
  const line = (text.split('\n').find((l) => l.trim() !== '') ?? '').trim()
  return line.length > 80 ? `${line.slice(0, 80)}…` : line
}

// tab 标签:语句起始 offset 上方最近的注释;无注释回退「结果 N」。
// doc 为注释检索的文本:新查询 = 编辑器当前内容,翻页 = 重放的脚本。
export function tabLabel(doc: string, from: number, index: number): string {
  return commentAbove(doc, from) ?? `结果 ${index + 1}`
}

// 发起「运行全部」:按拆分段生成 running tab,active 指向第 0 个。
export function tabsFromSegments(segs: SqlSegment[], doc: string): ResultTabItem[] {
  return segs.map((s, i) => ({
    label: tabLabel(doc, s.from, i),
    status: 'running',
    title: statementSummary(s.text),
  }))
}

// 运行完成:按结果 error 映射 ok/fail;结果可能少于段数(后端遇错即停)。
export function applyTabStatus(
  tabs: ResultTabItem[],
  res: { error?: string }[],
): ResultTabItem[] {
  return tabs.slice(0, res.length).map((t, i) => ({
    ...t,
    status: res[i]?.error ? 'fail' : 'ok',
  }))
}

// 返回结果与发起文本按顺序映射成语句标记:耗时来自 duration_ms,错误用原文。
export function marksFromResults<R extends { error?: string; duration_ms: number }>(
  texts: string[],
  res: R[],
  statementOf: (r: R) => string,
): MarkedStatement[] {
  return res.map((r, i): MarkedStatement => ({
    text: texts[i] ?? statementOf(r),
    status: r.error ? 'fail' : 'ok',
    detail: r.error ?? `${r.duration_ms} ms`,
  }))
}

// 语句状态标记:markedStatements 保存最近一次运行的语句级状态(运行中/成功/
// 失败);编辑器内容变化后按语句文本(trim 后相等)对 split 结果重新定位,
// 匹配不到的丢弃。
export function useStatementMarks(opts: {
  sql: Ref<string>
  split: (text: string) => SqlSegment[]
}): { markedStatements: Ref<MarkedStatement[]>; statementMarks: Ref<{ from: number; status: 'ok' | 'fail' | 'running'; detail?: string }[]> } {
  const markedStatements = ref<MarkedStatement[]>([])

  const statementMarks = computed(() => {
    const segs = opts.split(opts.sql.value)
    const used = new Set<number>()
    const marks: { from: number; status: 'ok' | 'fail' | 'running'; detail?: string }[] = []
    for (const m of markedStatements.value) {
      const idx = segs.findIndex((s, i) => !used.has(i) && s.text.trim() === m.text.trim())
      if (idx === -1) continue
      used.add(idx)
      marks.push({ from: segs[idx].from, status: m.status, detail: m.detail })
    }
    return marks
  })

  return { markedStatements, statementMarks }
}

// 光标位置与「执行当前语句」:SqlEditor 的 cursor emit(1 基行列)记录最新
// 光标;⌘Enter 时把行列换算成文本 offset,在 split 结果中定位光标所在语句段
// (段间空白归属前一段)。
export function useConsoleCursor(opts: {
  sql: Ref<string>
  split: (text: string) => SqlSegment[]
}): {
  cursorPos: Ref<{ line: number; col: number }>
  onCursor(pos: { line: number; col: number }): void
  statementAtCursor(): string | null
} {
  const cursorPos = ref({ line: 1, col: 1 })

  function onCursor(pos: { line: number; col: number }): void {
    cursorPos.value = { line: pos.line, col: pos.col }
  }

  function cursorOffset(text: string, pos: { line: number; col: number }): number {
    const lines = text.split('\n')
    let offset = 0
    for (let i = 0; i < Math.min(pos.line - 1, lines.length); i++) offset += lines[i].length + 1
    const lineText = lines[Math.min(Math.max(pos.line - 1, 0), lines.length - 1)] ?? ''
    return offset + Math.min(Math.max(pos.col - 1, 0), lineText.length)
  }

  function statementAtCursor(): string | null {
    const segs = opts.split(opts.sql.value)
    if (segs.length === 0) return null
    const offset = cursorOffset(opts.sql.value, cursorPos.value)
    const seg =
      segs.find((s) => offset >= s.from && offset <= s.to) ??
      [...segs].reverse().find((s) => s.to <= offset)
    return (seg ?? segs[0]).text
  }

  return { cursorPos, onCursor, statementAtCursor }
}
