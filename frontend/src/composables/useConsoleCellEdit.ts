// SQL 控制台结果卡行内编辑状态:目标(结果索引/行索引/列索引)+ 初始草稿;
// null 表示未在编辑。进入编辑的守卫与既有 Mysql/Postgres/CH 控制台逐字一致:
// 运行中或不可编辑结果忽略;PG 的「主键值缺失/NULL 行只读」经 rowLocatable 注入。
import { ref, type ComputedRef, type Ref } from 'vue'

// 行内编辑状态:目标(结果索引/行索引/列索引)+ 初始草稿。
export interface ConsoleCellEdit {
  stmtIndex: number
  rowIndex: number
  colIndex: number
  draft: string
}

export interface ConsoleCellEditOptions<R extends { rows?: (string | null)[][] | null }> {
  running: Ref<boolean>
  results: Ref<R[]>
  // 每条语句结果是否可编辑(索引对齐 results)。
  editableResults: Ref<boolean[]> | ComputedRef<boolean[]>
  // 行定位校验(缺列/NULL 主键的行只读);缺省不限制。
  rowLocatable?: (r: R, rowIndex: number) => boolean
}

export function useConsoleCellEdit<R extends { rows?: (string | null)[][] | null }>(
  opts: ConsoleCellEditOptions<R>,
) {
  const cellEdit = ref<ConsoleCellEdit | null>(null)

  // 卡片的 editing prop:仅当该卡对应单元格处于编辑态时非空。
  function editingFor(stmtIndex: number): { row: number; col: number; draft: string } | null {
    const e = cellEdit.value
    if (!e || e.stmtIndex !== stmtIndex) return null
    return { row: e.rowIndex, col: e.colIndex, draft: e.draft }
  }

  // 进入编辑:原始值取结果行内存(NULL→空草稿)。
  function startCellEdit(stmtIndex: number, rowIndex: number, colIndex: number): void {
    if (opts.running.value || !opts.editableResults.value[stmtIndex]) return
    const r = opts.results.value[stmtIndex]
    if (!r) return
    if (opts.rowLocatable && !opts.rowLocatable(r, rowIndex)) return
    const row = r.rows?.[rowIndex]
    if (!row) return
    cellEdit.value = {
      stmtIndex,
      rowIndex,
      colIndex,
      draft: row[colIndex] ?? '',
    }
  }

  function cancelCellEdit(): void {
    cellEdit.value = null
  }

  return { cellEdit, editingFor, startCellEdit, cancelCellEdit }
}

// 单表 SELECT 的来源定位:后端回传的权威来源(source_database/source_table,
// 按执行时生效库解析)优先;语句未带库名前缀时文本解析出的 database 是空串,
// 会让后端落到连接默认库上查主键(未配默认库则三个探测层全部落空,误报
// 「表无主键」),故仅在 source 缺失(旧后端)时才退回文本解析值。
// (从 MysqlSqlConsole.commitCellEdit 抽出的共享实现,语义逐字保留。)
export function resolveSourceTable(
  r: { source_database?: string; source_table?: string } | null | undefined,
  parsed: { database: string; table: string },
): { database: string; table: string } {
  return {
    database: (r?.source_database || parsed.database),
    table: (r?.source_table || parsed.table),
  }
}
