// Client-side export helpers: pure CSV/JSONL string builders plus the save
// triggers. Builders are framework-free so they can be fully unit tested; only
// saveFile/downloadFile touch the DOM or the backend.
import { getApi } from '@/api/client'
import type { Message } from '@/api/types'

// ExportColumn describes one CSV column: its header label and how to read the
// cell value out of a row. Generic so callers keep their row types.
export interface ExportColumn<T> {
  label: string
  value: (row: T) => string
}

export const CSV_MIME = 'text/csv'
export const JSONL_MIME = 'application/x-ndjson'
export const XLSX_MIME = 'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet'

// EXT_BY_MIME maps the supported download mimes to file extensions; unknown
// mimes fall back to .txt.
const EXT_BY_MIME: Record<string, string> = {
  [CSV_MIME]: 'csv',
  [JSONL_MIME]: 'jsonl',
  [XLSX_MIME]: 'xlsx',
}

// csvField quotes a field when it contains a comma, double quote, newline or
// carriage return, doubling embedded quotes (RFC 4180).
function csvField(v: string): string {
  return /[",\n\r]/.test(v) ? `"${v.replace(/"/g, '""')}"` : v
}

// exportCsv renders rows as CSV per RFC 4180 quoting with LF row separators,
// prefixed with a UTF-8 BOM so Excel detects the encoding and keeps Chinese
// readable. The header row is always emitted; empty rows yield just the BOM
// plus header.
export function exportCsv<T>(rows: readonly T[], cols: readonly ExportColumn<T>[]): string {
  const lines = [cols.map((c) => csvField(c.label)).join(',')]
  for (const row of rows) {
    lines.push(cols.map((c) => csvField(c.value(row))).join(','))
  }
  return `\uFEFF${lines.join('\n')}`
}

// exportJsonl renders rows as newline-delimited JSON with no trailing
// newline; empty rows yield an empty string.
export function exportJsonl<T>(rows: readonly T[]): string {
  return rows.map((row) => JSON.stringify(row)).join('\n')
}

// downloadFile saves content as <filename>.<ext>, deriving the extension from
// the mime type. The anchor is attached to the document before the click and
// removed afterwards — WKWebView only honours a[download] for attached
// anchors.
export function downloadFile(filename: string, content: string, mime: string): void {
  const url = URL.createObjectURL(new Blob([content], { type: mime }))
  const a = document.createElement('a')
  a.href = url
  a.download = `${filename}.${EXT_BY_MIME[mime] ?? 'txt'}`
  document.body.appendChild(a)
  a.click()
  a.remove()
  // Defer the revoke to the next tick: WKWebView can abort a download if the
  // object URL is revoked before the fetch has started.
  setTimeout(() => URL.revokeObjectURL(url), 0)
}

// saveFile stores an export via the backend's native save dialog. This is the
// primary path because WKWebView (the macOS Wails runtime) has no download
// delegate: <a download> anchor clicks are silently dropped, which made the
// packaged app's exports do nothing. A cancelled dialog (empty path) is a
// silent no-op; a failed backend call (e.g. plain-browser dev without the
// Wails bridge) falls back to the anchor download.
export async function saveFile(filename: string, content: string, mime: string): Promise<void> {
  try {
    // An empty path means the user dismissed the native dialog: a no-op.
    await getApi().saveTextFile({ filename, content, mime })
  } catch {
    downloadFile(filename, content, mime)
  }
}

// base64ToBytes decodes a base64 string into raw bytes. Wails bindings cannot
// return Go []byte as JSON arrays over the bridge — the transport delivers
// them as base64 strings (BuildResultXLSX is the first []byte-return binding
// in the repo), so every binary payload goes through here.
export function base64ToBytes(base64: string): Uint8Array<ArrayBuffer> {
  const bin = atob(base64)
  const bytes = new Uint8Array(new ArrayBuffer(bin.length))
  for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i)
  return bytes
}

// downloadBinaryFile is the anchor-download twin of downloadFile for binary
// payloads: bytes arrive base64-encoded (see base64ToBytes), become a Blob of
// the given mime and trigger a[download] with the mime-derived extension.
export function downloadBinaryFile(filename: string, base64: string, mime: string): void {
  const url = URL.createObjectURL(new Blob([base64ToBytes(base64)], { type: mime }))
  const a = document.createElement('a')
  a.href = url
  a.download = `${filename}.${EXT_BY_MIME[mime] ?? 'txt'}`
  document.body.appendChild(a)
  a.click()
  a.remove()
  // Defer the revoke to the next tick: WKWebView can abort a download if the
  // object URL is revoked before the fetch has started.
  setTimeout(() => URL.revokeObjectURL(url), 0)
}

// saveBinaryFile stores a binary export (e.g. XLSX) via the backend's native
// save dialog, same contract as saveFile: cancelled dialog (empty path) is a
// no-op, a missing binding or failed call (plain-browser dev) falls back to
// the anchor download. base64 travels verbatim — SaveTextFile cannot carry
// binary because a JS→Go JSON string is UTF-8 encoded on the wire.
export async function saveBinaryFile(filename: string, base64: string, mime: string): Promise<void> {
  const path = await getApi().saveBinaryFile?.({ filename, content_base64: base64, mime })
  // undefined = 绑定不可用(纯浏览器 dev)→ 锚点下载兜底;'' = 用户取消对话框
  // → 静默 no-op;非空路径 = 已写盘,无事可做。
  if (path === undefined) downloadBinaryFile(filename, base64, mime)
}

// exportResultXlsx renders a result set as a single-sheet XLSX workbook via
// the backend binding and saves it. 分页导出语义与 CSV/JSONL 一致:只导调用方
// 传入的 rows(分页场景即当前已拉取的行);sheetName 交给后端按 excelize 规则
// 清洗(非法字符/长度)。绑定缺失或构建失败时抛出,由调用方决定提示方式。
export async function exportResultXlsx(req: {
  columns: string[]
  rows: (string | null)[][]
  sheetName: string
  filename: string
}): Promise<void> {
  const base64 = await getApi().buildResultXLSX?.({
    columns: req.columns,
    rows: req.rows,
    sheet_name: req.sheetName,
  })
  if (base64 == null) throw new Error('XLSX 导出绑定不可用')
  await saveBinaryFile(req.filename, base64, XLSX_MIME)
}

// MESSAGE_EXPORT_COLUMNS is the column set shared by the message browse table
// and the SQL result grid. Timestamps export as ISO 8601 (deterministic and
// machine-readable) and empty key/value stay empty cells rather than the
// '(empty)' display placeholder.
export const MESSAGE_EXPORT_COLUMNS: ExportColumn<Message>[] = [
  { label: 'Partition', value: (m) => String(m.partition) },
  { label: 'Offset', value: (m) => String(m.offset) },
  { label: 'Timestamp', value: (m) => (m.timestamp ? new Date(m.timestamp).toISOString() : '') },
  { label: 'Key', value: (m) => m.key ?? '' },
  { label: 'Value', value: (m) => m.value ?? '' },
]
