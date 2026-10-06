package service

import (
	"os"
	"strconv"

	"github.com/nowen-video/nowen-video/internal/repository"
)

// 转码相关热设置键（冻结规格 §3.1）。DB SystemSetting KV，保存即生效。
// 与 internal/handler/admin_system.go 的 GET/PUT /settings/system 共用同一份 key。
const (
	SettingKeyHWDecodeMode       = "hw_decode_mode"
	SettingKeyHWEncoder          = "hw_encoder"
	SettingKeyGPUFallbackCPU     = "gpu_fallback_cpu"
	SettingKeyFFmpegPath         = "ffmpeg_path"
	SettingKeyFFprobePath        = "ffprobe_path"
	SettingKeyFFOIPEnabled       = "ffoip_enabled"
	SettingKeyFFOIPServerAddress = "ffoip_server_address"
	SettingKeyFFOIPAuthSecret    = "ffoip_auth_secret"
	SettingKeyTranscodeMaxSess   = "transcode_max_sessions"
	SettingKeyTranscodeSegDur    = "transcode_segment_duration"
	SettingKeyTranscodeCRF       = "transcode_crf"
	SettingKeyBrowserHEVC        = "browser_hevc"
	SettingKeyQualityPresets     = "quality_presets"
	SettingKeyDefaultQuality     = "default_quality_preset"

	// ffoip 子进程环境变量名（§1/§9，精确名）。
	envFFOIPClientAddress = "FFMPEG_OVER_IP_CLIENT_ADDRESS"
	envFFOIPAuthSecret    = "FFMPEG_OVER_IP_CLIENT_AUTH_SECRET"
	envFFOIPClientLog     = "FFMPEG_OVER_IP_CLIENT_LOG"
)

// 默认值（§3.1）。
const (
	defaultHWDecodeMode       = "auto"
	defaultHWEncoder          = "auto"
	defaultGPUFallbackCPU     = false
	defaultTranscodeMaxSess   = 6
	defaultTranscodeSegDur    = 6
	defaultTranscodeCRF       = 18
	defaultBrowserHEVC        = true
	defaultQualityPresetName  = "auto"
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

// buildFFOIPEnv 按 ffoip_enabled 构造注入给 ffmpeg/ffprobe 子进程的环境变量（§9）。
// 未启用或地址为空时返回 nil。
func buildFFOIPEnv(repo *repository.SystemSettingRepo) []string {
	if !settingBool(repo, SettingKeyFFOIPEnabled, false) {
		return nil
	}
	address := settingStr(repo, SettingKeyFFOIPServerAddress, "")
	if address == "" {
		return nil
	}
	env := []string{
		envFFOIPClientAddress + "=" + address,
	}
	if secret := settingStr(repo, SettingKeyFFOIPAuthSecret, ""); secret != "" {
		env = append(env, envFFOIPAuthSecret+"="+secret)
	}
	// 可选日志：透传当前进程的 FFOIP log 开关（若有），便于排障。
	if lvl := os.Getenv("FFMPEG_OVER_IP_CLIENT_LOG"); lvl != "" {
		env = append(env, envFFOIPClientLog+"="+lvl)
	}
	return env
}
