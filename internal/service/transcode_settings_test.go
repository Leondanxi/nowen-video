package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResolveFFmpegPathEnvOverride(t *testing.T) {
	t.Setenv(EnvOverrideFFmpegPath, "/opt/remote/ffmpeg")
	require.Equal(t, "/opt/remote/ffmpeg", ResolveFFmpegPath(nil, nil))
}

func TestResolveFFmpegPathDefault(t *testing.T) {
	t.Setenv(EnvOverrideFFmpegPath, "")
	require.Equal(t, "ffmpeg", ResolveFFmpegPath(nil, nil))
}

func TestResolveFFprobePathEnvOverride(t *testing.T) {
	t.Setenv(EnvOverrideFFprobePath, "/usr/local/bin/ffprobe")
	require.Equal(t, "/usr/local/bin/ffprobe", ResolveFFprobePath(nil, nil))
}

func TestResolveHWModeEnvOverride(t *testing.T) {
	t.Setenv(EnvOverrideHWDecodeMode, "hardware")
	t.Setenv(EnvOverrideHWEncoder, "nvenc")
	require.Equal(t, "hardware", resolveHWDecodeMode(nil))
	require.Equal(t, "nvenc", resolveHWEncoder(nil))
}

func TestResolveHWModeDefaultAuto(t *testing.T) {
	t.Setenv(EnvOverrideHWDecodeMode, "")
	t.Setenv(EnvOverrideHWEncoder, "")
	require.Equal(t, "auto", resolveHWDecodeMode(nil))
	require.Equal(t, "auto", resolveHWEncoder(nil))
}
