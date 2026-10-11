// 「忽略此版本」:更新弹窗写入被忽略的 latest_version,顶栏静默探测读取后
// 不为该版本点亮红点。手动检查(设置页/顶栏按钮)不受忽略限制,仍正常弹窗。
// 只记录单个版本号:新版本发布后 latest_version 变化,忽略自然失效。
export const IGNORED_VERSION_KEY = 'update.ignoredVersion'

// 被忽略的版本号;未设置时返回空串(与任何 latest_version 都不相等)。
export function getIgnoredVersion(): string {
  return localStorage.getItem(IGNORED_VERSION_KEY) ?? ''
}

export function ignoreVersion(version: string): void {
  localStorage.setItem(IGNORED_VERSION_KEY, version)
}
