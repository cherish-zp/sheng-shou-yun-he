// common_query.go 汇聚 std 适配层数据源(CH/MySQL/PG)共用的查询管线公共件:
// 结果集行收集、SQL 控制台分页包装与计数、语句判型。方言差异以参数/钩子
// 注入,公共函数本身不含任何数据源专属语法;Hive 走自有 HiveRows 接口与
// ROW_NUMBER 方言,不经此文件。
package service

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
)

// --- 行收集 ---

// collectStdRowsN 是通用行收集骨架:按 database/sql 结果集列类型构造 wire 列
// (newCol 钩子产出各源的列模型),逐行把单元格扫进 *any 后经 formatCell 钩子
// 渲染为字符串单元格(nil = NULL),至多消费 max 行(max<=0 不限制,供 SHOW/
// DESC/EXPLAIN 等不能包装的语句做 offset+limit 截断)。各源的时间/字节格式化
// 差异全部收敛在 formatCell,收集逻辑本身与方言无关。
func collectStdRowsN[C any](rows *sql.Rows, max int, newCol func(name, dbType string) C, formatCell func(v any, colType string) *string) ([]C, [][]*string, error) {
	types, err := rows.ColumnTypes()
	if err != nil {
		return nil, nil, fmt.Errorf("column types: %w", err)
	}
	cols := make([]C, len(types))
	colTypes := make([]string, len(types))
	for i, ct := range types {
		cols[i] = newCol(ct.Name(), ct.DatabaseTypeName())
		colTypes[i] = ct.DatabaseTypeName()
	}
	// 通用扫描:每个单元格扫进 *any;std 适配层对 NULL 行给出 nil。
	width := len(types)
	values := make([]any, width)
	pointers := make([]any, width)
	var out [][]*string
	for rows.Next() {
		if max > 0 && len(out) >= max {
			break
		}
		for i := range values {
			pointers[i] = &values[i]
		}
		if err := rows.Scan(pointers...); err != nil {
			return nil, nil, fmt.Errorf("scan row: %w", err)
		}
		row := make([]*string, width)
		for i, v := range values {
			row[i] = formatCell(v, colTypes[i])
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	return cols, out, nil
}

// formatStdCell 是 CH/PG 共用的单元格渲染钩子:两源都直接走 FormatCHCell
// (RFC3339 时间、超长截断),列类型不参与渲染。
func formatStdCell(v any, _ string) *string {
	return FormatCHCell(v, CHCellMaxBytes)
}

// --- SQL 控制台分页包装与计数 ---

// stdQueryer 是 std 适配层数据源(*sql.DB、*sql.Conn 等)共有的查询面。
type stdQueryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// wrapLimitOffsetPagedQuery 渲染 LIMIT/OFFSET 方言(CH/MySQL/PG)的分页包装:
// 原文(尾分号剥离)成为派生表 alias,页窗口以字面量 LIMIT/OFFSET 追加(整数,
// 无注入面);语句顶层以 ORDER BY 结尾且其后无 LIMIT/FOR 等尾巴时,把 ORDER
// BY 原样外提到包装外层(派生表不保证保留子查询内的 ORDER BY,跨页顺序可能
// 不稳)。alias 承载方言差异(CH/MySQL 用 _dbp,PG 用 _pgc)。Hive 无 OFFSET,
// 走 ROW_NUMBER 方言的 wrapHivePagedQuery,不经此函数。
func wrapLimitOffsetPagedQuery(stmt string, limit, offset int, alias string) string {
	body := stripTrailingSemicolon(stmt)
	tail := ""
	if head, order, ok := splitTrailingTopLevelOrderBy(body); ok {
		body, tail = head, " "+order
	}
	return "SELECT * FROM (" + body + ") " + alias + tail + " LIMIT " +
		strconv.Itoa(limit) + " OFFSET " + strconv.Itoa(offset)
}

// countWrappedTotal runs `SELECT <countExpr> FROM (<stmt>) <alias>` and
// returns the exact total; a failing count returns nil (total_rows is then
// omitted rather than guessed)。countExpr/alias 承载方言差异:CH 为小写
// count(),MySQL/PG 为 COUNT(*);派生表别名随源不同(_dbp/_pgc)。
func countWrappedTotal(ctx context.Context, q stdQueryer, stmt, countExpr, alias string) *int64 {
	rows, err := q.QueryContext(ctx, countExpr+" FROM ("+stripTrailingSemicolon(stmt)+") "+alias)
	if err != nil {
		return nil
	}
	defer rows.Close()
	if !rows.Next() {
		return nil
	}
	var total int64
	if err := rows.Scan(&total); err != nil {
		return nil
	}
	return &total
}

// --- 语句判型 ---

// stdStatementReturnsRows 按语句首关键字判型:首关键字属于 result-set 基础集
// (SELECT/SHOW/DESC/DESCRIBE/EXPLAIN/WITH)即应走 Query 而非 Exec;extra 承载
// 方言扩展(CH 的 EXISTS/CHECK)。取词方式保持原实现:不做 TrimSpace,取到
// 首个空白或 '(' 为止(调用方传入的语句已由 SplitSQLStatements 修剪)。
func stdStatementReturnsRows(stmt string, extra ...string) bool {
	first := stmt
	if i := strings.IndexAny(stmt, " \t\n\r("); i >= 0 {
		first = stmt[:i]
	}
	first = strings.ToUpper(first)
	switch first {
	case "SELECT", "SHOW", "DESC", "DESCRIBE", "EXPLAIN", "WITH":
		return true
	}
	for _, kw := range extra {
		if first == kw {
			return true
		}
	}
	return false
}
