#!/usr/bin/env bash
# Gitee Release 同步(唯一权威脚本,CI 与本机共用)。
#
# 用法:
#   本机: GITEE_TOKEN=<私人令牌> bash scripts/sync-gitee-release.sh <tag>
#   CI  : release.yml 末步自动调用;失败后可走 sync-gitee.yml 手动重跑(不重新构建)
#
# 依赖:bash + curl + python3(另用 git ls-remote 解析 tag 对象,缺失时回退默认文案);
#       不依赖 gh/jq。令牌需要 Gitee projects 权限;不要把令牌提交到任何仓库或 CI。
#
# 流程:
#   1. GitHub API 拉取该 tag 的 Release 元数据与全部附件(assets 列表必须带
#      per_page=100——默认分页只回 20 条,附件多时会漏,踩过的坑),下载到临时目录;
#   2. 附件改名:GitHub 会剥离附件名中的非 ASCII 字符(存储层行为,事后 PATCH
#      改回也无效),故 GitHub 侧统一 ASCII 前缀 ShengShouYunHe;Gitee 支持中文,
#      上传时换回 圣手运河 前缀。SHA256SUMS.txt 两个平台同名,不改名;
#   3. 从本地改名后的文件重算 sha256,组装 Gitee Release body:
#      annotated tag 注释 + 空行 + ```sha256sums 围栏代码块(与 SHA256SUMS.txt 同内容);
#      注释经 git ls-remote + GitHub API git/tags/{sha} 提取——脚本可能跑在未
#      checkout 的 CI job 里,不能依赖本地 git 仓库有该 tag;取不到回退默认文案;
#   4. 查/建 Gitee 版本化 Release(已存在则 PATCH body 保持同步);
#   5. 幂等上传附件:同名跳过;单文件最多重试 4 次、间隔 30s——上一份大文件在
#      Gitee 侧异步处理时,紧接着的请求会被拒(darwin-arm64 最小、上传最快,
#      历史上三次发布均在此失败);全部 curl 带连接/总超时,失败有 || 兜底,
#      防 set -e 中止整个同步;
#   6. 输出跳过/上传/放弃汇总;有放弃的附件时以非零码退出(CI 侧 continue-on-error
#      保持 best-effort,手动重跑则失败得响亮)。
set -euo pipefail

TAG="${1:?用法: GITEE_TOKEN=<私人令牌> bash scripts/sync-gitee-release.sh <tag>}"
GITEE_REPO="${GITEE_REPO:-princess-zp/sheng-shou-yun-he}"
GITHUB_REPO="${GITHUB_REPO:-cherish-zp/sheng-shou-yun-he}"
: "${GITEE_TOKEN:?请设置 GITEE_TOKEN 环境变量(Gitee 私人令牌,projects 权限)}"
GITHUB_TOKEN="${GITHUB_TOKEN:-}"            # 可选:公开仓库匿名可读(匿名 API 有限流)
RETRY_INTERVAL="${SYNC_RETRY_INTERVAL:-30}" # 上传重试间隔(秒);离线测试时可调小

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

# 所有 curl 统一带连接与总超时(best-effort:GitHub 构建机到 gitee.com 可能慢/不通)。
G() { curl -sS --connect-timeout 20 --max-time 1800 "$@"; }
# -f:HTTP >= 400 非零退出,配合 || 兜底,避免把错误响应体当 JSON 解析。
GF() { G -f "$@"; }
# GitHub API GET:配置了 GITHUB_TOKEN 时带认证头(匿名有 60 次/小时限流)。
gh_api() {
  if [ -n "${GITHUB_TOKEN}" ]; then
    G -H "Authorization: Bearer ${GITHUB_TOKEN}" "$@"
  else
    G "$@"
  fi
}
# sha256 统一封装:macOS 无 sha256sum,退回 shasum -a 256(Linux 两者都常见)。
sha256_of() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  else
    shasum -a 256 "$1" | awk '{print $1}'
  fi
}
# 从 stdin JSON 取指定字段;解析失败或类型不符输出空串,永不非零退出(容错)。
json_get() {
  python3 -c "
import sys, json
try:
    d = json.load(sys.stdin) or {}
except Exception:
    d = {}
print(d.get('$1', '') if isinstance(d, dict) else '')
"
}

# ---------- 1. 拉取 GitHub Release 元数据与附件 ----------
echo "== 拉取 GitHub Release ${TAG}(${GITHUB_REPO})=="
REL_JSON="$(GF "https://api.github.com/repos/${GITHUB_REPO}/releases/tags/${TAG}")" || {
  echo "错误:GitHub 上找不到 Release ${TAG}(404)或网络/限流失败,无法同步"
  exit 1
}
REL_ID="$(printf '%s' "${REL_JSON}" | json_get id)"
[ -n "${REL_ID}" ] || { echo "错误:无法解析 GitHub Release id"; exit 1; }

# assets 必须走独立列表接口并带 per_page=100(releases/tags 内嵌的 assets 列表
# 默认只回 20 条,踩过的坑)。
ASSETS="$(GF "https://api.github.com/repos/${GITHUB_REPO}/releases/${REL_ID}/assets?per_page=100" \
  | python3 -c "
import sys, json
try:
    assets = json.load(sys.stdin) or []
except Exception:
    assets = []
for a in assets:
    print(a.get('name', '') + '\t' + a.get('browser_download_url', ''))
")" || ASSETS=""
[ -n "${ASSETS}" ] || { echo "错误:GitHub Release ${TAG} 没有附件"; exit 1; }

DOWNLOAD_DIR="${TMP}/dl"
mkdir -p "${DOWNLOAD_DIR}"
while IFS=$'\t' read -r NAME URL; do
  [ -n "${NAME}" ] && [ -n "${URL}" ] || continue
  echo "  下载 ${NAME}"
  # -L:browser_download_url 会跳转到对象存储;--max-time 1800 已由 G 统一带上。
  GF -L --retry 3 --retry-delay 5 -o "${DOWNLOAD_DIR}/${NAME}" "${URL}" || {
    echo "错误:下载附件失败:${NAME}"
    exit 1
  }
done <<< "${ASSETS}"

# ---------- 2. 改名复制:ShengShouYunHe 前缀 → 圣手运河(SHA256SUMS.txt 不改名)----------
UPLOAD_DIR="${TMP}/gitee"
mkdir -p "${UPLOAD_DIR}"
for f in "${DOWNLOAD_DIR}"/*; do
  N="$(basename "${f}")"
  case "${N}" in
    ShengShouYunHe*) NEW="圣手运河${N#ShengShouYunHe}" ;;
    *)               NEW="${N}" ;; # SHA256SUMS.txt 及其他非前缀文件保持原名
  esac
  cp "${f}" "${UPLOAD_DIR}/${NEW}"
done

# ---------- 3. 组装 Gitee Release body ----------
# annotated tag 注释:用 git ls-remote 解析 tag 对象 sha 后走 GitHub API 读 message
# (不依赖本地 git 仓库有该 tag);取不到回退默认文案。
NOTES=""
if command -v git >/dev/null 2>&1; then
  LSREMOTE_URL="https://github.com/${GITHUB_REPO}.git"
  if [ -n "${GITHUB_TOKEN}" ]; then
    # 凭据只进 URL,任何路径都不会打印该 URL(ls-remote 失败走 || 兜底)。
    LSREMOTE_URL="https://x-access-token:${GITHUB_TOKEN}@github.com/${GITHUB_REPO}.git"
  fi
  # head -n1:annotated tag 的 ls-remote 会输出 tag 对象与 ^{peeled commit} 两行,
  # 第一行才是 tag 对象 sha;轻量 tag 只有一行(commit sha),后续 API 404 → 回退。
  TAGSHA="$(git ls-remote "${LSREMOTE_URL}" "refs/tags/${TAG}" 2>/dev/null \
    | awk '{print $1}' | head -n1 || true)"
  if [ -n "${TAGSHA}" ]; then
    NOTES="$(gh_api "https://api.github.com/repos/${GITHUB_REPO}/git/tags/${TAGSHA}" \
      | json_get message || true)"
  fi
fi
if [ -z "$(printf '%s' "${NOTES}" | tr -d '[:space:]')" ]; then
  NOTES="自动构建产物:macOS(Apple Silicon / Intel)、Windows 与 Linux 安装包。"
fi

# 围栏块与 CI 生成的 SHA256SUMS.txt 同格式、同内容(改名复制不改变文件内容)。
# 键固定顺序:darwin-arm64 / darwin-amd64 / windows-amd64 / linux-amd64;
# NSIS 安装器名虽含 windows-amd64,但后缀不同,不会误匹配。
SUMS=""
for key in darwin-arm64 darwin-amd64 windows-amd64 linux-amd64; do
  if [ "${key}" = "linux-amd64" ]; then
    F="${UPLOAD_DIR}/圣手运河-${TAG}-${key}.tar.gz"
  else
    F="${UPLOAD_DIR}/圣手运河-${TAG}-${key}.zip"
  fi
  if [ -f "${F}" ]; then
    [ -z "${SUMS}" ] || SUMS="${SUMS}"$'\n'
    SUMS="${SUMS}${key}: $(sha256_of "${F}")"
  else
    echo "::warning::缺少 ${key} 平台附件,sha256sums 围栏块缺该键"
  fi
done

BODY_FILE="${TMP}/body.md"
{
  printf '%s\n' "${NOTES}"
  if [ -n "${SUMS}" ]; then
    printf '\n```sha256sums\n'
    printf '%s\n' "${SUMS}"
    printf '```\n'
  fi
} > "${BODY_FILE}"

# ---------- 4. 查/建 Gitee 版本化 Release ----------
RID="$(G "https://gitee.com/api/v5/repos/${GITEE_REPO}/releases/tags/${TAG}?access_token=${GITEE_TOKEN}" \
  | json_get id || true)"
if [ -n "${RID}" ]; then
  # 重跑/复用已存在的发布:PATCH body,保持与「tag 注释 + sha256sums」同步。
  echo "== 复用已存在的 Gitee Release ${TAG}(id=${RID}),PATCH body 同步 =="
  PATCH_PAYLOAD="${TMP}/patch.json"
  BODY_FILE="${BODY_FILE}" PAYLOAD="${PATCH_PAYLOAD}" python3 - <<'PY'
import json, os

body = open(os.environ["BODY_FILE"], encoding="utf-8").read()
open(os.environ["PAYLOAD"], "w", encoding="utf-8").write(
    json.dumps({"body": body}, ensure_ascii=False))
PY
  RESP="$(G -X PATCH "https://gitee.com/api/v5/repos/${GITEE_REPO}/releases/${RID}?access_token=${GITEE_TOKEN}" \
    -H "Content-Type: application/json" --data-binary "@${PATCH_PAYLOAD}")" || RESP=""
  if printf '%s' "${RESP}" | grep -q '"id"'; then
    echo "  body 已同步"
  else
    echo "::warning::PATCH Gitee Release body 失败(附件上传继续):$(printf '%s' "${RESP}" | tr '\n' ' ' | cut -c1-300)"
  fi
else
  echo "== 创建 Gitee Release ${TAG} =="
  # JSON 组装交给 python3,避免正文里的换行/引号手工转义出错。
  CREATE_PAYLOAD="${TMP}/create.json"
  TAG_NAME="${TAG}" BODY_FILE="${BODY_FILE}" PAYLOAD="${CREATE_PAYLOAD}" python3 - <<'PY'
import json, os

body = open(os.environ["BODY_FILE"], encoding="utf-8").read()
payload = {
    "tag_name": os.environ["TAG_NAME"],
    "target_commitish": os.environ["TAG_NAME"],
    "name": os.environ["TAG_NAME"],
    "body": body,
    "prerelease": False,
}
open(os.environ["PAYLOAD"], "w", encoding="utf-8").write(
    json.dumps(payload, ensure_ascii=False))
PY
  RESP="$(G -X POST "https://gitee.com/api/v5/repos/${GITEE_REPO}/releases?access_token=${GITEE_TOKEN}" \
    -H "Content-Type: application/json" --data-binary "@${CREATE_PAYLOAD}")" || RESP=""
  RID="$(printf '%s' "${RESP}" | json_get id || true)"
fi
[ -n "${RID}" ] || {
  echo "错误:Gitee Release 创建失败:$(printf '%s' "${RESP}" | tr '\n' ' ' | cut -c1-300)"
  exit 1
}

# ---------- 5. 幂等上传附件 ----------
# 查已有附件列表必须带 per_page=100(默认只回 20 条,踩过的坑)。
EXISTING="$(G "https://gitee.com/api/v5/repos/${GITEE_REPO}/releases/${RID}?access_token=${GITEE_TOKEN}&per_page=100" \
  | python3 -c "
import sys, json
try:
    d = json.load(sys.stdin) or {}
except Exception:
    d = {}
print(' '.join(a.get('name', '') for a in d.get('assets', [])))
")" || EXISTING=""

UPLOADED=0
SKIPPED=0
GIVEN_UP=0
for f in "${UPLOAD_DIR}"/*; do
  N="$(basename "${f}")"
  case " ${EXISTING} " in
    *" ${N} "*)
      echo "跳过(已存在) ${N}"
      SKIPPED=$((SKIPPED + 1))
      continue
      ;;
  esac
  echo "上传 ${N}"
  OK=""
  # 重试 4 次、间隔 RETRY_INTERVAL(默认 30)秒,原因见文件头;最后一次失败后不再空等。
  for attempt in 1 2 3 4; do
    # || 兜底:curl 直连失败会让带 set -e 的脚本中止,这里吞掉退出码,靠响应内容判成败。
    RESP="$(G -X POST "https://gitee.com/api/v5/repos/${GITEE_REPO}/releases/${RID}/attach_files" \
      -F "access_token=${GITEE_TOKEN}" -F "file=@${f}")" || RESP=""
    if printf '%s' "${RESP}" | grep -q '"browser_download_url"'; then OK=1; break; fi
    echo "::warning::上传失败(第 ${attempt} 次) ${N}:$(printf '%s' "${RESP}" | tr '\n' ' ' | cut -c1-300)"
    if [ "${attempt}" -lt 4 ]; then sleep "${RETRY_INTERVAL}"; fi
  done
  if [ -n "${OK}" ]; then
    echo "  完成 ${N}"
    UPLOADED=$((UPLOADED + 1))
  else
    echo "::error::放弃上传 ${N}"
    GIVEN_UP=$((GIVEN_UP + 1))
  fi
done

echo "== 同步完成:https://gitee.com/${GITEE_REPO}/releases/tag/${TAG} =="
echo "汇总:上传 ${UPLOADED} 个、跳过 ${SKIPPED} 个、放弃 ${GIVEN_UP} 个"
[ "${GIVEN_UP}" -eq 0 ] || exit 1
