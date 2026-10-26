package backend

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// releaseAsset 是 giteeRelease.Assets 元素匿名结构的别名,便于构造测试数据。
type releaseAsset = struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

// mkAsset 构造一个 release 资产条目。
func mkAsset(name, url string) releaseAsset {
	return releaseAsset{Name: name, BrowserDownloadURL: url}
}

func TestParseVersion(t *testing.T) {
	cases := []struct {
		tag string
		ok  bool
		v   version
	}{
		{"v1.0.0", true, version{1, 0, 0}},
		{"1.2.3", true, version{1, 2, 3}},
		{"v1.0", true, version{1, 0, 0}},
		{"v2.1.0-rc", false, version{}},
		{"latest", false, version{}},
		{"", false, version{}},
	}
	for _, c := range cases {
		v, ok := parseVersion(c.tag)
		if ok != c.ok || (ok && v != c.v) {
			t.Fatalf("parseVersion(%q) = %v, %v; want %v, %v", c.tag, v, ok, c.v, c.ok)
		}
	}
}

func TestCompareVersions(t *testing.T) {
	if compareVersions(version{1, 0, 0}, version{1, 0, 0}) != 0 {
		t.Fatal("equal versions must compare 0")
	}
	if compareVersions(version{1, 1, 0}, version{1, 0, 9}) <= 0 {
		t.Fatal("1.1.0 must be newer than 1.0.9")
	}
	if compareVersions(version{2, 0, 0}, version{1, 9, 9}) <= 0 {
		t.Fatal("2.0.0 must be newer than 1.9.9")
	}
	if compareVersions(version{0, 9, 0}, version{1, 0, 0}) >= 0 {
		t.Fatal("0.9.0 must be older than 1.0.0")
	}
}

func fakeGiteeServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/releases/latest") {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// newDeadServer 返回一个恒 503 的服务器:测试里作为回退源,保证空 sha
// 重查/双源回退绝不出网打到真实 GitHub。
func newDeadServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down", http.StatusServiceUnavailable)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func newUpdateApp(t *testing.T, srv *httptest.Server) *App {
	t.Helper()
	a := &App{}
	a.baseURL.Store(srv.URL)
	a.fallbackURL.Store(newDeadServer(t).URL)
	a.httpClient = srv.Client()
	a.applyCmd = func(*exec.Cmd) error { return nil }
	// 测试源是 httptest 域名,不在生产白名单内,注入旁路。
	a.downloadURLCheck = func(string) error { return nil }
	return a
}

// --- 0. 下载入口白名单与 OpenURL scheme 白名单 ---

// TestValidateUpdateDownloadURL 覆盖入口 URL 校验:仅 https + 白名单域名
// 放行。注意:跟随重定向后的最终 URL 不校验(平台附件域已列入白名单),
// 这里只测入口判定。
func TestValidateUpdateDownloadURL(t *testing.T) {
	cases := []struct {
		name string
		url  string
		ok   bool
	}{
		{"gitee https 放行", "https://gitee.com/princess-zp/sheng-shou-yun-he/releases/download/v1.7.1/a.zip", true},
		{"github https 放行", "https://github.com/cherish-zp/sheng-shou-yun-he/releases/download/v1.7.1/a.zip", true},
		{"objects 附件域放行", "https://objects.githubusercontent.com/gh-attachments/xxx/a.zip", true},
		{"foruda 附件域放行", "https://foruda.gitee.com/xxx/a.zip", true},
		{"域名大小写不敏感", "https://GITEE.COM/x/a.zip", true},
		{"gitee 但 http 拒绝", "http://gitee.com/x/a.zip", false},
		{"附件域但 http 拒绝", "http://objects.githubusercontent.com/x/a.zip", false},
		{"非白名单域拒绝", "https://evil.example.com/a.zip", false},
		{"后缀伪造域拒绝", "https://gitee.com.evil.example.com/a.zip", false},
		{"file scheme 拒绝", "file:///etc/passwd", false},
		{"空 URL 拒绝", "", false},
		{"裸字符串拒绝", "not a url", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validateUpdateDownloadURL(c.url)
			if c.ok {
				if err != nil {
					t.Fatalf("validateUpdateDownloadURL(%q) = %v, want nil", c.url, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), "更新下载地址不在允许的域名内") {
				t.Fatalf("validateUpdateDownloadURL(%q) = %v, want domain-whitelist error", c.url, err)
			}
		})
	}
}

// TestDownloadUpdateRejectsNonWhitelistedURL 验证 DownloadUpdate 在发起任何
// 网络请求前就拒绝白名单外的入口 URL,并把进度置 error。
func TestDownloadUpdateRejectsNonWhitelistedURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("URL 校验必须在发起 HTTP 请求之前")
	}))
	defer srv.Close()
	app := &App{httpClient: srv.Client()} // 不注入旁路,走生产校验
	err := app.DownloadUpdate(DownloadUpdateRequest{URL: "http://evil.example.com/u.zip"})
	if err == nil || !strings.Contains(err.Error(), "更新下载地址不在允许的域名内") {
		t.Fatalf("expected whitelist rejection, got %v", err)
	}
	if prog := app.UpdateProgress(); prog.Phase != "error" || !strings.Contains(prog.Error, "更新下载地址不在允许的域名内") {
		t.Fatalf("progress must carry the whitelist error, got %+v", prog)
	}
}

// TestOpenURLSchemeWhitelist 验证 OpenURL 仅放行 http/https,其余 scheme
// 一律报错;合法 scheme 在 ctx 缺失时报的是「应用尚未初始化」而非协议拒绝。
func TestOpenURLSchemeWhitelist(t *testing.T) {
	app := &App{}
	for _, u := range []string{
		"file:///etc/passwd",
		"javascript:alert(1)",
		"ftp://example.com/x",
		"mailto:a@b.com",
		"/relative/path",
		"not a url",
		"",
	} {
		if err := app.OpenURL(u); err == nil {
			t.Fatalf("OpenURL(%q) must be rejected", u)
		}
	}
	for _, u := range []string{"https://gitee.com/x", "http://gitee.com/x"} {
		err := app.OpenURL(u)
		if err == nil {
			t.Fatalf("OpenURL(%q) without ctx must fail on ctx, got nil", u)
		}
		if strings.Contains(err.Error(), "协议") {
			t.Fatalf("OpenURL(%q) must pass the scheme check: %v", u, err)
		}
	}
}

func TestCheckUpdateFindsNewerVersion(t *testing.T) {
	srv := fakeGiteeServer(t, `{
	  "tag_name": "v1.1.0",
	  "body": "修复若干问题",
	  "assets": [
	    {"name": "圣手运河-v1.1.0-darwin-arm64.zip", "browser_download_url": "http://x/mac-arm.zip"},
	    {"name": "圣手运河-v1.1.0-darwin-amd64.zip", "browser_download_url": "http://x/mac-intel.zip"},
	    {"name": "圣手运河-v1.1.0-windows-amd64.zip", "browser_download_url": "http://x/win.zip"}
	  ]
	}`)
	app := newUpdateApp(t, srv)
	setRuntime(t, "darwin", "arm64")
	res, err := app.CheckUpdate(CheckUpdateRequest{CurrentVersion: "v1.0.0"})
	if err != nil {
		t.Fatalf("CheckUpdate: %v", err)
	}
	if !res.HasUpdate || res.LatestVersion != "v1.1.0" {
		t.Fatalf("expected update to v1.1.0, got %+v", res)
	}
	if res.Notes == "" {
		t.Fatal("notes must be carried through")
	}
	if !strings.HasSuffix(res.DownloadURL, "mac-arm.zip") {
		t.Fatalf("expected darwin-arm64 asset, got %q", res.DownloadURL)
	}
}

// TestCheckUpdatePicksAssetByArch 验证 Intel mac 命中 amd64 资产。
func TestCheckUpdatePicksAssetByArch(t *testing.T) {
	srv := fakeGiteeServer(t, `{
	  "tag_name": "v1.1.0",
	  "assets": [
	    {"name": "圣手运河-v1.1.0-darwin-arm64.zip", "browser_download_url": "http://x/mac-arm.zip"},
	    {"name": "圣手运河-v1.1.0-darwin-amd64.zip", "browser_download_url": "http://x/mac-intel.zip"}
	  ]
	}`)
	app := newUpdateApp(t, srv)
	setRuntime(t, "darwin", "amd64")
	res, err := app.CheckUpdate(CheckUpdateRequest{CurrentVersion: "v1.0.0"})
	if err != nil {
		t.Fatalf("CheckUpdate: %v", err)
	}
	if !strings.HasSuffix(res.DownloadURL, "mac-intel.zip") {
		t.Fatalf("expected darwin-amd64 asset, got %q", res.DownloadURL)
	}
}

// setRuntime 覆盖平台探测变量,测试结束自动还原。
func setRuntime(t *testing.T, goos, goarch string) {
	t.Helper()
	oldGOOS, oldGOARCH := runtimeGOOS, runtimeGOARCH
	runtimeGOOS, runtimeGOARCH = goos, goarch
	t.Cleanup(func() { runtimeGOOS, runtimeGOARCH = oldGOOS, oldGOARCH })
}

func TestCheckUpdateReportsUpToDate(t *testing.T) {
	srv := fakeGiteeServer(t, `{"tag_name":"v1.0.0","body":"","assets":[{"name":"a-macOS.zip","browser_download_url":"u"}]}`)
	res, err := newUpdateApp(t, srv).CheckUpdate(CheckUpdateRequest{CurrentVersion: "v1.0.0"})
	if err != nil {
		t.Fatalf("CheckUpdate: %v", err)
	}
	if res.HasUpdate {
		t.Fatalf("equal version must not flag update: %+v", res)
	}
}

func TestCheckUpdateIgnoresNonSemverTag(t *testing.T) {
	srv := fakeGiteeServer(t, `{"tag_name":"v1.1.0-rc.1","body":"","assets":[{"name":"a-macOS.zip","browser_download_url":"u"}]}`)
	res, err := newUpdateApp(t, srv).CheckUpdate(CheckUpdateRequest{CurrentVersion: "v1.0.0"})
	if err != nil {
		t.Fatalf("CheckUpdate: %v", err)
	}
	if res.HasUpdate {
		t.Fatalf("pre-release tag must be ignored: %+v", res)
	}
}

func TestPickAssetURL(t *testing.T) {
	rel := giteeRelease{Assets: []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
	}{
		{Name: "圣手运河-v1.6.0-darwin-amd64.zip", BrowserDownloadURL: "intel"},
		{Name: "圣手运河-v1.6.0-darwin-arm64.zip", BrowserDownloadURL: "arm"},
		{Name: "圣手运河-v1.6.0-windows-amd64.zip", BrowserDownloadURL: "win"},
		{Name: "v1.6.0.zip", BrowserDownloadURL: "source"},
	}}
	if got := pickAssetURL(rel, "darwin", "arm64"); got != "arm" {
		t.Fatalf("darwin/arm64 asset = %q, want arm", got)
	}
	if got := pickAssetURL(rel, "darwin", "amd64"); got != "intel" {
		t.Fatalf("darwin/amd64 asset = %q, want intel", got)
	}
	if got := pickAssetURL(rel, "windows", "amd64"); got != "win" {
		t.Fatalf("windows/amd64 asset = %q, want win", got)
	}
	// 上游只有单一 darwin 包时按系统名兜底,源码包永不被选中。
	single := giteeRelease{Assets: rel.Assets[1:2]}
	if got := pickAssetURL(single, "darwin", "amd64"); got != "arm" {
		t.Fatalf("darwin amd64 must fall back to the only darwin asset, got %q", got)
	}
	if got := pickAssetURL(giteeRelease{}, "darwin", "arm64"); got != "" {
		t.Fatalf("missing asset must yield empty, got %q", got)
	}
	sourceOnly := giteeRelease{Assets: []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
	}{{Name: "v1.6.0.zip", BrowserDownloadURL: "source"}}}
	if got := pickAssetURL(sourceOnly, "darwin", "arm64"); got != "" {
		t.Fatalf("source archive must never be picked, got %q", got)
	}
}

// makeUpdateZip writes a zip containing a minimal 圣手运河.app tree.
func makeUpdateZip(t *testing.T, path string) {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	add := func(name, content string) {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		io.WriteString(w, content)
	}
	add("圣手运河.app/Contents/", "")
	add("圣手运河.app/Contents/MacOS/圣手运河", "#!/bin/sh\n")
	add("圣手运河.app/Contents/Info.plist", "<plist/>")
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDownloadUpdateUnzipsApp(t *testing.T) {
	zipPath := filepath.Join(t.TempDir(), "u.zip")
	makeUpdateZip(t, zipPath)
	data, err := os.ReadFile(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(data)
	}))
	defer srv.Close()
	app := newUpdateApp(t, srv)

	if err := app.DownloadUpdate(DownloadUpdateRequest{URL: srv.URL + "/u.zip"}); err != nil {
		t.Fatalf("DownloadUpdate: %v", err)
	}
	staging := app.stagingDir.Load().(string)
	if !strings.HasPrefix(filepath.Base(staging), "sheng-shou-yun-he-update-") {
		t.Fatalf("staging dir must use the sheng-shou-yun-he-update- prefix, got %q", staging)
	}
	bin := filepath.Join(staging, "圣手运河.app", "Contents", "MacOS", "圣手运河")
	if _, err := os.Stat(bin); err != nil {
		t.Fatalf("expected extracted binary, got %v", err)
	}
	prog := app.UpdateProgress()
	if prog.Phase != "done" || prog.Percent != 100 {
		t.Fatalf("unexpected progress %+v", prog)
	}
}

func TestDownloadUpdateHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()
	app := newUpdateApp(t, srv)
	if err := app.DownloadUpdate(DownloadUpdateRequest{URL: srv.URL + "/x"}); err == nil {
		t.Fatal("expected download error")
	}
	if app.UpdateProgress().Phase != "error" {
		t.Fatalf("progress must be error, got %+v", app.UpdateProgress())
	}
}

// makeWindowsUpdateZip writes a zip containing only the payload exe
// (与发布流水线的 windows 包形状一致)。
func makeWindowsUpdateZip(t *testing.T, path string) {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("圣手运河.exe")
	if err != nil {
		t.Fatal(err)
	}
	io.WriteString(w, "MZ fake exe")
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestDownloadUpdateWindowsStagesExe 验证 Windows 下载后把解压出的 exe 放进
// 暂存目录(不再丢到「下载」文件夹等手动安装)。
func TestDownloadUpdateWindowsStagesExe(t *testing.T) {
	setRuntime(t, "windows", "amd64")
	zipPath := filepath.Join(t.TempDir(), "u.zip")
	makeWindowsUpdateZip(t, zipPath)
	data, err := os.ReadFile(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(data)
	}))
	defer srv.Close()
	app := newUpdateApp(t, srv)

	if err := app.DownloadUpdate(DownloadUpdateRequest{URL: srv.URL + "/u.zip"}); err != nil {
		t.Fatalf("DownloadUpdate: %v", err)
	}
	staging := app.stagingDir.Load().(string)
	if _, err := os.Stat(filepath.Join(staging, "圣手运河.exe")); err != nil {
		t.Fatalf("expected staged exe, got %v", err)
	}
	if app.UpdateProgress().Phase != "done" {
		t.Fatalf("unexpected progress %+v", app.UpdateProgress())
	}
}

func TestApplyUpdateRequiresStaging(t *testing.T) {
	app := newUpdateApp(t, httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})))
	if err := app.ApplyUpdate(ApplyUpdateRequest{}); err == nil {
		t.Fatal("expected error without staged update")
	}
}

// TestBundleFromExe 验证从可执行文件路径反查 .app 包目录。
func TestBundleFromExe(t *testing.T) {
	bundle, err := bundleFromExe("/Applications/圣手运河.app/Contents/MacOS/圣手运河")
	if err != nil || bundle != "/Applications/圣手运河.app" {
		t.Fatalf("bundleFromExe = %q, %v", bundle, err)
	}
	if _, err := bundleFromExe("/opt/homebrew/bin/圣手运河"); err == nil {
		t.Fatal("non-bundle executable must fail")
	}
}

// TestBuildUpdaterScriptSwapsInPlace 验证 macOS 脚本原位替换:旧包改名备份,
// 新包 ditto 到运行位置,启动失败自动还原;不再写死 /Applications。
func TestBuildUpdaterScriptSwapsInPlace(t *testing.T) {
	s := buildUpdaterScript("/tmp/stage", "/Applications/圣手运河.app")
	for _, want := range []string{
		`SRC="/tmp/stage/圣手运河.app"`,
		`TARGET="/Applications/圣手运河.app"`,
		`mv "$TARGET" "$BACKUP"`,
		`ditto "$SRC" "$TARGET"`,
		`open "$TARGET"`,
		`pgrep -x 圣手运河`,
		`mv "$BACKUP" "$TARGET"`,
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("script missing %q:\n%s", want, s)
		}
	}
}

// TestBuildWindowsUpdaterScript 验证 Windows 脚本:等待退出 → 旧 exe 原地
// 改名(运行中的 exe 允许改名)→ 复制新 exe → 启动 → 清理备份,复制失败还原。
// 脚本含中文 exe 名,必须在 @echo off 后先切 UTF-8 代码页。
func TestBuildWindowsUpdaterScript(t *testing.T) {
	s := buildWindowsUpdaterScript(`C:\stage`, `C:\Apps\圣手运河.exe`)
	for _, want := range []string{
		`@echo off`,
		`chcp 65001 >nul`,
		`move /Y "C:\Apps\圣手运河.exe" "C:\Apps\圣手运河.exe.old"`,
		`copy /Y "C:\stage\圣手运河.exe" "C:\Apps\圣手运河.exe"`,
		`start "" "C:\Apps\圣手运河.exe"`,
		`del "C:\Apps\圣手运河.exe.old"`,
		`move /Y "C:\Apps\圣手运河.exe.old" "C:\Apps\圣手运河.exe"`,
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("script missing %q:\n%s", want, s)
		}
	}
	if !strings.Contains(s, "@echo off\r\nchcp 65001 >nul") {
		t.Fatalf("chcp 65001 must come right after @echo off:\n%s", s)
	}
}

// TestApplyUpdateSpawnsScriptWhenStaged 验证 macOS 用运行位置生成替换脚本
// 并以分离进程启动。
func TestApplyUpdateSpawnsScriptWhenStaged(t *testing.T) {
	staging := t.TempDir()
	appDir := filepath.Join(staging, "圣手运河.app")
	os.MkdirAll(filepath.Join(appDir, "Contents", "MacOS"), 0o755)
	os.WriteFile(filepath.Join(appDir, "Contents", "MacOS", "圣手运河"), []byte("x"), 0o755)
	os.WriteFile(filepath.Join(appDir, "Contents", "Info.plist"), []byte("<x/>"), 0o644)
	setRuntime(t, "darwin", "arm64")
	oldExePath := exePath
	exePath = func() (string, error) {
		return "/Applications/圣手运河.app/Contents/MacOS/圣手运河", nil
	}
	t.Cleanup(func() { exePath = oldExePath })

	var started []string
	app := newUpdateApp(t, httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})))
	app.applyCmd = func(cmd *exec.Cmd) error {
		started = append(started, strings.Join(cmd.Args, " "))
		return nil
	}
	app.stagingDir.Store(staging)
	if err := app.ApplyUpdate(ApplyUpdateRequest{}); err != nil {
		t.Fatalf("ApplyUpdate: %v", err)
	}
	if len(started) != 1 || !strings.Contains(started[0], "apply-update.sh") {
		t.Fatalf("expected detached script start, got %v", started)
	}
	data, err := os.ReadFile(filepath.Join(staging, "apply-update.sh"))
	if err != nil {
		t.Fatalf("script not written: %v", err)
	}
	if !strings.Contains(string(data), `TARGET="/Applications/圣手运河.app"`) {
		t.Fatalf("script must target the running install location:\n%s", data)
	}
}

// TestApplyUpdateWindowsRunsBat 验证 Windows 分支:定位暂存 exe,生成 bat,
// 经 cmd /c 分离启动。
func TestApplyUpdateWindowsRunsBat(t *testing.T) {
	setRuntime(t, "windows", "amd64")
	staging := t.TempDir()
	os.WriteFile(filepath.Join(staging, "圣手运河.exe"), []byte("MZ"), 0o755)
	oldExePath := exePath
	exePath = func() (string, error) { return `C:\Apps\圣手运河.exe`, nil }
	t.Cleanup(func() { exePath = oldExePath })

	var started []string
	app := newUpdateApp(t, httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})))
	app.applyCmd = func(cmd *exec.Cmd) error {
		started = append(started, strings.Join(cmd.Args, " "))
		return nil
	}
	app.stagingDir.Store(staging)
	if err := app.ApplyUpdate(ApplyUpdateRequest{}); err != nil {
		t.Fatalf("ApplyUpdate: %v", err)
	}
	if len(started) != 1 || !strings.Contains(started[0], "apply-update.bat") {
		t.Fatalf("expected cmd /c bat start, got %v", started)
	}
	data, err := os.ReadFile(filepath.Join(staging, "apply-update.bat"))
	if err != nil {
		t.Fatalf("script not written: %v", err)
	}
	if !strings.Contains(string(data), `copy /Y `) {
		t.Fatalf("unexpected bat content:\n%s", data)
	}
}

// --- 1. pickAssetURL 加固:安装器资产绝不被自动更新选中 ---

// TestPickAssetURLSkipsInstaller 验证同一 release 同时提供 zip 与
// *-installer.exe 时必须选 zip;只有 installer 时返回空;tar.gz 同样可选。
func TestPickAssetURLSkipsInstaller(t *testing.T) {
	both := giteeRelease{Assets: []releaseAsset{
		mkAsset("ShengShouYunHe-v1.7.1-windows-amd64-installer.exe", "http://x/win-installer.exe"),
		mkAsset("ShengShouYunHe-v1.7.1-windows-amd64.zip", "http://x/win.zip"),
	}}
	if got := pickAssetURL(both, "windows", "amd64"); got != "http://x/win.zip" {
		t.Fatalf("must pick the zip over the installer, got %q", got)
	}
	installerOnly := giteeRelease{Assets: []releaseAsset{
		mkAsset("ShengShouYunHe-v1.7.1-windows-amd64-installer.exe", "http://x/win-installer.exe"),
	}}
	if got := pickAssetURL(installerOnly, "windows", "amd64"); got != "" {
		t.Fatalf("installer must never be auto-picked, got %q", got)
	}
	tgz := giteeRelease{Assets: []releaseAsset{
		mkAsset("圣手运河-v1.7.1-linux-amd64.tar.gz", "http://x/linux.tar.gz"),
	}}
	if got := pickAssetURL(tgz, "linux", "amd64"); got != "http://x/linux.tar.gz" {
		t.Fatalf("tar.gz must be selectable, got %q", got)
	}
}

// --- 2. 双源回退:Gitee 挂了回退 GitHub,两边都挂才报错 ---

// ghLatestPath 是 GitHub 回退源的 releases/latest 请求路径。
const ghLatestPath = "/repos/cherish-zp/sheng-shou-yun-he/releases/latest"

// newDualSourceApp 构造注入了 Gitee(主)与 GitHub(回退)两个源的 App。
func newDualSourceApp(t *testing.T, gitee, gh *httptest.Server) *App {
	t.Helper()
	a := &App{}
	a.baseURL.Store(gitee.URL)
	a.fallbackURL.Store(gh.URL)
	a.httpClient = gh.Client()
	a.applyCmd = func(*exec.Cmd) error { return nil }
	return a
}

func TestCheckUpdateFallsBackToGitHub(t *testing.T) {
	gitee := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "gitee down", http.StatusBadGateway)
	}))
	defer gitee.Close()
	var ghPaths []string
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ghPaths = append(ghPaths, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{
		  "tag_name": "v1.2.0",
		  "body": "GitHub 镜像发布",
		  "assets": [
		    {"name": "ShengShouYunHe-v1.2.0-darwin-arm64.zip", "browser_download_url": "http://gh/mac.zip"}
		  ]
		}`)
	}))
	defer gh.Close()

	setRuntime(t, "darwin", "arm64")
	res, err := newDualSourceApp(t, gitee, gh).CheckUpdate(CheckUpdateRequest{CurrentVersion: "v1.0.0"})
	if err != nil {
		t.Fatalf("CheckUpdate: %v", err)
	}
	if !res.HasUpdate || res.LatestVersion != "v1.2.0" {
		t.Fatalf("expected fallback update to v1.2.0, got %+v", res)
	}
	if len(ghPaths) != 1 || ghPaths[0] != ghLatestPath {
		t.Fatalf("fallback must hit GitHub latest path %s, got %v", ghLatestPath, ghPaths)
	}
	if !strings.HasSuffix(res.DownloadURL, "mac.zip") {
		t.Fatalf("expected darwin-arm64 asset from GitHub, got %q", res.DownloadURL)
	}
}

// TestCheckUpdateFallsBackOnBadGiteePayload 验证 Gitee 返回 200 但内容不可用
// (非法 JSON / 缺 tag_name / assets 为空)时同样回退 GitHub。
func TestCheckUpdateFallsBackOnBadGiteePayload(t *testing.T) {
	cases := []struct {
		name      string
		giteeBody string
	}{
		{"非法JSON", `not-json`},
		{"缺tag_name", `{"assets":[{"name":"a.zip","browser_download_url":"u"}]}`},
		{"assets为空", `{"tag_name":"v9.0.0","assets":[]}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gitee := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				io.WriteString(w, c.giteeBody)
			}))
			defer gitee.Close()
			gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				io.WriteString(w, `{"tag_name":"v1.3.0","assets":[{"name":"ShengShouYunHe-v1.3.0-darwin-arm64.zip","browser_download_url":"http://gh/mac.zip"}]}`)
			}))
			defer gh.Close()

			setRuntime(t, "darwin", "arm64")
			res, err := newDualSourceApp(t, gitee, gh).CheckUpdate(CheckUpdateRequest{CurrentVersion: "v1.0.0"})
			if err != nil {
				t.Fatalf("CheckUpdate: %v", err)
			}
			if !res.HasUpdate || res.LatestVersion != "v1.3.0" {
				t.Fatalf("expected fallback update to v1.3.0, got %+v", res)
			}
		})
	}
}

func TestCheckUpdateBothSourcesFail(t *testing.T) {
	down := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down", http.StatusServiceUnavailable)
	})
	gitee := httptest.NewServer(down)
	defer gitee.Close()
	gh := httptest.NewServer(down)
	defer gh.Close()

	_, err := newDualSourceApp(t, gitee, gh).CheckUpdate(CheckUpdateRequest{CurrentVersion: "v1.0.0"})
	if err == nil {
		t.Fatal("both sources down must yield an error")
	}
}

// --- 3. SHA256 校验链路 ---

// TestCheckUpdateFetchesPlatformSHA256 验证 has_update 时从 release 的
// sha256sums.txt 资产解析本平台哈希填入结果。
func TestCheckUpdateFetchesPlatformSHA256(t *testing.T) {
	const wantHash = "5cf1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0c1d2e3f4a5b6c7d8e9f0a1"
	var srvURL string
	mux := http.NewServeMux()
	mux.HandleFunc(fmt.Sprintf("/repos/%s/%s/releases/latest", updateOwner, updateRepo), func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, fmt.Sprintf(`{
		  "tag_name": "v1.7.1",
		  "body": "新版本",
		  "assets": [
		    {"name": "圣手运河-v1.7.1-darwin-arm64.zip", "browser_download_url": "%s/mac.zip"},
		    {"name": "sha256sums.txt", "browser_download_url": "%s/SHA256SUMS.txt"}
		  ]
		}`, srvURL, srvURL))
	})
	mux.HandleFunc("/SHA256SUMS.txt", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "linux-amd64: aaaa\n")
		io.WriteString(w, "darwin-arm64: "+wantHash+"\n")
	})
	srv := httptest.NewServer(mux)
	srvURL = srv.URL
	defer srv.Close()

	setRuntime(t, "darwin", "arm64")
	res, err := newUpdateApp(t, srv).CheckUpdate(CheckUpdateRequest{CurrentVersion: "v1.0.0"})
	if err != nil {
		t.Fatalf("CheckUpdate: %v", err)
	}
	if res.SHA256 != wantHash {
		t.Fatalf("expected platform hash %q, got %q", wantHash, res.SHA256)
	}
}

// TestCheckUpdateSHA256Optional 验证优雅降级:旧版本 Release 无 sha256sums
// 资产、或文件里没有本平台行时,SHA256 留空且不影响更新探测。
func TestCheckUpdateSHA256Optional(t *testing.T) {
	newSrv := func(sumsBody string) *httptest.Server {
		var srvURL string
		mux := http.NewServeMux()
		mux.HandleFunc(fmt.Sprintf("/repos/%s/%s/releases/latest", updateOwner, updateRepo), func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, fmt.Sprintf(`{
			  "tag_name": "v1.7.1",
			  "assets": [
			    {"name": "圣手运河-v1.7.1-darwin-arm64.zip", "browser_download_url": "%s/mac.zip"},
			    {"name": "SHA256SUMS.txt", "browser_download_url": "%s/SHA256SUMS.txt"}
			  ]
			}`, srvURL, srvURL))
		})
		mux.HandleFunc("/SHA256SUMS.txt", func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, sumsBody)
		})
		srv := httptest.NewServer(mux)
		srvURL = srv.URL
		t.Cleanup(srv.Close)
		return srv
	}

	setRuntime(t, "darwin", "arm64")
	cases := []struct {
		name string
		body string
	}{
		{"无本平台行", "linux-amd64: aaaa\n"},
		{"哈希长度不对", "darwin-arm64: deadbeef\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res, err := newUpdateApp(t, newSrv(c.body)).CheckUpdate(CheckUpdateRequest{CurrentVersion: "v1.0.0"})
			if err != nil {
				t.Fatalf("CheckUpdate: %v", err)
			}
			if !res.HasUpdate || res.SHA256 != "" {
				t.Fatalf("SHA256 must degrade to empty, got %+v", res)
			}
		})
	}

	// 旧版本 Release 完全没有 sha256sums 资产:不得发起额外请求。
	srv := fakeGiteeServer(t, `{"tag_name":"v1.7.1","assets":[{"name":"圣手运河-v1.7.1-darwin-arm64.zip","browser_download_url":"u"}]}`)
	res, err := newUpdateApp(t, srv).CheckUpdate(CheckUpdateRequest{CurrentVersion: "v1.0.0"})
	if err != nil {
		t.Fatalf("CheckUpdate: %v", err)
	}
	if !res.HasUpdate || res.SHA256 != "" {
		t.Fatalf("missing sums asset must degrade to empty, got %+v", res)
	}
}

// TestDownloadUpdateSHA256 覆盖下载校验:匹配成功(大小写不敏感)、不匹配
// 报错(phase=error 且含「校验失败」)、为空跳过。
func TestDownloadUpdateSHA256(t *testing.T) {
	zipPath := filepath.Join(t.TempDir(), "u.zip")
	makeUpdateZip(t, zipPath)
	data, err := os.ReadFile(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(data)
	}))
	defer srv.Close()

	sum := sha256.Sum256(data)
	good := hex.EncodeToString(sum[:])

	// 匹配成功:大写十六进制也必须通过。
	app := newUpdateApp(t, srv)
	if err := app.DownloadUpdate(DownloadUpdateRequest{URL: srv.URL + "/u.zip", SHA256: strings.ToUpper(good)}); err != nil {
		t.Fatalf("matching sha256 must pass: %v", err)
	}
	if app.UpdateProgress().Phase != "done" {
		t.Fatalf("unexpected progress %+v", app.UpdateProgress())
	}

	// 不匹配:返回 error,进度置 error 且错误信息含「校验失败」。
	bad := newUpdateApp(t, srv)
	err = bad.DownloadUpdate(DownloadUpdateRequest{URL: srv.URL + "/u.zip", SHA256: strings.Repeat("ab", 32)})
	if err == nil {
		t.Fatal("mismatched sha256 must fail")
	}
	prog := bad.UpdateProgress()
	if prog.Phase != "error" || !strings.Contains(prog.Error, "校验失败") {
		t.Fatalf("expected error phase with 校验失败, got %+v", prog)
	}

	// 为空:重查两源都拿不到 sums(该服务器对 releases/latest 返回 zip 字节,
	// 解析必失败;回退源恒 503)→ 放行,兼容旧版本 release 的真实场景。
	skip := newUpdateApp(t, srv)
	if err := skip.DownloadUpdate(DownloadUpdateRequest{URL: srv.URL + "/u.zip", SHA256: ""}); err != nil {
		t.Fatalf("empty sha256 with no sums must skip verification: %v", err)
	}
	if skip.UpdateProgress().Phase != "done" {
		t.Fatalf("unexpected progress %+v", skip.UpdateProgress())
	}
}

// newSummedUpdateSource 构造一个完整更新源:releases/latest 指向资产与
// SHA256SUMS.txt,资产本体可下载;sumsBody 是 sums 文件内容。
func newSummedUpdateSource(t *testing.T, assetPath string, assetData []byte, sumsBody string) *httptest.Server {
	t.Helper()
	var srvURL string
	mux := http.NewServeMux()
	mux.HandleFunc(fmt.Sprintf("/repos/%s/%s/releases/latest", updateOwner, updateRepo), func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, fmt.Sprintf(`{
		  "tag_name": "v1.7.1",
		  "assets": [
		    {"name": "圣手运河-v1.7.1-darwin-arm64.zip", "browser_download_url": "%s%s"},
		    {"name": "sha256sums.txt", "browser_download_url": "%s/SHA256SUMS.txt"}
		  ]
		}`, srvURL, assetPath, srvURL))
	})
	mux.HandleFunc("/SHA256SUMS.txt", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, sumsBody)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write(assetData)
	})
	srv := httptest.NewServer(mux)
	srvURL = srv.URL
	t.Cleanup(srv.Close)
	return srv
}

// TestDownloadUpdateRequeriesSHA256WhenMissing 验证 SHA256 强制化:前端没传
// sha(篡改/旧流程场景)而后端重查 release 拿得到期望哈希时,必须强制校验。
// URL 精确对上资产时用资产平台的哈希;对不上退回当前 goos/goarch。
func TestDownloadUpdateRequeriesSHA256WhenMissing(t *testing.T) {
	setRuntime(t, "darwin", "arm64")
	zipPath := filepath.Join(t.TempDir(), "u.zip")
	makeUpdateZip(t, zipPath)
	data, err := os.ReadFile(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	goodHash := hex.EncodeToString(sum[:])
	wrongHash := strings.Repeat("ab", 32)

	// URL 对上资产 + sums 哈希正确:空 sha 重查后校验通过。
	srv := newSummedUpdateSource(t, "/u.zip", data, "linux-amd64: "+wrongHash+"\ndarwin-arm64: "+goodHash+"\n")
	if err := newUpdateApp(t, srv).DownloadUpdate(DownloadUpdateRequest{URL: srv.URL + "/u.zip"}); err != nil {
		t.Fatalf("re-queried matching hash must pass: %v", err)
	}

	// URL 对上资产 + sums 哈希错误:空 sha 也必须报校验失败。
	srvBad := newSummedUpdateSource(t, "/u.zip", data, "darwin-arm64: "+wrongHash+"\n")
	bad := newUpdateApp(t, srvBad)
	err = bad.DownloadUpdate(DownloadUpdateRequest{URL: srvBad.URL + "/u.zip"})
	if err == nil || !strings.Contains(err.Error(), "SHA256 校验失败") {
		t.Fatalf("re-queried mismatched hash must fail, got %v", err)
	}
	if prog := bad.UpdateProgress(); prog.Phase != "error" || !strings.Contains(prog.Error, "校验失败") {
		t.Fatalf("expected error phase with 校验失败, got %+v", prog)
	}

	// URL 对不上资产(镜像路径):退回当前平台 darwin-arm64 的哈希校验。
	srvMirror := newSummedUpdateSource(t, "/u.zip", data, "linux-amd64: "+wrongHash+"\ndarwin-arm64: "+goodHash+"\n")
	if err := newUpdateApp(t, srvMirror).DownloadUpdate(DownloadUpdateRequest{URL: srvMirror.URL + "/mirror.zip"}); err != nil {
		t.Fatalf("platform-fallback hash must apply for unmatched URL: %v", err)
	}
}

// makeUpdateTarGz 生成含单个条目的 tar.gz(entryName 可带 ./ 等目录前缀,
// 模拟 CI 里 tar -C 打包的产物形态)。
func makeUpdateTarGz(t *testing.T, path, entryName, content string) {
	t.Helper()
	makeTarGzEntries(t, path, []tarEntry{{name: entryName, content: content}})
}

// tarEntry 描述 tar.gz 内的一个待打包条目;typeflag 零值为普通文件。
type tarEntry struct {
	name     string
	typeflag byte
	linkname string
	content  string
}

// makeTarGzEntries 打包多条目 tar.gz,供安全解包用例构造攻击形态。
func makeTarGzEntries(t *testing.T, path string, entries []tarEntry) {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	for _, e := range entries {
		hdr := &tar.Header{
			Name:     e.name,
			Mode:     0o755,
			Size:     int64(len(e.content)),
			Typeflag: tar.TypeReg,
		}
		if e.typeflag != 0 {
			hdr.Typeflag = e.typeflag
		}
		if e.linkname != "" {
			hdr.Linkname = e.linkname
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(e.content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestExtractTarGzPayloadRejectsUnsafeEntries 覆盖安全解包:正常包通过并把
// 二进制解到暂存目录;含 ../ 段、符号链接、硬链接、绝对路径条目的包一律拒绝。
func TestExtractTarGzPayloadRejectsUnsafeEntries(t *testing.T) {
	okPath := filepath.Join(t.TempDir(), "ok.tar.gz")
	makeTarGzEntries(t, okPath, []tarEntry{
		{name: "./README.md", content: "hi"},
		{name: "./圣手运河", content: "binary-bytes"},
	})
	dest := t.TempDir()
	bin, err := extractTarGzPayload(okPath, dest)
	if err != nil {
		t.Fatalf("normal package must pass: %v", err)
	}
	if want := filepath.Join(dest, "圣手运河"); bin != want {
		t.Fatalf("extracted path = %q, want %q", bin, want)
	}
	got, err := os.ReadFile(bin)
	if err != nil || string(got) != "binary-bytes" {
		t.Fatalf("extracted content = %q, %v", got, err)
	}
	if fi, err := os.Stat(bin); err != nil || fi.Mode()&0o111 == 0 {
		t.Fatalf("extracted binary must stay executable: %v, %v", fi, err)
	}

	attackCases := []struct {
		name    string
		entries []tarEntry
	}{
		{"父目录穿越", []tarEntry{
			{name: "./圣手运河", content: "x"},
			{name: "../evil.sh", content: "x"},
		}},
		{"嵌套上跳", []tarEntry{
			{name: "a/../../evil.sh", content: "x"},
		}},
		{"符号链接条目", []tarEntry{
			{name: "./圣手运河", content: "x"},
			{name: "link", typeflag: tar.TypeSymlink, linkname: "/etc/passwd"},
		}},
		{"硬链接条目", []tarEntry{
			{name: "./圣手运河", content: "x"},
			{name: "hard", typeflag: tar.TypeLink, linkname: "圣手运河"},
		}},
		{"绝对路径条目", []tarEntry{
			{name: "/etc/evil", content: "x"},
			{name: "./圣手运河", content: "x"},
		}},
	}
	for _, c := range attackCases {
		t.Run(c.name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "x.tar.gz")
			makeTarGzEntries(t, p, c.entries)
			dest := t.TempDir()
			if _, err := extractTarGzPayload(p, dest); err == nil {
				t.Fatal("unsafe entry must be rejected")
			}
		})
	}
}

// TestDownloadUpdateTarGz 验证 Linux 资产(.tar.gz)的下载闭环:落盘为暂存
// 目录的 update.tar.gz,并在暂存目录安全解包出 圣手运河 二进制(apply 脚本
// 直接 mv 该文件,不再 tar 解包),SHA256 校验同样生效,缺二进制时报错。
func TestDownloadUpdateTarGz(t *testing.T) {
	tgzPath := filepath.Join(t.TempDir(), "u.tar.gz")
	makeUpdateTarGz(t, tgzPath, "./圣手运河", "binary-bytes")
	data, err := os.ReadFile(tgzPath)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(data)
	}))
	defer srv.Close()

	// 含二进制的 tar.gz:落盘为 update.tar.gz,校验通过,进度 done。
	app := newUpdateApp(t, srv)
	url := srv.URL + "/ShengShouYunHe-v1.7.1-linux-amd64.tar.gz"
	if err := app.DownloadUpdate(DownloadUpdateRequest{URL: url, SHA256: hex.EncodeToString(sum[:])}); err != nil {
		t.Fatalf("tar.gz download must pass: %v", err)
	}
	if app.UpdateProgress().Phase != "done" {
		t.Fatalf("unexpected progress %+v", app.UpdateProgress())
	}
	staging, _ := app.stagingDir.Load().(string)
	if _, err := os.Stat(filepath.Join(staging, "update.tar.gz")); err != nil {
		t.Fatalf("staged update.tar.gz missing: %v", err)
	}
	binData, err := os.ReadFile(filepath.Join(staging, "圣手运河"))
	if err != nil || string(binData) != "binary-bytes" {
		t.Fatalf("staged binary missing or wrong content: %q, %v", binData, err)
	}

	// 缺二进制的 tar.gz:报错且进度置 error。
	badPath := filepath.Join(t.TempDir(), "bad.tar.gz")
	makeUpdateTarGz(t, badPath, "README.md", "no binary here")
	badData, err := os.ReadFile(badPath)
	if err != nil {
		t.Fatal(err)
	}
	badSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(badData)
	}))
	defer badSrv.Close()
	bad := newUpdateApp(t, badSrv)
	if err := bad.DownloadUpdate(DownloadUpdateRequest{URL: badSrv.URL + "/x-linux-amd64.tar.gz"}); err == nil {
		t.Fatal("tar.gz without the binary must fail")
	}
	if prog := bad.UpdateProgress(); prog.Phase != "error" || !strings.Contains(prog.Error, "圣手运河") {
		t.Fatalf("expected error phase mentioning payload, got %+v", prog)
	}
}

// --- 4. Linux 原位替换 ---

// TestBuildLinuxUpdaterScript 验证 Linux 脚本:二进制已由 DownloadUpdate
// 安全解包到暂存目录,脚本只做 /proc 解析真实路径 → mv 原位替换 →
// chmod +x → 后台重启;不再 tar 解包。
func TestBuildLinuxUpdaterScript(t *testing.T) {
	s := buildLinuxUpdaterScript("/tmp/stage", "/opt/apps/圣手运河")
	for _, want := range []string{
		"/proc/self/exe",
		`SRC="/tmp/stage/圣手运河"`,
		`TARGET="/opt/apps/圣手运河"`,
		`mv -f "$SRC" "$TARGET"`,
		"chmod +x",
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("script missing %q:\n%s", want, s)
		}
	}
	for _, banned := range []string{"tar ", "mktemp", "find "} {
		if strings.Contains(s, banned) {
			t.Fatalf("script must not unpack the archive anymore (found %q):\n%s", banned, s)
		}
	}
}

// TestApplyUpdateLinuxRunsSh 验证 Linux 分支:定位暂存目录里已解包的
// 二进制,生成替换脚本并以分离进程启动。
func TestApplyUpdateLinuxRunsSh(t *testing.T) {
	setRuntime(t, "linux", "amd64")
	staging := t.TempDir()
	os.WriteFile(filepath.Join(staging, "圣手运河"), []byte("fake binary"), 0o755)
	oldExePath := exePath
	exePath = func() (string, error) { return "/opt/apps/圣手运河", nil }
	t.Cleanup(func() { exePath = oldExePath })

	var started []string
	app := newUpdateApp(t, httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})))
	app.applyCmd = func(cmd *exec.Cmd) error {
		started = append(started, strings.Join(cmd.Args, " "))
		return nil
	}
	app.stagingDir.Store(staging)
	if err := app.ApplyUpdate(ApplyUpdateRequest{}); err != nil {
		t.Fatalf("ApplyUpdate: %v", err)
	}
	if len(started) != 1 || !strings.Contains(started[0], "apply-update.sh") {
		t.Fatalf("expected detached script start, got %v", started)
	}
	data, err := os.ReadFile(filepath.Join(staging, "apply-update.sh"))
	if err != nil {
		t.Fatalf("script not written: %v", err)
	}
	if !strings.Contains(string(data), `TARGET="/opt/apps/圣手运河"`) {
		t.Fatalf("script must target the running binary:\n%s", data)
	}
	if !strings.Contains(string(data), `SRC="`+filepath.Join(staging, "圣手运河")+`"`) {
		t.Fatalf("script must mv the pre-extracted binary:\n%s", data)
	}
	if strings.Contains(string(data), "tar ") {
		t.Fatalf("script must not unpack the archive:\n%s", data)
	}
}

// --- 5. Notes 清理:剔除 body 末尾的 ```sha256sums 围栏块 ---

func TestStripSHA256Fence(t *testing.T) {
	if got := stripSHA256Fence("常规说明\n- 修复 A"); got != "常规说明\n- 修复 A" {
		t.Fatalf("body without fence must be untouched, got %q", got)
	}
	if got := stripSHA256Fence("说明\n\n```sha256sums\ndarwin-arm64: 5cf1...\n```\n"); got != "说明" {
		t.Fatalf("closed fence must be stripped, got %q", got)
	}
	if got := stripSHA256Fence("说明\n```SHA256SUMS\nabc: 00"); got != "说明" {
		t.Fatalf("unclosed fence must be stripped to the end, got %q", got)
	}
}

// TestCheckUpdateStripsSHA256Fence 验证端到端:返回给前端的 Notes 不再带
// 哈希围栏块。
func TestCheckUpdateStripsSHA256Fence(t *testing.T) {
	rel := giteeRelease{
		TagName: "v1.1.0",
		Body:    "## 更新内容\n- 修复 A\n\n```sha256sums\ndarwin-arm64: 5cf1...\nwindows-amd64: abcd...\n```\n",
		Assets:  []releaseAsset{mkAsset("圣手运河-v1.1.0-darwin-arm64.zip", "u")},
	}
	data, err := json.Marshal(rel)
	if err != nil {
		t.Fatal(err)
	}
	srv := fakeGiteeServer(t, string(data))
	res, err := newUpdateApp(t, srv).CheckUpdate(CheckUpdateRequest{CurrentVersion: "v1.0.0"})
	if err != nil {
		t.Fatalf("CheckUpdate: %v", err)
	}
	if want := "## 更新内容\n- 修复 A"; res.Notes != want {
		t.Fatalf("notes must drop the sha256sums fence, got %q, want %q", res.Notes, want)
	}
}
