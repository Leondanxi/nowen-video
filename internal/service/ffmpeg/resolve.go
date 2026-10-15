package ffmpeg

import "strings"

// 解码模式（用户高层选择）字符串枚举（冻结规格 §2）。
const (
	HWModeSoftware  = "software"
	HWModeHardware  = "hardware"
	HWModeAuto      = "auto"
	EncoderModeAuto = "auto"
)

// ResolveBackend 按用户解码模式 / 硬件 API 偏好 / 本地探测结果解析最终后端。
//
// 冻结规格 §6（纯函数，需单测）：
//
//	software          -> none
//	hardware:
//	   hwEncoder != auto -> hwEncoder   // 关键：即使本地探测不到也强制（远端 GPU）
//	   否则              -> detected
//	auto:
//	   detected != none -> detected
//	   hwEncoder != auto -> hwEncoder   // auto 下也允许配置的编码器兜底
//	   否则              -> none
func ResolveBackend(hwDecodeMode, hwEncoder, detected string) string {
	switch normalizeMode(hwDecodeMode) {
	case HWModeSoftware:
		return HWAccelNone
	case HWModeHardware:
		if enc := normalizeEncoder(hwEncoder); enc != EncoderModeAuto {
			return enc
		}
		return normalizeDetected(detected)
	default: // auto
		if d := normalizeDetected(detected); d != HWAccelNone {
			return d
		}
		if enc := normalizeEncoder(hwEncoder); enc != EncoderModeAuto {
			return enc
		}
		return HWAccelNone
	}
}

// normalizeMode 归一化解码模式，未知值一律视为 auto。
func normalizeMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case HWModeSoftware:
		return HWModeSoftware
	case HWModeHardware:
		return HWModeHardware
	default:
		return HWModeAuto
	}
}

// normalizeEncoder 归一化硬件 API 偏好；空 / auto 视为 auto。
func normalizeEncoder(encoder string) string {
	switch strings.ToLower(strings.TrimSpace(encoder)) {
	case "", EncoderModeAuto:
		return EncoderModeAuto
	case HWAccelNVENC:
		return HWAccelNVENC
	case HWAccelQSV:
		return HWAccelQSV
	case HWAccelVAAPI:
		return HWAccelVAAPI
	case HWAccelAMF:
		return HWAccelAMF
	default:
		return EncoderModeAuto
	}
}

// normalizeDetected 归一化本地探测结果；未知 / none 视为软件。
func normalizeDetected(detected string) string {
	switch strings.ToLower(strings.TrimSpace(detected)) {
	case HWAccelNVENC:
		return HWAccelNVENC
	case HWAccelQSV:
		return HWAccelQSV
	case HWAccelVAAPI:
		return HWAccelVAAPI
	case HWAccelAMF:
		return HWAccelAMF
	default:
		return HWAccelNone
	}
}
