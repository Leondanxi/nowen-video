package ffmpeg

import (
	"fmt"
	"math"
	"path/filepath"
	"strconv"
)

// Profile 目标码率/分辨率配置。
// 所有字段都是 FFmpeg 能直接识别的字符串（如 "800k"、"1920"）。
type Profile struct {
	Width        int
	Height       int
	VideoBitrate string
	AudioBitrate string
	// MaxBitrate / BufSize 为空时默认与 VideoBitrate 相等（等价于低延迟 1x buffer）。
	MaxBitrate string
	BufSize    string
}

// BuildOptions 组装 FFmpeg 命令行的所有参数。
// 字段按 "输入 / 编码 / HLS / 其他" 分组，详见注释。
type BuildOptions struct {
	// ---------- 输入/输出 ----------
	InputPath  string   // 必填，源文件路径或 URL
	OutputDir  string   // 必填，输出目录，将在其中生成 stream.m3u8 + segNNNN.ts
	ExtraInput []string // 可选，-i 前额外插入的前置参数（例如 WebDAV 的 -reconnect 等）

	// ---------- 编码器相关 ----------
	HWAccel     string  // "", "none", "nvenc", "qsv", "vaapi"
	Profile     Profile // 目标分辨率/码率
	VAAPIDevice string  // vaapi 模式下的设备路径（如 /dev/dri/renderD128）
	X264Preset  string  // software 模式下 -preset，留空则使用 "medium"
	QSVPreset   string  // qsv 模式下 -preset，留空则使用 "medium"
	Threads     int     // -threads 值；<=0 时省略
	UseCRF      bool    // 软件编码是否使用 CRF 恒定质量（transcode 场景用）；否则用 -b:v 固定码率
	CRF         int     // UseCRF=true 时生效，<=0 默认 23
	// QSVGlobalQuality >0 时 qsv 走 CQP 质量模式（-global_quality 值），忽略 -b:v/-maxrate/-bufsize；
	// <=0 时走码率模式（与预处理行为一致）。
	QSVGlobalQuality int
	SoftwareTune     string // x264 -tune 值（"zerolatency"/""），留空则不加
	NvencTune        string // nvenc -tune 值（"ll"/""），留空则不加
	// QSVAttachOutputFormat 是否在 qsv 模式下显式指定 -hwaccel_output_format qsv。
	// transcode 场景建议 false（允许 FFmpeg 在 QSV 解码失败时回退软件解码），
	// 预处理场景可为 true（全 GPU 管线，性能更高）。
	QSVAttachOutputFormat bool
	// VideoFilter 完整的 -vf 值。**若非空则直接使用**（例如 HDR tonemap 链），
	// 为空时会按 HWAccel 自动生成 scale 滤镜。
	VideoFilter string

	// ---------- HLS 输出 ----------
	HLSTime         int    // 每片秒数；<=0 默认 4
	HLSFlags        string // 完整 -hls_flags 值；为空时使用 "independent_segments"
	HLSPlaylistType string // event / vod / ""(不设置)
	StartNumber     int    // <=0 时不设置
	ForceKeyFrames  bool   // 是否加 "-force_key_frames expr:gte(t,n_forced*HLSTime)"
	// GOPSize -g 与 -keyint_min 的值；<=0 时默认按 HLSTime*25 估算
	GOPSize int

	// ---------- 输入 seek（仅 transcode 使用）----------
	StartOffsetSec float64 // >0.5 时在 -i 前插入 -ss

	// SkipVAAPIRateLimits VAAPI 分支是否省略 -maxrate/-bufsize/-keyint_min。
	// 仅用于与历史 transcode 实现保持字节一致；新场景不建议开启。
	SkipVAAPIRateLimits bool

	// ---------- 新版码率/质量控制（冻结规格 §7，仅实时转码使用） ----------
	// UseNumericRateControl=true 时启用下面三个字段描述的数值码率/CRF 流程，
	// 忽略 legacy 的 Profile.VideoBitrate 字符串与 UseCRF；
	// false 时行为与历史完全一致（preprocess / certification 命令不受影响）。
	UseNumericRateControl bool
	// NumericBitrateKbps >0 = 码率档（KBPS）；==0 = 恒定质量（CRF/CQ/CQP）。
	NumericBitrateKbps int
	// EffectiveCRF 是每档 CRF（>0）优先，否则全局 transcode_crf。质量模式使用。
	EffectiveCRF int
	// SourceIsAV1 标记源视频编码为 AV1。Turing NVENC/NVDEC 无 AV1 硬编解码：
	// 此时去掉硬件解码预参（CPU 解码 + GPU 编码），且绝不生成 AV1 硬参。
	SourceIsAV1 bool
}

// DefaultTranscodeCRF 是软件 x264 的全局恒定质量基准（§3.1 transcode_crf 默认 18）。
const DefaultTranscodeCRF = 18

// CRFToNVENCCQ 把 x264 CRF 尺度映射到 NVENC CQ 尺度（冻结规格 §7）。
// cq = clamp(round(CRF*1.35), 0, 51)。
func CRFToNVENCCQ(crf int) int {
	cq := int(math.Round(float64(crf) * 1.35))
	if cq < 0 {
		return 0
	}
	if cq > 51 {
		return 51
	}
	return cq
}

// kbpsArg 把 KBPS 整数转成 ffmpeg 的 "NNNNk" 码率参数。
func kbpsArg(kbps int) string {
	return strconv.Itoa(kbps) + "k"
}

// BuildHLSArgs 根据 opts 构建完整的 FFmpeg 参数列表（不含 ffmpeg 二进制路径）。
//
// 返回的参数顺序与历史 transcode.go / preprocess.go / abr.go 的实现保持一致，
// 以便回归测试时可以做字节级对比。
func BuildHLSArgs(opts BuildOptions) []string {
	hlsTime := opts.HLSTime
	if hlsTime <= 0 {
		hlsTime = 4
	}
	gop := opts.GOPSize
	if gop <= 0 {
		gop = hlsTime * 25
	}
	gopStr := strconv.Itoa(gop)

	outputPath := filepath.Join(opts.OutputDir, "stream.m3u8")
	segmentPath := filepath.Join(opts.OutputDir, "seg%04d.ts")

	// -------- baseArgs（-y + 可选 -ss + 硬件加速前置 + -i） --------
	baseArgs := []string{"-y"}
	if opts.StartOffsetSec > 0.5 {
		baseArgs = append(baseArgs, "-ss", fmt.Sprintf("%.2f", opts.StartOffsetSec))
	}
	baseArgs = append(baseArgs, opts.ExtraInput...)

	// 硬件加速前置参数。AV1 源在 Turing 等无 AV1 硬解的 GPU 上无效，
	// 此时去掉硬件解码预参（CPU 解码 + GPU 编码），见 §7 AV1 边界。
	hwDecode := opts.HWAccel != HWAccelNone && !opts.SourceIsAV1
	if hwDecode {
		switch opts.HWAccel {
		case HWAccelNVENC:
			baseArgs = append(baseArgs, "-hwaccel", "cuda", "-hwaccel_output_format", "cuda")
		case HWAccelQSV:
			baseArgs = append(baseArgs, "-hwaccel", "qsv")
			if opts.QSVAttachOutputFormat {
				baseArgs = append(baseArgs, "-hwaccel_output_format", "qsv")
			}
		case HWAccelVAAPI:
			dev := opts.VAAPIDevice
			baseArgs = append(baseArgs,
				"-hwaccel", "vaapi",
				"-hwaccel_output_format", "vaapi",
				"-vaapi_device", dev,
			)
		case HWAccelAMF:
			baseArgs = append(baseArgs, "-hwaccel", "d3d11va")
		}
	}

	baseArgs = append(baseArgs, "-i", opts.InputPath)

	// -------- videoArgs --------
	videoArgs := buildVideoArgs(opts, gopStr)

	// -------- audioArgs --------
	audioArgs := []string{"-c:a", "aac", "-b:a", opts.Profile.AudioBitrate, "-ac", "2"}

	// -------- hlsArgs --------
	hlsFlags := opts.HLSFlags
	if hlsFlags == "" {
		hlsFlags = "independent_segments"
	}
	hlsArgs := []string{
		"-f", "hls",
		"-hls_time", strconv.Itoa(hlsTime),
		"-hls_list_size", "0",
		"-hls_segment_filename", segmentPath,
		"-hls_flags", hlsFlags,
	}
	if opts.HLSPlaylistType != "" {
		hlsArgs = append(hlsArgs, "-hls_playlist_type", opts.HLSPlaylistType)
	}
	if opts.StartNumber > 0 {
		hlsArgs = append(hlsArgs, "-start_number", strconv.Itoa(opts.StartNumber))
	}
	if opts.ForceKeyFrames {
		hlsArgs = append(hlsArgs, "-force_key_frames", fmt.Sprintf("expr:gte(t,n_forced*%d)", hlsTime))
	}
	hlsArgs = append(hlsArgs, outputPath)

	// -------- 组装 --------
	args := baseArgs
	if opts.Threads > 0 {
		args = append(args, "-threads", strconv.Itoa(opts.Threads))
	}
	args = append(args, videoArgs...)
	args = append(args, audioArgs...)
	args = append(args, hlsArgs...)
	return args
}

// buildVideoArgs 按 HWAccel 分支生成视频编码参数。
func buildVideoArgs(opts BuildOptions, gopStr string) []string {
	if opts.UseNumericRateControl {
		return buildNumericVideoArgs(opts, gopStr)
	}

	p := opts.Profile
	maxRate := p.MaxBitrate
	if maxRate == "" {
		maxRate = p.VideoBitrate
	}
	bufSize := p.BufSize
	if bufSize == "" {
		bufSize = p.VideoBitrate
	}

	// 未显式指定 VideoFilter 时，按 HWAccel 自动生成 scale
	scale := opts.VideoFilter

	switch opts.HWAccel {
	case HWAccelNVENC:
		if scale == "" {
			scale = fmt.Sprintf("scale_cuda=%d:%d:format=nv12", p.Width, p.Height)
		}
		args := []string{
			"-c:v", "h264_nvenc",
			"-preset", "p4",
		}
		if opts.NvencTune != "" {
			args = append(args, "-tune", opts.NvencTune)
		}
		args = append(args,
			"-b:v", p.VideoBitrate,
			"-maxrate", maxRate,
			"-bufsize", bufSize,
			"-g", gopStr,
			"-keyint_min", gopStr,
			"-sc_threshold", "0",
			"-vf", scale,
		)
		return args

	case HWAccelQSV:
		preset := opts.QSVPreset
		if preset == "" {
			// 【火力全开】默认 preset 由 medium 改为 faster，
			// QSV 硬编硬件开销固定，preset 对速度影响相对有限，
			// 但 faster 还是能提速 20~40%，画质损失几乎无感。
			preset = "faster"
		}
		if scale == "" {
			scale = fmt.Sprintf("scale_qsv=%d:%d", p.Width, p.Height)
		}
		if opts.QSVGlobalQuality > 0 {
			// CQP 质量模式：对应 transcode 实时场景，不锁码率、以恒定质量编码。
			return []string{
				"-c:v", "h264_qsv",
				"-preset", preset,
				"-global_quality", strconv.Itoa(opts.QSVGlobalQuality),
				"-g", gopStr,
				"-pix_fmt", "nv12",
				"-vf", scale,
			}
		}
		return []string{
			"-c:v", "h264_qsv",
			"-preset", preset,
			"-b:v", p.VideoBitrate,
			"-maxrate", maxRate,
			"-bufsize", bufSize,
			"-pix_fmt", "yuv420p",
			"-vf", scale,
			"-g", gopStr,
			"-keyint_min", gopStr,
		}

	case HWAccelVAAPI:
		if scale == "" {
			scale = fmt.Sprintf("scale_vaapi=w=%d:h=%d", p.Width, p.Height)
		}
		if opts.SkipVAAPIRateLimits {
			return []string{
				"-c:v", "h264_vaapi",
				"-b:v", p.VideoBitrate,
				"-g", gopStr,
				"-pix_fmt", "yuv420p",
				"-vf", scale,
			}
		}
		return []string{
			"-c:v", "h264_vaapi",
			"-b:v", p.VideoBitrate,
			"-maxrate", maxRate,
			"-bufsize", bufSize,
			"-pix_fmt", "yuv420p",
			"-vf", scale,
			"-g", gopStr,
			"-keyint_min", gopStr,
		}

	default:
		// 软件编码 libx264
		preset := opts.X264Preset
		if preset == "" {
			// 【火力全开】默认 preset 由 medium 改为 veryfast：
			//   - 速度提升 ~2-3x（vs medium），严重 CPU 编码场景收益最大
			//   - 相同码率下码率效率略低（文件略大 5~10%），
			//     但 NAS 场景磁盘充裕，速度更重要
			//   - PSNR / 主观画质差别胉眼难辨
			preset = "veryfast"
		}
		if scale == "" {
			scale = fmt.Sprintf("scale=%d:%d", p.Width, p.Height)
		}
		args := []string{
			"-c:v", "libx264",
			"-preset", preset,
		}
		if opts.SoftwareTune != "" {
			args = append(args, "-tune", opts.SoftwareTune)
		}
		if opts.UseCRF {
			crf := opts.CRF
			if crf <= 0 {
				crf = 23
			}
			args = append(args, "-crf", strconv.Itoa(crf))
		} else {
			args = append(args,
				"-b:v", p.VideoBitrate,
				"-maxrate", maxRate,
				"-bufsize", bufSize,
			)
		}
		args = append(args,
			"-g", gopStr,
			"-keyint_min", gopStr,
			"-sc_threshold", "0",
			"-pix_fmt", "yuv420p",
			"-vf", scale,
		)
		return args
	}
}

// buildNumericVideoArgs 按"后端 + 档位"生成实时转码视频参数（冻结规格 §7）。
//
//	缩放：Width==0（原画）不加 scale；Width>0 按后端选择对应 scale 滤镜。
//	码率：BitrateKbps>0 → -b:v/-maxrate(1.5x)/-bufsize(2x)；==0 → CRF/CQ/CQP 恒定质量。
//	AV1 源：由调用方（buildArgs）在源 codec 为 AV1 且硬件后端时设置 SourceIsAV1，
//	该路径已在 BuildHLSArgs 中去掉硬件解码预参；这里绝不生成 AV1 编码器。
func buildNumericVideoArgs(opts BuildOptions, gopStr string) []string {
	p := opts.Profile
	bitrate := opts.NumericBitrateKbps
	crf := opts.EffectiveCRF
	if crf <= 0 {
		crf = DefaultTranscodeCRF
	}
	scale := opts.VideoFilter
	// Width==0（原画）且未显式指定滤镜 → 不缩放。
	skipScale := p.Width <= 0 && scale == ""

	appendVF := func(args []string) []string {
		if skipScale {
			return args
		}
		if scale == "" {
			switch opts.HWAccel {
			case HWAccelNVENC:
				scale = fmt.Sprintf("scale_cuda=%d:%d:format=nv12", p.Width, p.Height)
			case HWAccelQSV:
				scale = fmt.Sprintf("scale_qsv=%d:%d", p.Width, p.Height)
			case HWAccelVAAPI:
				scale = fmt.Sprintf("scale_vaapi=w=%d:h=%d:format=nv12", p.Width, p.Height)
			case HWAccelAMF:
				scale = fmt.Sprintf("scale=%d:%d:flags=lanczos", p.Width, p.Height)
			default:
				scale = fmt.Sprintf("scale=%d:%d", p.Width, p.Height)
			}
		}
		return append(args, "-vf", scale)
	}

	switch opts.HWAccel {
	case HWAccelNVENC:
		args := []string{"-c:v", "h264_nvenc", "-preset", "p5", "-rc", "vbr", "-spatial-aq", "1"}
		if bitrate > 0 {
			args = append(args,
				"-b:v", kbpsArg(bitrate),
				"-maxrate", kbpsArg(bitrate*3/2),
				"-bufsize", kbpsArg(bitrate*2),
			)
		} else {
			// 恒定质量：-b:v 0 + NVENC CQ（CRF→CQ 映射）。
			args = append(args, "-b:v", "0", "-cq", strconv.Itoa(CRFToNVENCCQ(crf)))
		}
		args = appendVF(args)
		return append(args,
			"-g", gopStr,
			"-keyint_min", gopStr,
			"-sc_threshold", "0",
		)

	case HWAccelQSV:
		preset := opts.QSVPreset
		if preset == "" {
			preset = "faster"
		}
		args := []string{"-c:v", "h264_qsv", "-preset", preset}
		if bitrate > 0 {
			args = append(args,
				"-b:v", kbpsArg(bitrate),
				"-maxrate", kbpsArg(bitrate*3/2),
				"-bufsize", kbpsArg(bitrate*2),
			)
		} else {
			args = append(args, "-global_quality", strconv.Itoa(crf))
		}
		args = appendVF(args)
		return append(args,
			"-pix_fmt", "nv12",
			"-g", gopStr,
			"-keyint_min", gopStr,
		)

	case HWAccelVAAPI:
		args := []string{"-c:v", "h264_vaapi"}
		if bitrate > 0 {
			args = append(args,
				"-b:v", kbpsArg(bitrate),
				"-maxrate", kbpsArg(bitrate*3/2),
				"-bufsize", kbpsArg(bitrate*2),
			)
		} else {
			args = append(args, "-rc_mode", "CQP", "-qp", strconv.Itoa(crf))
		}
		args = appendVF(args)
		return append(args,
			"-g", gopStr,
			"-keyint_min", gopStr,
		)

	case HWAccelAMF:
		args := []string{"-c:v", "h264_amf", "-quality", "quality"}
		if bitrate > 0 {
			args = append(args, "-rc", "cbr", "-b:v", kbpsArg(bitrate))
		} else {
			args = append(args,
				"-rc", "cqp",
				"-qp_i", strconv.Itoa(crf),
				"-qp_p", strconv.Itoa(crf),
				"-qp_b", strconv.Itoa(crf),
			)
		}
		args = appendVF(args)
		return append(args,
			"-g", gopStr,
			"-keyint_min", gopStr,
		)

	default:
		// 软件 libx264。
		preset := opts.X264Preset
		if preset == "" {
			preset = "veryfast"
		}
		args := []string{"-c:v", "libx264", "-preset", preset}
		if opts.SoftwareTune != "" {
			args = append(args, "-tune", opts.SoftwareTune)
		}
		if bitrate > 0 {
			args = append(args,
				"-b:v", kbpsArg(bitrate),
				"-maxrate", kbpsArg(bitrate*3/2),
				"-bufsize", kbpsArg(bitrate*2),
			)
		} else {
			args = append(args, "-crf", strconv.Itoa(crf))
		}
		args = appendVF(args)
		return append(args,
			"-g", gopStr,
			"-keyint_min", gopStr,
			"-sc_threshold", "0",
			"-pix_fmt", "yuv420p",
		)
	}
}
