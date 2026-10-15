package profile

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func resetOverrides(t *testing.T) {
	t.Helper()
	require.NoError(t, SetOverrides(nil))
	t.Cleanup(func() { _ = SetOverrides(nil) })
}

func TestDefaultQualityOrderAndFixed(t *testing.T) {
	resetOverrides(t)
	got := Effective()
	require.Len(t, got, 6)
	ids := []string{}
	for _, p := range got {
		ids = append(ids, p.Name)
	}
	require.Equal(t, []string{"original", "4k", "2k", "1080p", "720p", "640p"}, ids)
}

func TestOriginalPresetIsFixedZero(t *testing.T) {
	resetOverrides(t)
	p, ok := RuntimeEffective("original")
	require.True(t, ok)
	require.True(t, p.Fixed)
	require.Equal(t, 0, p.Width)
	require.Equal(t, 0, p.Height)
	require.Equal(t, 0, p.BitrateKbps)
	require.Equal(t, 0, p.AudioKbps)
	require.Equal(t, 0, p.CRF)
}

func TestRuntimeEffectiveUnknown(t *testing.T) {
	resetOverrides(t)
	_, ok := RuntimeEffective("definitely-not-a-preset")
	require.False(t, ok)
}

func TestHeightDerivation(t *testing.T) {
	require.Equal(t, 360, DeriveHeight(640))
	require.Equal(t, 720, DeriveHeight(1280))
	require.Equal(t, 1080, DeriveHeight(1920))
	require.Equal(t, 1440, DeriveHeight(2560))
	require.Equal(t, 2160, DeriveHeight(3840))
	require.Equal(t, 0, DeriveHeight(0))
	require.Equal(t, 0, DeriveHeight(-100))
}

func TestSetOverridesAtomically(t *testing.T) {
	resetOverrides(t)
	custom := []Preset{
		{Name: "original", Fixed: true},
		{Name: "1080p", DisplayName: "1080P", Width: 1920, Height: 1080, BitrateKbps: 2500, AudioKbps: 192, CRF: 20},
		{Name: "480p", DisplayName: "480P", Width: 854, Height: 480, BitrateKbps: 800, AudioKbps: 96},
	}
	require.NoError(t, SetOverrides(custom))
	got := Effective()
	require.Len(t, got, 3)
	require.Equal(t, "1080p", got[1].Name)
	require.Equal(t, 2500, got[1].BitrateKbps)

	// RuntimeEffective 命中覆盖值。
	p, ok := RuntimeEffective("480p")
	require.True(t, ok)
	require.Equal(t, 854, p.Width)

	// 清空覆盖 → 回退内置默认。
	require.NoError(t, SetOverrides(nil))
	require.Len(t, Effective(), 6)
}

func TestSetOverridesValidationRejectsWhole(t *testing.T) {
	resetOverrides(t)
	// id 含非法字符。
	require.Error(t, SetOverrides([]Preset{
		{Name: "original", Fixed: true},
		{Name: "bad id!", Width: 640},
	}))
	// 与 auto 重名。
	require.Error(t, SetOverrides([]Preset{
		{Name: "original", Fixed: true},
		{Name: "auto", Width: 640},
	}))
	// 缺 original。
	require.Error(t, SetOverrides([]Preset{{Name: "1080p", Width: 1920}}))
	// original 不是 0 值固定。
	require.Error(t, SetOverrides([]Preset{
		{Name: "original", Width: 1920},
	}))
	// 负码率。
	require.Error(t, SetOverrides([]Preset{
		{Name: "original", Fixed: true},
		{Name: "1080p", Width: 1920, BitrateKbps: -5},
	}))
	// 校验失败不污染生效表（仍为内置默认）。
	require.Len(t, Effective(), 6)
}
