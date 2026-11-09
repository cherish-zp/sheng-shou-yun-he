// readonly.go 承载「生产安全模式(全局只读)」:开关落 SQLite(经 store 的
// app_settings 表),Service 层所有会改动外部系统状态的写方法入口经
// ensureWritable 统一拒绝;SQL 控制台 Execute 与 ES DSL 控制台作为「可执行
// 任意写」的例外通道,经 ensureSQLReadOnlySafe / ensureESDSLReadOnlySafe
// 按语句/方法判定放行只读语句。只读操作(浏览/查询/Scan/Preview*)不接入,
// 本地持久化(连接管理、保存的查询、审计)也不属于「外部系统状态」,同样
// 不接入。
package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"sheng-shou-yun-he/backend/internal/store"
)

// ErrReadonlyMode 是只读模式下写操作被拒绝时返回的哨兵错误;文案即面向
// 用户的完整提示(errors.Is 可判型)。
var ErrReadonlyMode = errors.New("生产安全模式已开启,写操作被拒绝")

// readonlyModeOn / readonlyModeOff 是 app_settings 里 readonly_mode 键的值。
const (
	readonlyModeOn  = "1"
	readonlyModeOff = "0"
)

// IsReadonlyMode reports whether the global read-only (production safety)
// mode is on. 键缺失或存储读取失败时视为关闭(故障不应把用户锁死在只读)。
func (s *Service) IsReadonlyMode(ctx context.Context) bool {
	value, ok, err := s.store.GetSetting(store.SettingReadonlyMode)
	if err != nil || !ok {
		return false
	}
	return value == readonlyModeOn
}

// SetReadonlyMode persists the global read-only (production safety) switch.
func (s *Service) SetReadonlyMode(ctx context.Context, on bool) error {
	value := readonlyModeOff
	if on {
		value = readonlyModeOn
	}
	if err := s.store.SetSetting(store.SettingReadonlyMode, value); err != nil {
		return fmt.Errorf("save readonly mode: %w", err)
	}
	return nil
}

// ensureWritable 是所有变更类方法入口的统一闸门:只读模式开启时返回
// ErrReadonlyMode,调用方原样透传(多返回值方法补零值)。
func (s *Service) ensureWritable(ctx context.Context) error {
	if s.IsReadonlyMode(ctx) {
		return ErrReadonlyMode
	}
	return nil
}

// readonlyStmtSnippet 截断回显的语句片段,避免超长 SQL 撑爆错误文案。
func readonlyStmtSnippet(stmt string) string {
	const max = 80
	runes := []rune(stmt)
	if len(runes) > max {
		return string(runes[:max]) + "…"
	}
	return stmt
}

// ensureSQLReadOnlySafe 是 SQL 控制台 Execute 的只读闸门:只读模式下逐语句
// 判型,任一语句不属于结果集关键字(SELECT/SHOW/DESC/DESCRIBE/EXPLAIN/WITH
// 及方言扩展,由各源的 returnsRows 判定函数承载)即整体拒绝,错误文案与
// ensureWritable 一致并附首条违规语句片段。returnsRows 复用各源 Execute
// 内部同一套判型函数,保证「放行集」与控制台正常判型完全一致。
func (s *Service) ensureSQLReadOnlySafe(ctx context.Context, sqlText string, returnsRows func(string) bool) error {
	if !s.IsReadonlyMode(ctx) {
		return nil
	}
	for _, stmt := range SplitSQLStatements(sqlText) {
		if !returnsRows(stmt) {
			return fmt.Errorf("%w: %s", ErrReadonlyMode, readonlyStmtSnippet(stmt))
		}
	}
	return nil
}

// ensureESDSLReadOnlySafe 是 ES DSL 控制台的只读闸门:只读模式下仅放行
// GET/HEAD(幂等读),POST/PUT/DELETE 一律拒绝——DSL 控制台等价于任意
// REST 写入口,无法按路径穷举,按方法判定是最可靠的保守边界。
func (s *Service) ensureESDSLReadOnlySafe(ctx context.Context, method string) error {
	if !s.IsReadonlyMode(ctx) {
		return nil
	}
	m := strings.ToUpper(strings.TrimSpace(method))
	if m == "GET" || m == "HEAD" {
		return nil
	}
	return fmt.Errorf("%w: %s", ErrReadonlyMode, m)
}
