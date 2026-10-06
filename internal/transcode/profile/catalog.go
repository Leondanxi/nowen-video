package profile

import (
	"fmt"
	"regexp"
	"sync/atomic"
)

// Preset is the single ordered quality catalog shared by runtime transcode,
// ABR playlists and the legacy preprocess pipeline. Runtime and persistent ABR
// bitrates remain explicit policies because they optimize different workloads;
// resolution, naming and audio policy must not drift between pipelines.
//
// 新增字段（冻结规格 §5）：
//   - CRF        每档恒定质量值；0 = 用全局 transcode_crf 或按码率。
//   - Fixed      仅 original=true（不可删 / 不可改 id 与 0 值）。
//   - BitrateKbps 数值视频码率（KBPS），便于比较 / ABR / 对外 DTO；
//     保留既有字符串码率字段（RuntimeVideoBitrate 等）以兼容持久化路径。
//   - DisplayName 对外展示名（§4 name，如 "原画"/"720P"）。
//   - AudioKbps  数值音频码率（KBPS）。
type Preset struct {
	Name                   string
	DisplayName            string
	Width                  int
	Height                 int
	AudioBitrate           string
	RuntimeVideoBitrate    string
	PersistentVideoBitrate string
	PersistentMaxBitrate   string
	PersistentBufSize      string

	CRF         int
	Fixed       bool
	BitrateKbps int
	AudioKbps   int
}

// EncodingProfile is the transport-neutral shape consumed by FFmpeg adapters
// and exposed by the legacy ABR status API.
type EncodingProfile struct {
	Name         string `json:"name"`
	Width        int    `json:"width"`
	Height       int    `json:"height"`
	VideoBitrate string `json:"video_bitrate"`
	AudioBitrate string `json:"audio_bitrate"`
	MaxBitrate   string `json:"max_bitrate"`
	BufSize      string `json:"buf_size"`
}

var catalog = []Preset{
	{Name: "360p", Width: 640, Height: 360, AudioBitrate: "96k", RuntimeVideoBitrate: "800k", PersistentVideoBitrate: "800k", PersistentMaxBitrate: "1200k", PersistentBufSize: "1600k"},
	{Name: "480p", Width: 854, Height: 480, AudioBitrate: "128k", RuntimeVideoBitrate: "1500k", PersistentVideoBitrate: "1400k", PersistentMaxBitrate: "2100k", PersistentBufSize: "2800k"},
	{Name: "720p", Width: 1280, Height: 720, AudioBitrate: "128k", RuntimeVideoBitrate: "3000k", PersistentVideoBitrate: "2800k", PersistentMaxBitrate: "4200k", PersistentBufSize: "5600k"},
	{Name: "1080p", Width: 1920, Height: 1080, AudioBitrate: "192k", RuntimeVideoBitrate: "6000k", PersistentVideoBitrate: "5000k", PersistentMaxBitrate: "7500k", PersistentBufSize: "10000k"},
	{Name: "2K", Width: 2560, Height: 1440, AudioBitrate: "192k", RuntimeVideoBitrate: "12000k", PersistentVideoBitrate: "10000k", PersistentMaxBitrate: "15000k", PersistentBufSize: "20000k"},
	{Name: "4K", Width: 3840, Height: 2160, AudioBitrate: "256k", RuntimeVideoBitrate: "25000k", PersistentVideoBitrate: "20000k", PersistentMaxBitrate: "30000k", PersistentBufSize: "40000k"},
}

// Presets returns a copy so callers cannot mutate the process-wide catalog.
func Presets() []Preset {
	result := make([]Preset, len(catalog))
	copy(result, catalog)
	return result
}

func Find(name string) (Preset, bool) {
	for _, preset := range catalog {
		if preset.Name == name {
			return preset, true
		}
	}
	return Preset{}, false
}

func Runtime(name string) (EncodingProfile, bool) {
	preset, ok := Find(name)
	if !ok {
		return EncodingProfile{}, false
	}
	return EncodingProfile{
		Name:         preset.Name,
		Width:        preset.Width,
		Height:       preset.Height,
		VideoBitrate: preset.RuntimeVideoBitrate,
		AudioBitrate: preset.AudioBitrate,
	}, true
}

func Persistent(name string) (EncodingProfile, bool) {
	preset, ok := Find(name)
	if !ok {
		return EncodingProfile{}, false
	}
	return persistentProfile(preset), true
}

func PersistentProfiles() []EncodingProfile {
	profiles := make([]EncodingProfile, 0, len(catalog))
	for _, preset := range catalog {
		profiles = append(profiles, persistentProfile(preset))
	}
	return profiles
}

func Names() []string {
	names := make([]string, 0, len(catalog))
	for _, preset := range catalog {
		names = append(names, preset.Name)
	}
	return names
}

func NamesUpToHeight(height int) []string {
	names := make([]string, 0, len(catalog))
	for _, preset := range catalog {
		if preset.Height <= height {
			names = append(names, preset.Name)
		}
	}
	return names
}

func HighestPersistentAtOrBelow(height int) (EncodingProfile, bool) {
	var selected Preset
	found := false
	for _, preset := range catalog {
		if preset.Height <= height && (!found || preset.Height > selected.Height) {
			selected = preset
			found = true
		}
	}
	if !found {
		return EncodingProfile{}, false
	}
	return persistentProfile(selected), true
}

func persistentProfile(preset Preset) EncodingProfile {
	return EncodingProfile{
		Name:         preset.Name,
		Width:        preset.Width,
		Height:       preset.Height,
		VideoBitrate: preset.PersistentVideoBitrate,
		AudioBitrate: preset.AudioBitrate,
		MaxBitrate:   preset.PersistentMaxBitrate,
		BufSize:      preset.PersistentBufSize,
	}
}

// ==================== 共享画质档位（冻结规格 §4 / §5） ====================
//
// 这是前后端共用的权威画质阶梯。它与上面的 legacy `catalog`（360p..4K，
// 带字符串码率，服务于持久化预处理 / ABR 状态接口）相互独立：
//   - legacy catalog：Presets()/Persistent()/Names()/… 行为保持不变。
//   - 本阶梯：Effective()/RuntimeEffective()，供播放器画质菜单与实时转码使用。

// PresetIDOriginal 是固定内置"原画"档位的 id（width=height=bitrate=0）。
const PresetIDOriginal = "original"

// PresetIDAuto 是虚拟"自动档"id，不是 preset 表中的一行，前端菜单置顶。
const PresetIDAuto = "auto"

// defaultQualityLadder 是内置默认画质阶梯（§4.1，按播放器菜单顺序降序）。
// height 由 width*9/16 派生：640→360, 1280→720, 1920→1080, 2560→1440, 3840→2160。
func defaultQualityLadder() []Preset {
	return []Preset{
		{Name: "original", DisplayName: "原画", Width: 0, Height: 0, BitrateKbps: 0, AudioKbps: 0, CRF: 0, Fixed: true},
		{Name: "4k", DisplayName: "4K", Width: 3840, Height: 2160, BitrateKbps: 8000, AudioKbps: 256, Fixed: false},
		{Name: "2k", DisplayName: "2K", Width: 2560, Height: 1440, BitrateKbps: 3500, AudioKbps: 192, Fixed: false},
		{Name: "1080p", DisplayName: "1080P", Width: 1920, Height: 1080, BitrateKbps: 2000, AudioKbps: 192, Fixed: false},
		{Name: "720p", DisplayName: "720P", Width: 1280, Height: 720, BitrateKbps: 1000, AudioKbps: 128, Fixed: false},
		{Name: "640p", DisplayName: "640P", Width: 640, Height: 360, BitrateKbps: 600, AudioKbps: 96, Fixed: false},
	}
}

// idPattern 限定档位 id（要进 URL）：仅字母 / 数字 / 下划线 / 短横。
var idPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// qualityOverrides 保存热加载的画质档位覆盖。nil 表示使用内置默认阶梯。
var qualityOverrides atomic.Value // holds []Preset

// defaultPresetName 是播放默认档位（全局默认 = 自动）。
const defaultPresetName = PresetIDAuto

// DefaultPresetName 返回播放默认档位 id（冻结默认 "auto"）。
func DefaultPresetName() string { return defaultPresetName }

// DeriveHeight 由宽度按 16:9 派生高度（width<=0 时返回 0）。
func DeriveHeight(width int) int {
	if width <= 0 {
		return 0
	}
	return width * 9 / 16
}

// SetOverrides 原子地设置共享画质档位覆盖。空 / nil 表示清空覆盖、回退内置默认。
// 校验失败时整体拒绝，返回明确错误，且不修改当前生效档位。
func SetOverrides(list []Preset) error {
	if len(list) == 0 {
		qualityOverrides.Store(([]Preset)(nil))
		return nil
	}
	if err := validatePresets(list); err != nil {
		return err
	}
	// 拷贝，避免调用方后续突变。
	stored := make([]Preset, len(list))
	copy(stored, list)
	qualityOverrides.Store(stored)
	return nil
}

// validatePresets 校验档位表（§5）：id 正则、不可与 auto 重名、
// original 必须存在且保持 0 值固定、width>=0、bitrate>=0。
func validatePresets(list []Preset) error {
	seen := make(map[string]bool, len(list))
	hasOriginal := false
	for i := range list {
		p := &list[i]
		p.Name = trimSpace(p.Name)
		if p.Name == "" {
			return fmt.Errorf("quality preset #%d has empty id", i)
		}
		if !idPattern.MatchString(p.Name) {
			return fmt.Errorf("quality preset id %q is invalid (only letters/digits/_/- allowed)", p.Name)
		}
		if p.Name == PresetIDAuto {
			return fmt.Errorf("quality preset id %q is reserved", PresetIDAuto)
		}
		if seen[p.Name] {
			return fmt.Errorf("duplicate quality preset id %q", p.Name)
		}
		seen[p.Name] = true
		if p.Width < 0 || p.BitrateKbps < 0 || p.AudioKbps < 0 || p.CRF < 0 {
			return fmt.Errorf("quality preset %q has negative dimension/bitrate/crf", p.Name)
		}
		if p.Name == PresetIDOriginal {
			hasOriginal = true
			if p.Width != 0 || p.Height != 0 || p.BitrateKbps != 0 || p.AudioKbps != 0 || p.CRF != 0 || !p.Fixed {
				return fmt.Errorf("quality preset %q must remain fixed all-zero", PresetIDOriginal)
			}
		}
	}
	if !hasOriginal {
		return fmt.Errorf("quality preset %q is required and must be fixed all-zero", PresetIDOriginal)
	}
	return nil
}

func trimSpace(s string) string {
	start := 0
	for start < len(s) && (s[start] == ' ' || s[start] == '\t') {
		start++
	}
	end := len(s)
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t' || s[end-1] == '\n' || s[end-1] == '\r') {
		end--
	}
	return s[start:end]
}

// Effective 返回当前生效的共享画质阶梯：有覆盖用覆盖，否则内置默认。
// 返回拷贝，调用方无法突变进程内注册表。
func Effective() []Preset {
	if overrides, ok := qualityOverrides.Load().([]Preset); ok && len(overrides) > 0 {
		out := make([]Preset, len(overrides))
		copy(out, overrides)
		return out
	}
	builtin := defaultQualityLadder()
	out := make([]Preset, len(builtin))
	copy(out, builtin)
	return out
}

// RuntimeEffective 按 id 查找当前生效档位。original 始终返回 0 值档。
// 未找到时返回 (Preset{}, false)。
func RuntimeEffective(id string) (Preset, bool) {
	for _, p := range Effective() {
		if p.Name == id {
			return p, true
		}
	}
	return Preset{}, false
}
