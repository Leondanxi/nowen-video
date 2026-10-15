/**
 * 自动画质 ABR 纯逻辑模块（§13）。
 *
 * 本模块不依赖 React / hls.js，只接收外部喂入的观测输入并输出"应切换到哪个档位"。
 * 这样判定口径可独立单测、可在 VideoPlayer / SessionVideoPlayer 两边一致地接线。
 *
 * 阶梯（rungs）按 高→低 排序，index 0 固定为「原画(original)」：
 *   original → 4k → 2k → 1080p → 720p → 640p
 * 起始固定原画；卡顿立即降一档；带宽估算可靠时直接收敛到最高可用档；
 * 恢复后按迟滞（hysteresis）逐级升回，直至原画。
 */

export interface AutoRung {
  /** 档位 id（对应 TranscodePreset.id） */
  id: string
  /** 该档所需视频码率 KBPS；original 未知源码率时传 Infinity */
  bitrateKbps: number
}

export interface AutoQualityConfig {
  /** 降档冷却：切换后至少间隔（默认 6s） */
  downgradeCooldownMs: number
  /** 升档观察持续时间（默认 10s） */
  upgradeSustainMs: number
  /** 升档所需前向缓冲（秒，默认 10s） */
  upgradeBufferSec: number
  /** 升档带宽余量阈值：bw >= 上一档 bitrate / 0.7 */
  upgradeBwUpFactor: number
  /** 带宽选档系数：选满足 bitrate <= bw*0.9 的最高档 */
  bwSelectFactor: number
  /** 非 seek 的等待/卡顿持续超过该秒数即降一档（默认 0.5s） */
  stallDowngradeSec: number
  /** 前向缓冲低于该秒数计一次低缓冲（默认 2s） */
  lowBufferSec: number
  /** 连续低缓冲次数触发降档（默认 2） */
  lowBufferStrikes: number
  /** 连续"追不上实时"的分片次数触发降档（默认 3） */
  fragSlowStrikeLimit: number
}

export const DEFAULT_AUTO_QUALITY_CONFIG: AutoQualityConfig = {
  downgradeCooldownMs: 6_000,
  upgradeSustainMs: 10_000,
  upgradeBufferSec: 10,
  upgradeBwUpFactor: 0.7, // bw >= next.bitrate / 0.7
  bwSelectFactor: 0.9,
  stallDowngradeSec: 0.5,
  lowBufferSec: 2,
  lowBufferStrikes: 2,
  fragSlowStrikeLimit: 3,
}

export type AutoQualityDecision =
  | { type: 'switch'; rungId: string }
  | { type: 'noop' }

/** 原画档 id（固定内置行，不可删） */
export const ORIGINAL_PRESET_ID = 'original'

export class AutoQualityController {
  private readonly config: AutoQualityConfig
  private rungs: AutoRung[] = []
  private index = 0
  private lastSwitchAt = 0

  private bwKbps = 0
  private bufferSec = 0

  // 卡顿追踪
  private stallSince: number | null = null
  private lowBufferStrikes = 0
  private fragSlowStrikes = 0

  // 升档迟滞追踪
  private upgradeCandidateIdx: number | null = null
  private upgradeSince: number | null = null

  constructor(config: Partial<AutoQualityConfig> = {}) {
    this.config = { ...DEFAULT_AUTO_QUALITY_CONFIG, ...config }
  }

  /** 设置阶梯（高→低，index0=原画）。会重置到原画并清空追踪状态。 */
  setRungs(rungs: AutoRung[]): void {
    this.rungs = [...rungs]
    this.reset()
  }

  /** 回到原画起点，清空所有追踪状态。 */
  reset(now = 0): void {
    this.index = 0
    this.lastSwitchAt = now
    this.stallSince = null
    this.lowBufferStrikes = 0
    this.fragSlowStrikes = 0
    this.upgradeCandidateIdx = null
    this.upgradeSince = null
  }

  /** 外部（手动选档/会话重启）强制把控制器对齐到某个已有档位。 */
  adoptRung(rungId: string, now: number): void {
    const idx = this.rungs.findIndex((r) => r.id === rungId)
    if (idx < 0) return
    this.index = idx
    this.lastSwitchAt = now
    this.lowBufferStrikes = 0
    this.fragSlowStrikes = 0
    this.upgradeCandidateIdx = null
    this.upgradeSince = null
  }

  get currentRungId(): string | null {
    return this.rungs[this.index]?.id ?? null
  }

  get currentIndex(): number {
    return this.index
  }

  /** hls.bandwidthEstimate（FRAG_LOADED/FRAG_BUFFERED 刷新），单位 bps → 内部换算 kbps */
  notifyBandwidth(bps: number): void {
    if (bps > 0) this.bwKbps = bps / 1000
  }

  /** 原生 waiting/stalled(true)、playing/stalled-end(false)。已排除 seek 触发。 */
  notifyStallChange(stalling: boolean, now: number): void {
    if (stalling) {
      if (this.stallSince === null) this.stallSince = now
    } else {
      this.stallSince = null
    }
  }

  /** BUFFER_STALLED_ERROR 等致命卡顿事件 */
  notifyStalledError(): void {
    this.stalledErrorPending = true
  }
  private stalledErrorPending = false

  /** 一个分片下载耗时 > 分片时长（追不上实时） */
  notifyFragmentTooSlow(): void {
    this.fragSlowStrikes += 1
  }

  /** 周期性前向缓冲健康度 buffered.end - currentTime（秒） */
  notifyBufferHealth(bufferSec: number): void {
    this.bufferSec = bufferSec
  }

  private chooseRung(now: number): AutoQualityDecision {
    if (this.rungs.length === 0) return { type: 'noop' }
    const cfg = this.config
    const inCooldown = now - this.lastSwitchAt < cfg.downgradeCooldownMs

    // —— 1) 立即降档（卡顿/缓冲不足/追不上实时）——
    const stalledLongEnough = this.stallSince !== null && now - this.stallSince >= cfg.stallDowngradeSec * 1000
    if (!inCooldown && (this.stalledErrorPending || stalledLongEnough)) {
      this.stalledErrorPending = false
      return this.downgrade(now)
    }
    this.stalledErrorPending = false

    if (!inCooldown && this.bufferSec < cfg.lowBufferSec) {
      this.lowBufferStrikes += 1
      if (this.lowBufferStrikes >= cfg.lowBufferStrikes) {
        this.lowBufferStrikes = 0
        return this.downgrade(now)
      }
    } else if (this.bufferSec >= cfg.lowBufferSec) {
      this.lowBufferStrikes = 0
    }

    if (!inCooldown && this.fragSlowStrikes >= cfg.fragSlowStrikeLimit) {
      this.fragSlowStrikes = 0
      return this.downgrade(now)
    }

    // —— 2) 带宽选档（估算可靠时）——
    if (this.bwKbps > 0) {
      const desired = this.pickByBandwidth(this.bwKbps * cfg.bwSelectFactor)
      if (desired > this.index) {
        // 带宽恶化：直接跨多档下探（快速收敛）
        this.index = desired
        this.lastSwitchAt = now
        this.upgradeCandidateIdx = null
        this.upgradeSince = null
        return { type: 'switch', rungId: this.rungs[desired].id }
      }
      if (desired < this.index) {
        // 带宽恢复：逐级升档，需迟滞 + 缓冲 + 持续观察
        const nextIdx = this.index - 1
        const required = this.rungs[Math.max(nextIdx, 1)]?.bitrateKbps ?? 0
        const bwOk = this.bwKbps >= required / cfg.upgradeBwUpFactor
        const bufferOk = this.bufferSec >= cfg.upgradeBufferSec
        if (bwOk && bufferOk) {
          if (this.upgradeCandidateIdx !== nextIdx) {
            this.upgradeCandidateIdx = nextIdx
            this.upgradeSince = now
          } else if (this.upgradeSince !== null && now - this.upgradeSince >= cfg.upgradeSustainMs) {
            this.index = nextIdx
            this.lastSwitchAt = now
            this.upgradeCandidateIdx = null
            this.upgradeSince = null
            return { type: 'switch', rungId: this.rungs[nextIdx].id }
          }
        } else {
          this.upgradeCandidateIdx = null
          this.upgradeSince = null
        }
      }
    }

    return { type: 'noop' }
  }

  /** 周期性 tick（建议 250~500ms）；返回需要切换到的档位或 null */
  tick(now: number): AutoQualityDecision | null {
    const decision = this.chooseRung(now)
    if (decision.type === 'switch') return decision
    return null
  }

  private downgrade(now: number): AutoQualityDecision {
    if (this.index >= this.rungs.length - 1) {
      // 已在最低档，不再降
      this.lastSwitchAt = now
      return { type: 'noop' }
    }
    this.index += 1
    this.lastSwitchAt = now
    this.upgradeCandidateIdx = null
    this.upgradeSince = null
    return { type: 'switch', rungId: this.rungs[this.index].id }
  }

  /** 选满足 bitrate <= bwKbps 的最高档（最小 index）；original=Infinity 不会被带宽选中 */
  private pickByBandwidth(bwKbps: number): number {
    for (let i = 0; i < this.rungs.length; i += 1) {
      if (this.rungs[i].bitrateKbps <= bwKbps) return i
    }
    return this.rungs.length - 1
  }
}
