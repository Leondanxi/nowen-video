package service

import (
	"os"
	"strconv"
	"strings"

	"github.com/nowen-video/nowen-video/internal/config"
	"github.com/nowen-video/nowen-video/internal/repository"
)

// 转码相关热设置键（冻结规格 §3.1）。DB SystemSetting KV，保存即生效。
// 与 internal/handler/admin_system.go 的 GET/PUT /settings/system 共用同一份 key。
const (
	SettingKeyHWDecodeMode     = "hw_decode_mode"
	SettingKeyHWEncoder        = "hw_encoder"
	SettingKeyGPUFallbackCPU   = "gpu_fallback_cpu"
	SettingKeyFFmpegPath       = "ffmpeg_path"
	SettingKeyFFprobePath      = "ffprobe_path"
	SettingKeyTranscodeMaxSess = "transcode_max_sessions"
	SettingKeyTranscodeSegDur  = "transcode_segment_duration"
	SettingKeyTranscodeCRF     = "transcode_crf"
	SettingKeyBrowserHEVC      = "browser_hevc"
	SettingKeyQualityPresets   = "quality_presets"
	SettingKeyDefaultQuality   = "default_quality_preset"
)

// 用户容器环境变量覆盖名（需求 B，固定名）。优先级：热设置(DB) > 这些 env > Viper/默认。
const (
	EnvOverrideFFmpegPath   = "NOWEN_APP_FFMPEG_PATH"
	EnvOverrideFFprobePath  = "NOWEN_APP_FFPROBE_PATH"
	EnvOverrideHWDecodeMode = "NOWEN_TRANSCODE_HW_DECODE_MODE"
	EnvOverrideHWEncoder    = "NOWEN_TRANSCODE_HW_ENCODER"
)

// 默认值。
const (
	defaultHWDecodeMode      = "auto"
	defaultHWEncoder         = "auto"
	defaultGPUFallbackCPU    = false
	defaultTranscodeMaxSess  = 6
	defaultTranscodeSegDur   = 6
	defaultTranscodeCRF      = 18
	defaultBrowserHEVC       = true
	defaultQualityPresetName = "auto"
)

func settingStr(repo *repository.SystemSettingRepo, key, def string) string {
	if repo == nil {
		return def
	}
	v, err := repo.Get(key)
	if err != nil || v == "" {
		return def
	}
	return v
}

func settingBool(repo *repository.SystemSettingRepo, key string, def bool) bool {
	if repo == nil {
		return def
	}
	v, err := repo.Get(key)
	if err != nil {
		return def
	}
	return v == "true" || v == "1"
}

func settingInt(repo *repository.SystemSettingRepo, key string, def int) int {
	if repo == nil {
		return def
	}
	v, err := repo.Get(key)
	if err != nil || v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return def
	}
	return n
}

// ResolveFFmpegPath 解析 ffmpeg 可执行路径，优先级：热设置(DB ffmpeg_path) >
// 环境变量 NOWEN_APP_FFMPEG_PATH > Viper/cfg.App.FFmpegPath > 默认 "ffmpeg"。
// 所有 ffmpeg 调用统一走此入口。
func ResolveFFmpegPath(repo *repository.SystemSettingRepo, cfg *config.Config) string {
	if v := strings.TrimSpace(settingStr(repo, SettingKeyFFmpegPath, "")); v != "" {
		return v
	}
	if v := strings.TrimSpace(os.Getenv(EnvOverrideFFmpegPath)); v != "" {
		return v
	}
	if cfg != nil && strings.TrimSpace(cfg.App.FFmpegPath) != "" {
		return cfg.App.FFmpegPath
	}
	return "ffmpeg"
}

// ResolveFFprobePath 同理，对应 NOWEN_APP_FFPROBE_PATH。
func ResolveFFprobePath(repo *repository.SystemSettingRepo, cfg *config.Config) string {
	if v := strings.TrimSpace(settingStr(repo, SettingKeyFFprobePath, "")); v != "" {
		return v
	}
	if v := strings.TrimSpace(os.Getenv(EnvOverrideFFprobePath)); v != "" {
		return v
	}
	if cfg != nil && strings.TrimSpace(cfg.App.FFprobePath) != "" {
		return cfg.App.FFprobePath
	}
	return "ffprobe"
}

// resolveHWDecodeMode 解析解码模式：热设置 > 环境变量 NOWEN_TRANSCODE_HW_DECODE_MODE > 默认 auto。
func resolveHWDecodeMode(repo *repository.SystemSettingRepo) string {
	if v := strings.TrimSpace(settingStr(repo, SettingKeyHWDecodeMode, "")); v != "" {
		return v
	}
	if v := strings.TrimSpace(os.Getenv(EnvOverrideHWDecodeMode)); v != "" {
		return v
	}
	return defaultHWDecodeMode
}

// resolveHWEncoder 同理，对应 NOWEN_TRANSCODE_HW_ENCODER。
func resolveHWEncoder(repo *repository.SystemSettingRepo) string {
	if v := strings.TrimSpace(settingStr(repo, SettingKeyHWEncoder, "")); v != "" {
		return v
	}
	if v := strings.TrimSpace(os.Getenv(EnvOverrideHWEncoder)); v != "" {
		return v
	}
	return defaultHWEncoder
}
