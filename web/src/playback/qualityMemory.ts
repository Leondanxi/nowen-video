/**
 * 每片画质记忆（§13）。
 * localStorage 单一 JSON map：key = `nowen-quality-map`，值 `{ [mediaId]: presetId }`。
 * 只在「用户手动选档」时写入；自动控制器的自动切换绝不写入。
 * 读取到的手动选择优先于全局默认（auto）。Safari/隐私模式等异常时优雅降级。
 */

const STORAGE_KEY = 'nowen-quality-map'

type QualityMap = Record<string, string>

function readMap(): QualityMap {
  if (typeof window === 'undefined' || !window.localStorage) return {}
  try {
    const raw = window.localStorage.getItem(STORAGE_KEY)
    if (!raw) return {}
    const parsed = JSON.parse(raw) as QualityMap
    return parsed && typeof parsed === 'object' ? parsed : {}
  } catch {
    return {}
  }
}

/** 读取该片的手动画质选择；无记录返回 null（走全局默认 auto）。 */
export function getRememberedQuality(mediaId: string): string | null {
  if (!mediaId) return null
  const value = readMap()[mediaId]
  return typeof value === 'string' && value.length > 0 ? value : null
}

/** 记录该片的手动画质选择（仅手动调用）。 */
export function rememberQuality(mediaId: string, presetId: string): void {
  if (!mediaId || !presetId) return
  if (typeof window === 'undefined' || !window.localStorage) return
  try {
    const map = readMap()
    if (map[mediaId] === presetId) return
    map[mediaId] = presetId
    window.localStorage.setItem(STORAGE_KEY, JSON.stringify(map))
  } catch {
    /* 存储已满/隐私模式：静默降级 */
  }
}
