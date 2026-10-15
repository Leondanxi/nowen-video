package ffmpeg

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResolveBackendSoftware(t *testing.T) {
	require.Equal(t, HWAccelNone, ResolveBackend("software", "nvenc", "nvenc"))
	require.Equal(t, HWAccelNone, ResolveBackend("software", "auto", "qsv"))
}

func TestResolveBackendHardwareForcedEncoderEvenWhenDetectionNone(t *testing.T) {
	// §6 关键：hardware + 指定 nvenc，即使本地探测为 none（远端 GPU）也强制。
	require.Equal(t, HWAccelNVENC, ResolveBackend("hardware", "nvenc", "none"))
	require.Equal(t, HWAccelQSV, ResolveBackend("hardware", "qsv", "none"))
	require.Equal(t, HWAccelVAAPI, ResolveBackend("hardware", "vaapi", "none"))
	require.Equal(t, HWAccelAMF, ResolveBackend("hardware", "amf", "none"))
}

func TestResolveBackendHardwareUsesDetectedWhenEncoderAuto(t *testing.T) {
	require.Equal(t, HWAccelNVENC, ResolveBackend("hardware", "auto", "nvenc"))
	require.Equal(t, HWAccelNone, ResolveBackend("hardware", "auto", "none"))
}

func TestResolveBackendAuto(t *testing.T) {
	// 探测到 → 用探测。
	require.Equal(t, HWAccelQSV, ResolveBackend("auto", "nvenc", "qsv"))
	// 探测 none → 用配置编码器兜底。
	require.Equal(t, HWAccelNVENC, ResolveBackend("auto", "nvenc", "none"))
	// 探测 none 且编码器 auto → 软件。
	require.Equal(t, HWAccelNone, ResolveBackend("auto", "auto", "none"))
	// 大小写 / 空白容错。
	require.Equal(t, HWAccelNVENC, ResolveBackend(" Auto ", " NVENC ", " none "))
}

func TestCRFToNVENCCQMapping(t *testing.T) {
	// cq = clamp(round(CRF*1.35), 0, 51)（§7 对照表，取公式确定的若干点）。
	require.Equal(t, 22, CRFToNVENCCQ(16))
	require.Equal(t, 24, CRFToNVENCCQ(18))
	require.Equal(t, 27, CRFToNVENCCQ(20))
	require.Equal(t, 31, CRFToNVENCCQ(23))
	require.Equal(t, 35, CRFToNVENCCQ(26))
	// clamp 边界。
	require.Equal(t, 0, CRFToNVENCCQ(-5))
	require.Equal(t, 51, CRFToNVENCCQ(100))
}
