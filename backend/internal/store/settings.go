// settings.go 提供应用级 key-value 设置(app_settings 表)的读写。设置值
// 为不加密的普通文本(当前仅承载「生产安全模式」这类非敏感开关),键不
// 存在时由调用方决定默认值,本层不做语义解释。
package store

import (
	"database/sql"
	"errors"
	"fmt"
)

// SettingReadonlyMode 是「生产安全模式(全局只读)」的设置键;值为 "1"
// 表示开启,"0" 或键缺失表示关闭(语义解释在 service 层)。
const SettingReadonlyMode = "readonly_mode"

// GetSetting returns the value stored under key. The second return is false
// when the key does not exist yet (caller picks the default).
func (s *Store) GetSetting(key string) (string, bool, error) {
	var value string
	if err := s.db.QueryRow(`SELECT value FROM app_settings WHERE key = ?`, key).Scan(&value); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("get setting %s: %w", key, err)
	}
	return value, true, nil
}

// SetSetting upserts the value under key (INSERT OR REPLACE semantics).
func (s *Store) SetSetting(key, value string) error {
	if key == "" {
		return fmt.Errorf("setting key must not be empty")
	}
	if _, err := s.db.Exec(
		`INSERT INTO app_settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		key, value,
	); err != nil {
		return fmt.Errorf("set setting %s: %w", key, err)
	}
	return nil
}
