package handler

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/nowen-video/nowen-video/internal/service"
	transcodeprofile "github.com/nowen-video/nowen-video/internal/transcode/profile"
	"go.uber.org/zap"
)

type PlaybackPlanHandler struct {
	stream *service.StreamService
	logger *zap.SugaredLogger
}

type PlannedMediaPlayInfo struct {
	*service.MediaPlayInfo
	PlaybackPlan *service.PlaybackPlan `json:"playback_plan"`
}

func NewPlaybackPlanHandler(stream *service.StreamService, logger *zap.SugaredLogger) *PlaybackPlanHandler {
	return &PlaybackPlanHandler{stream: stream, logger: logger}
}

// GetInfo is the canonical Lite playback-info entry point. It keeps all legacy
// fields while embedding the server-side playback decision, so clients no
// longer need a second /plan round trip.
func (h *PlaybackPlanHandler) GetInfo(c *gin.Context) {
	mediaID := c.Param("id")
	if mediaID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing id"})
		return
	}

	info, err := h.stream.GetMediaPlayInfo(mediaID)
	if err != nil {
		c.JSON(playbackPlanErrorStatus(err), gin.H{"error": err.Error()})
		return
	}
	caps := h.clientCapabilities(c)
	plan, err := h.stream.PlanPlaybackWithInfoAuthoritative(mediaID, info, caps)
	if err != nil {
		if h.logger != nil {
			h.logger.Warnf("生成播放规划失败 media_id=%s: %v", mediaID, err)
		}
		c.JSON(playbackPlanErrorStatus(err), gin.H{"error": err.Error()})
		return
	}
	h.logPlaybackPlan(mediaID, info, plan, caps)

	c.JSON(http.StatusOK, gin.H{"data": PlannedMediaPlayInfo{
		MediaPlayInfo: info,
		PlaybackPlan:  plan,
	}})
}

// QualityPresets 返回播放器画质菜单（§11.1）：{data:{default, presets}}。
// presets 首项为虚拟"自动"档（id=auto，不是 preset 表中的一行）。
func (h *PlaybackPlanHandler) QualityPresets(c *gin.Context) {
	type presetItem struct {
		ID           string `json:"id"`
		Name         string `json:"name"`
		Width        int    `json:"width"`
		Height       int    `json:"height"`
		Bitrate      int    `json:"bitrate"`
		AudioBitrate int    `json:"audio_bitrate"`
		CRF          int    `json:"crf"`
		Fixed        bool   `json:"fixed"`
	}
	// 仅返回真实档位（original..640p）；虚拟「自动(auto)」档由前端置顶，
	// 不在此列出（§11.1），否则会与前端自动项重复并污染自动 ABR 阶梯。
	presets := []presetItem{}
	for _, p := range transcodeprofile.Effective() {
		name := p.DisplayName
		if name == "" {
			name = p.Name
		}
		presets = append(presets, presetItem{
			ID:           p.Name,
			Name:         name,
			Width:        p.Width,
			Height:       p.Height,
			Bitrate:      p.BitrateKbps,
			AudioBitrate: p.AudioKbps,
			CRF:          p.CRF,
			Fixed:        p.Fixed,
		})
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{
		"default": h.stream.DefaultQualityPreset(),
		"presets": presets,
	}})
}

// Get remains available for diagnostics and clients that only need a plan.
func (h *PlaybackPlanHandler) Get(c *gin.Context) {
	mediaID := c.Param("id")
	if mediaID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing id"})
		return
	}

	caps := h.clientCapabilities(c)
	plan, err := h.stream.PlanPlaybackAuthoritative(mediaID, caps)
	if err != nil {
		if h.logger != nil {
			h.logger.Warnf("生成播放规划失败 media_id=%s: %v", mediaID, err)
		}
		c.JSON(playbackPlanErrorStatus(err), gin.H{"error": err.Error()})
		return
	}
	h.logPlaybackPlan(mediaID, nil, plan, caps)
	c.JSON(http.StatusOK, gin.H{"data": plan})
}

func (h *PlaybackPlanHandler) logPlaybackPlan(mediaID string, info *service.MediaPlayInfo, plan *service.PlaybackPlan, caps service.PlaybackClientCapabilities) {
	if h.logger == nil || plan == nil {
		return
	}
	fields := []interface{}{
		"media_id", mediaID,
		"method", plan.Method,
		"reason_code", plan.ReasonCode,
		"session_required", plan.SessionRequired,
		"platform", caps.Platform,
		"supports_hevc", caps.SupportsHEVC,
		"probe_verified", plan.SourceTechnical != nil,
	}
	if info != nil {
		fields = append(fields,
			"file_ext", info.FileExt,
			"video_codec", info.VideoCodec,
			"audio_codec", info.AudioCodec,
			"can_direct", info.CanDirectPlay,
			"can_remux", info.CanRemux,
		)
	}
	h.logger.Infow("playback plan selected", fields...)
}

func playbackPlanErrorStatus(err error) int {
	if errors.Is(err, service.ErrMediaNotFound) {
		return http.StatusNotFound
	}
	return http.StatusInternalServerError
}

func (h *PlaybackPlanHandler) clientCapabilities(c *gin.Context) service.PlaybackClientCapabilities {
	caps := h.stream.DefaultPlaybackClientCapabilities(c.GetHeader("User-Agent"))
	caps.SupportsDirectPlay = queryBool(c, "supports_direct", caps.SupportsDirectPlay)
	caps.SupportsRemux = queryBool(c, "supports_remux", caps.SupportsRemux)
	caps.SupportsHEVC = queryBool(c, "supports_hevc", caps.SupportsHEVC)
	caps.ForceTranscode = queryBool(c, "force_transcode", false)
	caps.MaxBitrate = queryPositiveInt(c, "max_bitrate")

	// 扩展精确能力参数（来自前端 media-capabilities 探测）
	caps.HEVCHardware = queryBool(c, "hevc_hardware", false)
	caps.AudioSupportsAC3 = queryBool(c, "audio_supports_ac3", false)
	caps.AudioSupportsEAC3 = queryBool(c, "audio_supports_eac3", false)
	caps.AudioSupportsFLAC = queryBool(c, "audio_supports_flac", false)
	caps.AudioSupportsOpus = queryBool(c, "audio_supports_opus", false)
	caps.ContainerSupportsMP4 = queryBool(c, "container_supports_mp4", true)
	caps.ContainerSupportsWebM = queryBool(c, "container_supports_webm", false)
	caps.MSEH264 = queryBool(c, "mse_h264", false)
	caps.MSEHEVC = queryBool(c, "mse_hevc", false)
	caps.Platform = strings.TrimSpace(c.Query("platform"))
	caps.Quality = strings.TrimSpace(c.Query("quality"))

	return caps
}

func queryBool(c *gin.Context, key string, defaultValue bool) bool {
	value := strings.TrimSpace(c.Query(key))
	if value == "" {
		return defaultValue
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return defaultValue
	}
	return parsed
}

func queryPositiveInt(c *gin.Context, key string) int {
	value := strings.TrimSpace(c.Query(key))
	if value == "" {
		return 0
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return 0
	}
	return parsed
}
