package service

import (
	"testing"

	"github.com/nowen-video/nowen-video/internal/model"
	transcodeprofile "github.com/nowen-video/nowen-video/internal/transcode/profile"
)

func TestNormalizeRuntimeProfile(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"auto", "auto", "auto"},
		{"legacy 360p maps to 640p", "360p", "640p"},
		{"640p stays", "640p", "640p"},
		{"480p stays", "480p", "480p"},
		{"720p stays", "720p", "720p"},
		{"1080p stays", "1080p", "1080p"},
		{"uppercase 2K lowercases to 2k", "2K", "2k"},
		{"uppercase 4K lowercases to 4k", "4K", "4k"},
		{"uppercase 720P lowercases", "720P", "720p"},
		{"unknown id returned as-is", "9999p", "9999p"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizeRuntimeProfile(tc.in); got != tc.want {
				t.Fatalf("normalizeRuntimeProfile(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestDefaultProfileReturnsValidRuntimeRungs(t *testing.T) {
	svc := &PlaybackSessionService{}
	cases := []struct {
		name       string
		resolution string
		want       string
	}{
		{"nil media", "", "720p"},
		{"1080p", "1080p", "1080p"},
		{"720p", "720p", "720p"},
		{"480p", "480p", "480p"},
		{"legacy 360p now resolves to 640p", "360p", "640p"},
		{"unknown resolution falls back to 640p", "", "640p"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var media *model.Media
			if tc.name != "nil media" {
				media = &model.Media{Resolution: tc.resolution}
			}
			got := svc.defaultProfile(media)
			if got != tc.want {
				t.Fatalf("defaultProfile(%q) = %q, want %q", tc.resolution, got, tc.want)
			}
			// 关键回归：默认档位必须是运行时阶梯里真实存在的 id，否则会重现
			// unknown quality preset 导致的 500。
			if _, ok := transcodeprofile.RuntimeEffective(got); !ok {
				t.Fatalf("default profile %q is not a valid runtime preset", got)
			}
		})
	}
}
