package backend

import (
	"context"
	"errors"
	"testing"

	"sheng-shou-yun-he/backend/internal/service"
)

// TestAppSettingsRoundTrip 覆盖 App 绑定层的设置读写:默认关闭 → 开启生效 →
// 关闭还原,且开关状态经 service 落盘(SQLite)可回读。
func TestAppSettingsRoundTrip(t *testing.T) {
	app := newTestApp(t)

	settings, err := app.GetAppSettings()
	if err != nil {
		t.Fatalf("GetAppSettings: %v", err)
	}
	if settings.ReadonlyMode {
		t.Fatal("readonly_mode must default to false")
	}

	if _, err := app.SetAppSettings(SetAppSettingsRequest{ReadonlyMode: true}); err != nil {
		t.Fatalf("SetAppSettings(on): %v", err)
	}
	settings, err = app.GetAppSettings()
	if err != nil {
		t.Fatalf("GetAppSettings after on: %v", err)
	}
	if !settings.ReadonlyMode {
		t.Fatal("readonly_mode = false after enabling via SetAppSettings")
	}
	if !app.svc.IsReadonlyMode(context.Background()) {
		t.Fatal("service must see readonly mode on after SetAppSettings")
	}

	if _, err := app.SetAppSettings(SetAppSettingsRequest{ReadonlyMode: false}); err != nil {
		t.Fatalf("SetAppSettings(off): %v", err)
	}
	settings, err = app.GetAppSettings()
	if err != nil {
		t.Fatalf("GetAppSettings after off: %v", err)
	}
	if settings.ReadonlyMode {
		t.Fatal("readonly_mode = true after disabling via SetAppSettings")
	}
}

// TestAppWriteRejectedInReadonlyMode 绑定层冒烟:只读模式下写操作(App.
// CreateTopic)返回只读拒绝,错误文案与 service 哨兵一致。
func TestAppWriteRejectedInReadonlyMode(t *testing.T) {
	app := newTestApp(t)
	if _, err := app.SetAppSettings(SetAppSettingsRequest{ReadonlyMode: true}); err != nil {
		t.Fatalf("SetAppSettings(on): %v", err)
	}
	err := app.CreateTopic(CreateTopicRequest{ConnectionID: "c1", Topic: "t1", Partitions: 1, ReplicationFactor: 1})
	if err == nil {
		t.Fatal("CreateTopic must be rejected in readonly mode")
	}
	if !errors.Is(err, service.ErrReadonlyMode) {
		t.Fatalf("error %v is not service.ErrReadonlyMode", err)
	}
}
