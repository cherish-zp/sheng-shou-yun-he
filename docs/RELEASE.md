# 发布与自动更新

「圣手运河」的发版链路总览：**GitHub Actions 是唯一构建方**（Gitee Go 云端只有 Linux 容器，没有 macOS/Windows 构建机），推送 `v*` tag 触发四平台构建 → 产物汇总到 GitHub Release → 同步脚本自动同步到 Gitee Release（国内主分发渠道）。应用内自动更新检查主用 Gitee API `releases/latest`，失败回退 GitHub API——两平台匿名 API 各限 60 次/时/IP，这是做双源的原因。

> 更深的坑点全集见参考项目 `/Users/zhangpeng/code_bigmodel/MasterfulHands/docs/AUTO-UPDATE.md`（Tauri 版，含 13 条坑），本文只收录对本项目（Wails）适用的部分。

## 1. 产物与命名契约

四平台产物与构建环境：

| 平台 | runner | 备注 |
|---|---|---|
| darwin/arm64 | macos-14 | 原生构建 |
| darwin/amd64 | macos-14 及交叉编译 | — |
| windows/amd64 | windows-latest | NSIS 安装器为可选产物，步骤 `continue-on-error` |
| linux/amd64 | ubuntu-22.04 | 构建前 `sudo apt-get install -y libgtk-3-dev libwebkit2gtk-4.0-dev`；**必须 22.04——24.04 只有 webkit2gtk-4.1** |

GitHub 附件一律 ASCII 命名（逐字精确）：

- `ShengShouYunHe-<tag>-darwin-arm64.zip`
- `ShengShouYunHe-<tag>-darwin-amd64.zip`
- `ShengShouYunHe-<tag>-windows-amd64.zip`
- `ShengShouYunHe-<tag>-linux-amd64.tar.gz`
- `SHA256SUMS.txt`
- `ShengShouYunHe-<tag>-windows-amd64-installer.exe`（可选，NSIS 安装器）

Gitee 附件中文命名：`圣手运河-<tag>-...`（如 `圣手运河-v1.7.1-darwin-arm64.zip`），由同步脚本上传时自动改名；`SHA256SUMS.txt` 两侧同名，不改。

`SHA256SUMS.txt` 契约：

- 每行 `<os>-<arch>: <64位hex>`，共 4 个平台键：`darwin-arm64` / `darwin-amd64` / `windows-amd64` / `linux-amd64`；
- 其内容同时以 ```` ```sha256sums ```` 围栏块附在两平台 Release body 末尾（人类可读）；
- 更新引擎以 `SHA256SUMS.txt` 附件为准做下载校验，Release body 里的围栏块仅供人读；
- 安装器（`-installer.exe`）永远不会被自动更新选中。

## 2. 发版 SOP

1. **版本号三处同步并一致**：`wails.json`（`info.productVersion` 与顶层 `productVersion`）、`frontend/package.json`、`frontend/src/version.ts`——CI 有 check-versions job 校验，不一致直接红。
2. **更新 CHANGELOG 并提交**：中文 Conventional Commits，type 前缀英文（如 `feat: 新增 Topic 扩充分区 API`）。
3. **打 annotated tag 并推送**（注释即发布说明，CI 会将其提取为两平台 Release body）：
   ```bash
   git tag -a v1.7.1 -m "v1.7.1：新增 xxx，修复 xxx"
   git push origin main --tags
   ```
   CI 跑在 GitHub Actions 上：origin 的第二个 push 地址就是 GitHub，`push --tags` 双推后，需确认 tag 已到达 GitHub 侧——用 `git ls-remote github refs/tags/v1.7.1` 或网页确认。
4. **观察 CI**：四个构建 job + release job 全绿。
5. **验收**（可复制执行，替换 `<tag>`）：
   ```bash
   # Gitee Release 附件齐全且为中文名
   curl -s "https://gitee.com/api/v5/repos/princess-zp/sheng-shou-yun-he/releases/tags/<tag>" \
     | python3 -m json.tool | grep browser_download_url
   # SHA256SUMS.txt 可下载
   curl -sL "https://gitee.com/princess-zp/sheng-shou-yun-he/releases/download/<tag>/SHA256SUMS.txt"
   # HEAD 抽查一个中文附件的真实 Content-Length
   curl -sIL "https://gitee.com/princess-zp/sheng-shou-yun-he/releases/download/<tag>/圣手运河-<tag>-darwin-arm64.zip" \
     | grep -i content-length
   ```
   注意：Gitee `attach_files` API 返回的 `size` 字段显示 0 是假象，不代表附件损坏，以 HEAD 请求的 Content-Length 为准。
6. **兜底**：Gitee 同步失败 → GitHub Actions 手动重跑 `sync-gitee.yml`（workflow_dispatch 填 tag，不重新构建）→ 仍不行则本机执行：
   ```bash
   GITEE_TOKEN=<token> bash scripts/sync-gitee-release.sh <tag>
   ```
7. **Hotfix 纪律**：不要删除已发布的 tag 重打——已发布版本的更新检查/清单指向该 tag，重打会造成已下载用户错乱；要修就 bump patch 版本（如 v1.7.1 → v1.7.2）重走本流程。

## 3. 一次性前置配置

1. **`GITEE_TOKEN` Secret**：GitHub 仓库 Settings → Secrets → 添加 `GITEE_TOKEN`；token 在 gitee.com/profile/personal_access_tokens 生成，勾 `projects` 权限。
2. **双远程**：`origin` = gitee（fetch + 第 1 push 地址）+ github（第 2 push 地址），`git push origin main --tags` 一次双推。

## 4. 本项目适用踩坑清单

每条格式：坑 → 症状 → 解法。

1. **GitHub 剥离附件名非 ASCII 字符** → 上传带中文前缀的附件名被剥掉中文字符，事后 API PATCH 改名返回 200 但纹丝不动 → GitHub 侧附件名一律 ASCII，中文名只在 Gitee 侧出现（同步脚本上传时自动改名）。
2. **关窗 ≠ 退出（macOS）** → 关闭窗口进程仍在后台，「打开」唤起的是内存里的旧界面，新装的代码永远不生效 → 验证新版本前先 `pgrep -fl 圣手运河` 排查残留，`osascript -e 'quit app "圣手运河"'` 或 `pkill -x 圣手运河` 清干净再装。
3. **Gitee attach_files API 的 size 字段恒为 0** → 验收时误判附件损坏 → 是 API 展示问题而非损坏，用 `curl -sIL <附件URL>` 看 Content-Length 判断。
4. **Gitee 配额**：单附件 100MB、仓库附件总量约 1GB → 本项目产物 13~20MB 很安全，但别往 Release 塞无关大文件；发多了清理旧附件。
5. **匿名 API 限流**（Gitee 与 GitHub 各 60 次/时/IP） → 更新检查偶发 403/失败 → 应用内已做双源回退（Gitee 失败转 GitHub）；本机调试 API 时别高频刷。
6. **Gitee assets 列表默认只返回 20 条** → 同步脚本漏判已存在的附件，重跑同步产生重复附件 → 所有列附件的调用带 `per_page=100`。
7. **Linux 用户运行前置** → 装完启动报缺 webkit2gtk → 需 `sudo apt install libgtk-3-dev libwebkit2gtk-4.0-dev`，发布说明里要写；自动更新对 Linux 是替换二进制（tar.gz），不需要 sudo。
8. **版本号三处漂移** → 某处改了别处忘改，关于页显示与 tag 不一致 → CI check-versions job 拦截（参考项目曾出现 package.json 与配置文件版本不同步的漂移）。
