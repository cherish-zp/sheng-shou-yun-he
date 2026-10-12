export function prettyJSON(input: string): string {
  try {
    return JSON.stringify(JSON.parse(input), null, 2)
  } catch {
    return input
  }
}

export function formatTime(ms: number): string {
  if (!ms) return '-'
  return new Date(ms).toLocaleString()
}

export function displayValue(v: string | undefined | null): string {
  if (v == null || v === '') return '(empty)'
  return v
}

// 列表单元格截断展示:沿用 displayValue 的空值语义(null/空串 → '(empty)',
// 保留消息列表原可见行为,仅新增截断),>200 字符截断 + … 后缀,空白串原样
// 透传不 trim。列表 DOM 只保留截断文本,完整值仍由详情抽屉用不截断的
// displayValue 呈现。
export const CELL_TRUNCATE = 200

export function truncateCell(v: string | undefined | null): string {
  if (v == null || v === '') return '(empty)'
  return v.length > CELL_TRUNCATE ? `${v.slice(0, CELL_TRUNCATE)}…` : v
}

// formatCount abbreviates a record count for the tree badges: 1200 -> "1.2K",
// 1000000 -> "1M" (trailing zeros dropped). Small and negative counts render
// verbatim.
export function formatCount(n: number): string {
  const abs = Math.abs(n)
  const units: [number, string][] = [
    [1e9, 'B'],
    [1e6, 'M'],
    [1e3, 'K'],
  ]
  for (const [size, suffix] of units) {
    if (abs >= size) {
      const trimmed = String(parseFloat((n / size).toFixed(1)))
      return `${trimmed}${suffix}`
    }
  }
  return String(n)
}

// decodeBytes renders a raw byte buffer (base64/string) for display purposes.
export function bytesToString(v: string | undefined | null): string {
  return displayValue(v)
}
