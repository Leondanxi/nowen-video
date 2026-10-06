package transcode

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	playbacksession "github.com/nowen-video/nowen-video/internal/playback/session"
	"github.com/nowen-video/nowen-video/internal/service/ffmpeg"
	transcodeexecutor "github.com/nowen-video/nowen-video/internal/transcode/executor"
	transcodegovernor "github.com/nowen-video/nowen-video/internal/transcode/governor"
	transcodeprofile "github.com/nowen-video/nowen-video/internal/transcode/profile"
	transcoderuntime "github.com/nowen-video/nowen-video/internal/transcode/runtime"
	"go.uber.org/zap"
)

const (
	defaultSegmentDuration     = 2
	defaultPlaylistWindow      = 30
	defaultDeleteThreshold     = 10
	defaultFirstSegmentTimeout = 12 * time.Second
	defaultHardwareStartBudget = 4 * time.Second
	defaultFirstSegmentPoll    = 50 * time.Millisecond
	defaultX264Preset          = "veryfast"
	defaultQSVPreset           = "faster"
)

type Config struct {
	FFmpegPath  string
	HWAccel     string
	VAAPIDevice string
	Threads     int

	SegmentDuration     int
	PlaylistWindow      int
	DeleteThreshold     int
	FirstSegmentTimeout time.Duration
	HardwareStartBudget time.Duration
	FirstSegmentPoll    time.Duration

	X264Preset string
	QSVPreset  string

	// TranscodeCRF 是全局 x264 CRF / 恒定质量基准（§3.1 transcode_crf，默认 18）。
	// 每档 Preset.CRF>0 时优先用档位值。
	TranscodeCRF int
}

func DefaultConfig(ffmpegPath, hwAccel, vaapiDevice string, threads int) Config {
	return Config{
		FFmpegPath:          ffmpegPath,
		HWAccel:             hwAccel,
		VAAPIDevice:         vaapiDevice,
		Threads:             threads,
		SegmentDuration:     defaultSegmentDuration,
		PlaylistWindow:      defaultPlaylistWindow,
		DeleteThreshold:     defaultDeleteThreshold,
		FirstSegmentTimeout: defaultFirstSegmentTimeout,
		HardwareStartBudget: defaultHardwareStartBudget,
		FirstSegmentPoll:    defaultFirstSegmentPoll,
		X264Preset:          defaultX264Preset,
		QSVPreset:           defaultQSVPreset,
	}
}

type Runtime interface {
	Run(context.Context, transcodegovernor.Kind, transcodeexecutor.Command, transcodeexecutor.Callbacks) transcodeexecutor.Result
}

var _ Runtime = (*transcoderuntime.Runtime)(nil)

type Runner struct {
	sessions *playbacksession.Manager
	runtime  Runtime
	cfg      Config
	logger   *zap.SugaredLogger
}

type StartRequest struct {
	SessionID       string
	GenerationID    uint64
	InputPath       string
	ExtraInput      []string
	ProfileID       string
	StartPositionMS int64
	FPS             float64
	Backend         string
	VideoFilter     string
	// DisableFallback=true 时（显式强制硬件且 gpu_fallback_cpu=false）：
	// 只尝试 preferred 后端，启动失败不静默回退 CPU，而是返回明确错误（§8）。
	DisableFallback bool
	// SourceIsAV1 标记源视频编码为 AV1；硬件后端下去掉硬解预参、绝不生成 AV1 硬参（§7）。
	SourceIsAV1 bool
}

type ReadyResult struct {
	Session    playbacksession.SessionSnapshot
	Generation playbacksession.GenerationSnapshot
	StartupMS  int64
	Err        error
}

type Execution struct {
	ready     chan ReadyResult
	done      chan transcodeexecutor.Result
	readyOnce sync.Once
	doneOnce  sync.Once
}

func (e *Execution) Ready() <-chan ReadyResult             { return e.ready }
func (e *Execution) Done() <-chan transcodeexecutor.Result { return e.done }

func (e *Execution) WaitReady(ctx context.Context) (ReadyResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case result, ok := <-e.ready:
		if !ok {
			return ReadyResult{}, errors.New("playback readiness result is unavailable")
		}
		return result, result.Err
	case <-ctx.Done():
		return ReadyResult{}, ctx.Err()
	}
}

func NewRunner(sessions *playbacksession.Manager, runtime Runtime, cfg Config, logger *zap.SugaredLogger) (*Runner, error) {
	if sessions == nil {
		return nil, fmt.Errorf("playback session manager is required")
	}
	if runtime == nil {
		return nil, fmt.Errorf("media execution runtime is required")
	}
	if logger == nil {
		logger = zap.NewNop().Sugar()
	}
	if cfg.FFmpegPath == "" {
		cfg.FFmpegPath = "ffmpeg"
	}
	if cfg.SegmentDuration <= 0 {
		cfg.SegmentDuration = defaultSegmentDuration
	}
	if cfg.PlaylistWindow <= 0 {
		cfg.PlaylistWindow = defaultPlaylistWindow
	}
	if cfg.DeleteThreshold <= 0 {
		cfg.DeleteThreshold = defaultDeleteThreshold
	}
	if cfg.FirstSegmentTimeout <= 0 {
		cfg.FirstSegmentTimeout = defaultFirstSegmentTimeout
	}
	if cfg.HardwareStartBudget <= 0 {
		cfg.HardwareStartBudget = defaultHardwareStartBudget
	}
	if cfg.FirstSegmentPoll <= 0 {
		cfg.FirstSegmentPoll = defaultFirstSegmentPoll
	}
	if cfg.X264Preset == "" {
		cfg.X264Preset = defaultX264Preset
	}
	if cfg.QSVPreset == "" {
		cfg.QSVPreset = defaultQSVPreset
	}
	if err := ffmpeg.ValidateRollingHLSOptions(ffmpeg.RollingHLSOptions{
		ListSize:        cfg.PlaylistWindow,
		DeleteThreshold: cfg.DeleteThreshold,
	}); err != nil {
		return nil, err
	}
	return &Runner{sessions: sessions, runtime: runtime, cfg: cfg, logger: logger}, nil
}

func (r *Runner) Start(ctx context.Context, request StartRequest) (*Execution, error) {
	if ctx != nil {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
	}
	if strings.TrimSpace(request.SessionID) == "" || request.GenerationID == 0 {
		return nil, fmt.Errorf("session id and generation id are required")
	}
	if strings.TrimSpace(request.InputPath) == "" {
		return nil, fmt.Errorf("transcode input path is required")
	}

	runtimeView, err := r.sessions.Runtime(request.SessionID, request.GenerationID)
	if err != nil {
		return nil, err
	}
	if request.ProfileID == "" || request.ProfileID == transcodeprofile.PresetIDAuto {
		// 自动档为虚拟模式（§2）：实时转码从"原画"起播，由 ABR 客户端降档。
		request.ProfileID = transcodeprofile.PresetIDOriginal
	}
	if request.StartPositionMS <= 0 {
		request.StartPositionMS = runtimeView.Snapshot.StartPositionMS
	}
	if _, ok := transcodeprofile.RuntimeEffective(request.ProfileID); !ok {
		return nil, fmt.Errorf("unknown quality preset %q", request.ProfileID)
	}
	processLease, err := r.sessions.AcquireProcess(request.SessionID, request.GenerationID)
	if err != nil {
		return nil, err
	}

	execution := &Execution{
		ready: make(chan ReadyResult, 1),
		done:  make(chan transcodeexecutor.Result, 1),
	}
	go func() {
		defer processLease.Release()
		r.run(runtimeView, request, execution)
	}()
	return execution, nil
}

func (r *Runner) run(runtimeView playbacksession.GenerationRuntime, request StartRequest, execution *Execution) {
	startedAt := time.Now()
	deadline := startedAt.Add(r.cfg.FirstSegmentTimeout)
	preferredBackend := request.Backend
	if preferredBackend == "" {
		preferredBackend = r.cfg.HWAccel
	}
	if preferredBackend == "" {
		preferredBackend = ffmpeg.HWAccelNone
	}
	backends := []string{preferredBackend}
	// §8：显式强制硬件（DisableFallback）时不追加 CPU 回退，失败直接报错。
	if preferredBackend != ffmpeg.HWAccelNone && !request.DisableFallback {
		backends = append(backends, ffmpeg.HWAccelNone)
	}

	var readyMu sync.Mutex
	readyPublished := false
	publishReady := func() error {
		readyMu.Lock()
		defer readyMu.Unlock()
		if readyPublished {
			return nil
		}
		if err := r.publishReady(request, execution, startedAt); err != nil {
			return err
		}
		readyPublished = true
		return nil
	}
	isReady := func() bool {
		readyMu.Lock()
		defer readyMu.Unlock()
		return readyPublished
	}

	var finalResult transcodeexecutor.Result
	var lastWatchErr error
	for attemptIndex, backend := range backends {
		if runtimeView.Context.Err() != nil {
			finalResult = transcodeexecutor.Result{Err: runtimeView.Context.Err(), Cancelled: true}
			break
		}
		if time.Now().After(deadline) {
			finalResult = transcodeexecutor.Result{Err: context.DeadlineExceeded, TimedOut: true}
			lastWatchErr = context.DeadlineExceeded
			break
		}
		if attemptIndex > 0 {
			if err := resetOutputDirectory(runtimeView.OutputDir); err != nil {
				finalResult = transcodeexecutor.Result{Err: fmt.Errorf("reset generation output: %w", err)}
				break
			}
		}
		_ = r.sessions.ResetGenerationAttempt(request.SessionID, request.GenerationID, backend)

		attemptDeadline := deadline
		if backend != ffmpeg.HWAccelNone {
			hardwareDeadline := time.Now().Add(r.cfg.HardwareStartBudget)
			if hardwareDeadline.Before(attemptDeadline) {
				attemptDeadline = hardwareDeadline
			}
		}
		attemptCtx, cancelAttempt := context.WithCancel(runtimeView.Context)
		watchCtx, cancelWatch := context.WithDeadline(runtimeView.Context, attemptDeadline)
		watchDone := make(chan error, 1)
		go func() {
			err := r.waitForFirstSegment(watchCtx, runtimeView.OutputDir)
			if err == nil {
				err = publishReady()
			}
			if err != nil {
				cancelAttempt()
			}
			watchDone <- err
		}()

		args, err := r.buildArgs(runtimeView, request, backend)
		if err != nil {
			cancelAttempt()
			cancelWatch()
			lastWatchErr = <-watchDone
			finalResult = transcodeexecutor.Result{Err: err}
			break
		}
		kind := transcodegovernor.KindSoftwareTranscode
		if backend != ffmpeg.HWAccelNone {
			kind = transcodegovernor.KindHardwareTranscode
		}
		finalResult = r.runtime.Run(attemptCtx, kind, transcodeexecutor.Command{
			Path:       r.cfg.FFmpegPath,
			Args:       args,
			StderrTail: 80,
		}, transcodeexecutor.Callbacks{
			OnStarted: func(process *os.Process) {
				pid := 0
				if process != nil {
					pid = process.Pid
				}
				_ = r.sessions.MarkGenerationStarted(request.SessionID, request.GenerationID, backend, pid)
			},
			OnProgress: func(progress transcodeexecutor.Progress) {
				_ = r.sessions.MarkGenerationProgress(request.SessionID, request.GenerationID, progress.OutTimeMS, progress.Speed)
			},
		})
		cancelAttempt()
		cancelWatch()
		lastWatchErr = <-watchDone

		if !isReady() {
			if available, availableErr := firstSegmentAvailable(runtimeView.OutputDir); availableErr == nil && available {
				if err := publishReady(); err != nil && finalResult.Err == nil {
					finalResult.Err = err
				}
			}
		}
		if isReady() {
			if finalResult.Err == nil {
				_ = r.sessions.MarkGenerationCompleted(request.SessionID, request.GenerationID)
			} else if runtimeView.Context.Err() == nil {
				_ = r.sessions.MarkGenerationFailed(request.SessionID, request.GenerationID, "ffmpeg_failed", finalResult.ErrorText())
			}
			break
		}

		if lastWatchErr != nil && !errors.Is(lastWatchErr, context.Canceled) && !errors.Is(lastWatchErr, context.DeadlineExceeded) {
			if finalResult.Err == nil {
				finalResult.Err = lastWatchErr
			}
			break
		}
		canFallback := backend != ffmpeg.HWAccelNone && runtimeView.Context.Err() == nil && time.Now().Before(deadline)
		if canFallback {
			r.logger.Warnw("playback hardware startup failed; falling back to software",
				"session_id", request.SessionID,
				"generation_id", request.GenerationID,
				"backend", backend,
				"error", finalResult.ErrorText(),
				"watch_error", lastWatchErr,
			)
			continue
		}
		if backend != ffmpeg.HWAccelNone && request.DisableFallback {
			// §8：已强制硬件、未回退 CPU —— 排障日志，便于定位远端 GPU / 硬解问题。
			r.logger.Errorw("playback hardware startup failed; fallback to CPU is disabled (forced hardware)",
				"session_id", request.SessionID,
				"generation_id", request.GenerationID,
				"backend", backend,
				"error", finalResult.ErrorText(),
				"watch_error", lastWatchErr,
			)
			if finalResult.Err == nil {
				finalResult.Err = fmt.Errorf("hardware backend %q failed and CPU fallback is disabled", backend)
			}
		}
		break
	}

	if !isReady() {
		errorCode := "ffmpeg_failed"
		if finalResult.TimedOut || errors.Is(finalResult.Err, context.DeadlineExceeded) || errors.Is(lastWatchErr, context.DeadlineExceeded) {
			errorCode = "first_segment_timeout"
		}
		if runtimeView.Context.Err() != nil || (finalResult.Cancelled && !errors.Is(lastWatchErr, context.DeadlineExceeded)) {
			errorCode = "session_cancelled"
		}
		message := finalResult.ErrorText()
		if message == "" && lastWatchErr != nil {
			message = lastWatchErr.Error()
		}
		if message == "" {
			message = errorCode
		}
		_ = r.sessions.MarkGenerationFailed(request.SessionID, request.GenerationID, errorCode, message)
		execution.signalReady(ReadyResult{StartupMS: time.Since(startedAt).Milliseconds(), Err: finalResult.Err})
	}
	execution.signalDone(finalResult)
}

func (r *Runner) publishReady(request StartRequest, execution *Execution, startedAt time.Time) error {
	if err := r.sessions.MarkFirstSegmentReady(request.SessionID, request.GenerationID); err != nil {
		return err
	}
	sessionSnapshot, err := r.sessions.ActivateGeneration(request.SessionID, request.GenerationID)
	if err != nil {
		return err
	}
	generation := playbacksession.GenerationSnapshot{}
	if sessionSnapshot.Generation != nil {
		generation = *sessionSnapshot.Generation
	}
	execution.signalReady(ReadyResult{
		Session:    sessionSnapshot,
		Generation: generation,
		StartupMS:  time.Since(startedAt).Milliseconds(),
	})
	return nil
}

func (r *Runner) buildArgs(runtimeView playbacksession.GenerationRuntime, request StartRequest, backend string) ([]string, error) {
	preset, ok := transcodeprofile.RuntimeEffective(request.ProfileID)
	if !ok {
		return nil, fmt.Errorf("unknown quality preset %q", request.ProfileID)
	}
	fps := request.FPS
	if fps <= 0 {
		fps = 25
	}
	gopSize := int(math.Round(fps * float64(r.cfg.SegmentDuration)))
	if gopSize < 1 {
		gopSize = r.cfg.SegmentDuration * 25
	}

	// 缩放：仅软件后端由 runner 生成 decrease+pad 滤镜；硬件后端由 encoder 按
	// 后端生成 scale_cuda/scale_qsv/…。原画（Width==0）任何后端都不加 scale。
	videoFilter := request.VideoFilter
	if videoFilter == "" && backend == ffmpeg.HWAccelNone && preset.Width > 0 {
		videoFilter = fitScaleFilter(preset.Width, preset.Height)
	}

	// 每档 CRF>0 优先，否则全局 transcode_crf。
	crf := preset.CRF
	if crf <= 0 {
		crf = r.cfg.TranscodeCRF
	}

	audioBitrate := preset.AudioBitrate
	if audioBitrate == "" && preset.AudioKbps > 0 {
		audioBitrate = strconv.Itoa(preset.AudioKbps) + "k"
	}
	if audioBitrate == "" {
		audioBitrate = "128k"
	}

	args := ffmpeg.BuildRollingHLSArgs(ffmpeg.BuildOptions{
		InputPath:             request.InputPath,
		OutputDir:             runtimeView.OutputDir,
		ExtraInput:            request.ExtraInput,
		HWAccel:               backend,
		Profile:               ffmpeg.Profile{Width: preset.Width, Height: preset.Height, AudioBitrate: audioBitrate},
		VAAPIDevice:           r.cfg.VAAPIDevice,
		X264Preset:            r.cfg.X264Preset,
		QSVPreset:             r.cfg.QSVPreset,
		Threads:               r.cfg.Threads,
		SoftwareTune:          "zerolatency",
		QSVAttachOutputFormat: false,
		VideoFilter:           videoFilter,
		HLSTime:               r.cfg.SegmentDuration,
		HLSFlags:              "delete_segments+temp_file+independent_segments+program_date_time",
		ForceKeyFrames:        true,
		StartOffsetSec:        float64(request.StartPositionMS) / 1000,
		GOPSize:               gopSize,

		UseNumericRateControl: true,
		NumericBitrateKbps:    preset.BitrateKbps,
		EffectiveCRF:          crf,
		SourceIsAV1:           request.SourceIsAV1,
	}, ffmpeg.RollingHLSOptions{
		ListSize:        r.cfg.PlaylistWindow,
		DeleteThreshold: r.cfg.DeleteThreshold,
		SegmentPattern:  "seg_%06d.ts",
		MapAudioTrack:   true,
		AudioTrack:      runtimeView.Snapshot.AudioTrack,
	})
	return withMachineProgress(args), nil
}

func (r *Runner) waitForFirstSegment(ctx context.Context, outputDir string) error {
	ticker := time.NewTicker(r.cfg.FirstSegmentPoll)
	defer ticker.Stop()
	for {
		available, err := firstSegmentAvailable(outputDir)
		if err == nil && available {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func firstSegmentAvailable(outputDir string) (bool, error) {
	manifestPath := filepath.Join(outputDir, "stream.m3u8")
	content, err := os.ReadFile(manifestPath)
	if err != nil {
		return false, err
	}
	for _, rawLine := range strings.Split(string(content), "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if filepath.Base(line) != line || strings.ContainsAny(line, `/\\`) {
			return false, fmt.Errorf("unsafe segment path in generated manifest: %q", line)
		}
		info, err := os.Stat(filepath.Join(outputDir, line))
		if err == nil && info.Mode().IsRegular() && info.Size() > 0 {
			return true, nil
		}
	}
	return false, nil
}

func resetOutputDirectory(outputDir string) error {
	entries, err := os.ReadDir(outputDir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := os.RemoveAll(filepath.Join(outputDir, entry.Name())); err != nil {
			return err
		}
	}
	return nil
}

func withMachineProgress(args []string) []string {
	if len(args) == 0 {
		return nil
	}
	outputIndex := len(args) - 1
	result := make([]string, 0, len(args)+3)
	result = append(result, args[:outputIndex]...)
	result = append(result, "-progress", "pipe:2", "-nostats")
	result = append(result, args[outputIndex])
	return result
}

func fitScaleFilter(width, height int) string {
	return fmt.Sprintf(
		"scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2:color=black,setsar=1",
		width,
		height,
		width,
		height,
	)
}

func (e *Execution) signalReady(result ReadyResult) {
	e.readyOnce.Do(func() {
		e.ready <- result
		close(e.ready)
	})
}

func (e *Execution) signalDone(result transcodeexecutor.Result) {
	e.doneOnce.Do(func() {
		e.done <- result
		close(e.done)
	})
}
