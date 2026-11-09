// app_settings.go 暴露应用级设置给前端:「生产安全模式(全局只读)」开关
// 的读取与持久化。开关本身是本地设置,开启/关闭不受只读模式限制(否则开
// 关将无法关闭)。
package backend

// AppSettings mirrors the persisted app settings for the frontend. snake_case
// JSON 字段与全仓约定一致。
type AppSettings struct {
	ReadonlyMode bool `json:"readonly_mode"`
}

// SetAppSettingsRequest carries the settings fields the frontend may change.
// 布尔直传:前端每次提交完整开关状态(非增量 patch)。
type SetAppSettingsRequest struct {
	ReadonlyMode bool `json:"readonly_mode"`
}

// GetAppSettings returns the current app settings. 键缺失(首次使用)时
// readonly_mode 为零值 false,与后端默认语义一致。
func (a *App) GetAppSettings() (*AppSettings, error) {
	ctx, cancel := a.newContext()
	defer cancel()
	return &AppSettings{ReadonlyMode: a.svc.IsReadonlyMode(ctx)}, nil
}

// SetAppSettings persists the app settings and echoes them back.
func (a *App) SetAppSettings(req SetAppSettingsRequest) (*AppSettings, error) {
	ctx, cancel := a.newContext()
	defer cancel()
	if err := a.svc.SetReadonlyMode(ctx, req.ReadonlyMode); err != nil {
		return nil, err
	}
	return &AppSettings{ReadonlyMode: req.ReadonlyMode}, nil
}
