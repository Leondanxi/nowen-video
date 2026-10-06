package ffmpeg

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNumericOriginalSoftwareOmitsScaleAndUsesCRF(t *testing.T) {
	args := BuildRollingHLSArgs(BuildOptions{
		InputPath:            "in.mkv",
		OutputDir:            t.TempDir(),
		HWAccel:              HWAccelNone,
		Profile:              Profile{Width: 0, Height: 0, AudioBitrate: "128k"},
		UseNumericRateControl: true,
		NumericBitrateKbps:    0,
		EffectiveCRF:          18,
		HLSTime:              2,
	}, RollingHLSOptions{ListSize: 30, DeleteThreshold: 10, SegmentPattern: "seg_%06d.ts"})

	require.Contains(t, args, "libx264")
	requireArgPair(t, args, "-crf", "18")
	require.Contains(t, args, "aac")
	require.NotContains(t, args, "-vf") // 原画不缩放
	require.NotContains(t, args, "-b:v")
}

func TestNumericSoftware720pUsesBitrateAndScale(t *testing.T) {
	args := BuildRollingHLSArgs(BuildOptions{
		InputPath:             "in.mkv",
		OutputDir:             t.TempDir(),
		HWAccel:               HWAccelNone,
		Profile:               Profile{Width: 1280, Height: 720, AudioBitrate: "128k"},
		UseNumericRateControl: true,
		NumericBitrateKbps:    1000,
		EffectiveCRF:          18,
		VideoFilter:           "scale=1280:720:force_original_aspect_ratio=decrease,pad=1280:720:(ow-iw)/2:(oh-ih)/2:color=black,setsar=1",
		HLSTime:               2,
	}, RollingHLSOptions{ListSize: 30, DeleteThreshold: 10, SegmentPattern: "seg_%06d.ts"})

	require.Contains(t, args, "libx264")
	requireArgPair(t, args, "-b:v", "1000k")
	requireArgPair(t, args, "-maxrate", "1500k") // 1.5x
	requireArgPair(t, args, "-bufsize", "2000k") // 2x
	require.Contains(t, args, "-vf")
	require.NotContains(t, args, "-crf")
}

func TestNumericNVENC720pUsesScaleCudaAndBitrate(t *testing.T) {
	args := BuildRollingHLSArgs(BuildOptions{
		InputPath:             "in.mkv",
		OutputDir:             t.TempDir(),
		HWAccel:               HWAccelNVENC,
		Profile:               Profile{Width: 1280, Height: 720, AudioBitrate: "128k"},
		UseNumericRateControl: true,
		NumericBitrateKbps:    1000,
		EffectiveCRF:          18,
		HLSTime:               2,
	}, RollingHLSOptions{ListSize: 30, DeleteThreshold: 10, SegmentPattern: "seg_%06d.ts"})

	require.Contains(t, args, "h264_nvenc")
	requireArgPair(t, args, "-b:v", "1000k")
	require.Contains(t, args, "scale_cuda=1280:720:format=nv12")
	require.Contains(t, args, "-hwaccel")
}

func TestNumericNVENCOriginalUsesZeroBitrateAndCQ(t *testing.T) {
	args := BuildRollingHLSArgs(BuildOptions{
		InputPath:             "in.mkv",
		OutputDir:             t.TempDir(),
		HWAccel:               HWAccelNVENC,
		Profile:               Profile{Width: 0, Height: 0, AudioBitrate: "128k"},
		UseNumericRateControl: true,
		NumericBitrateKbps:    0,
		EffectiveCRF:          23, // → CQ 31
		HLSTime:               2,
	}, RollingHLSOptions{ListSize: 30, DeleteThreshold: 10, SegmentPattern: "seg_%06d.ts"})

	require.Contains(t, args, "h264_nvenc")
	requireArgPair(t, args, "-b:v", "0")
	requireArgPair(t, args, "-cq", "31")
	require.NotContains(t, args, "-vf") // 原画不缩放
}

func TestNumericAV1SourceNVENCSkipsHWDecodeAndEmitsNoAV1(t *testing.T) {
	args := BuildRollingHLSArgs(BuildOptions{
		InputPath:             "in.mkv",
		OutputDir:             t.TempDir(),
		HWAccel:               HWAccelNVENC,
		Profile:               Profile{Width: 0, Height: 0, AudioBitrate: "128k"},
		UseNumericRateControl: true,
		NumericBitrateKbps:    0,
		EffectiveCRF:          18,
		SourceIsAV1:           true, // Turing 无 AV1 硬解
		HLSTime:               2,
	}, RollingHLSOptions{ListSize: 30, DeleteThreshold: 10, SegmentPattern: "seg_%06d.ts"})

	// CPU 解码：去掉 -hwaccel cuda / output_format cuda。
	require.NotContains(t, args, "cuda")
	// 仍用 h264_nvenc 编码，绝不生成 AV1 硬参。
	require.Contains(t, args, "h264_nvenc")
	require.NotContains(t, args, "av1_nvenc")
	require.NotContains(t, args, "hevc_nvenc")
}
