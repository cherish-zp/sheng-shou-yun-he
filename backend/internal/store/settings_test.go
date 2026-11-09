package store

import (
	"testing"
)

// TestGetSetSettingRoundTrip 覆盖 app_settings 的键缺失、写入、覆盖三条路径:
// 缺失键返回 ok=false 由调用方取默认;Set 为 upsert,重复写覆盖旧值。
func TestGetSetSettingRoundTrip(t *testing.T) {
	st := newTestStore(t)

	if _, ok, err := st.GetSetting(SettingReadonlyMode); err != nil || ok {
		t.Fatalf("missing key: ok=%v err=%v, want ok=false err=nil", ok, err)
	}

	if err := st.SetSetting(SettingReadonlyMode, "1"); err != nil {
		t.Fatalf("set setting: %v", err)
	}
	got, ok, err := st.GetSetting(SettingReadonlyMode)
	if err != nil || !ok {
		t.Fatalf("get after set: ok=%v err=%v, want ok=true err=nil", ok, err)
	}
	if got != "1" {
		t.Fatalf("value = %q, want %q", got, "1")
	}

	// 覆盖写:同一键二次写入替换旧值(生产安全模式开关往复切换路径)。
	if err := st.SetSetting(SettingReadonlyMode, "0"); err != nil {
		t.Fatalf("overwrite setting: %v", err)
	}
	if got, _ := st.mustGetSettingForTest(t, SettingReadonlyMode); got != "0" {
		t.Fatalf("value after overwrite = %q, want %q", got, "0")
	}

	// 空键拒绝:防止误写不可寻址的行。
	if err := st.SetSetting("", "x"); err == nil {
		t.Fatal("empty key must be rejected")
	}
}

func (s *Store) mustGetSettingForTest(t *testing.T, key string) (string, bool) {
	t.Helper()
	v, ok, err := s.GetSetting(key)
	if err != nil {
		t.Fatalf("get setting %s: %v", key, err)
	}
	return v, ok
}
