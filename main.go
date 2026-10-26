// Command sheng-shou-yun-he is the Wails desktop app entry point. It binds the
// backend.App (backend package) to the frontend. A CLI demo lives in
// cmd/dbclient for non-GUI verification.
package main

import (
	"embed"
	"log"
	"os"
	"path/filepath"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/mac"

	"sheng-shou-yun-he/backend"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	home, err := os.UserHomeDir()
	if err != nil {
		log.Fatal(err)
	}
	dbDir := filepath.Join(home, ".db-client")
	dbPath := filepath.Join(dbDir, "config.db")
	// 主密钥解析:DB_CLIENT_MASTER > ~/.db-client/master.key > 首次生成随机
	// 密钥;全部不可用才回退内置默认(ResolveMasterKey 内已向 stderr 告警)。
	// 绝不打印密钥内容。
	master, _ := backend.ResolveMasterKey(os.Getenv("DB_CLIENT_MASTER"), filepath.Join(dbDir, "master.key"), os.Stderr)

	app, closeDB, err := backend.NewDefaultApp(dbPath, master)
	if err != nil {
		log.Fatal(err)
	}
	defer closeDB()

	err = wails.Run(&options.App{
		Title:  "圣手运河",
		Width:  1280,
		Height: 800,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		// 标题栏双栏合一:隐藏原生标题文字、内容铺满整窗(红绿灯保留并悬浮),
		// 应用内 topbar 上移占据原生标题栏那一行,换取更多垂直空间。
		Mac: &mac.Options{
			TitleBar: &mac.TitleBar{
				TitlebarAppearsTransparent: true,
				FullSizeContent:            true,
				HideTitle:                  true,
				HideTitleBar:               false, // 保留红绿灯
			},
		},
		// 与默认主题「少女粉」--bg(#fff5f9)同色,消除启动瞬间窗口底色闪色。
		BackgroundColour: &options.RGBA{R: 255, G: 245, B: 249, A: 1},
		OnStartup:        app.Startup,
		Bind: []interface{}{
			app,
		},
	})
	if err != nil {
		log.Fatal(err)
	}
}
