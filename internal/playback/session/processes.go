package session

import (
	"context"
	"fmt"
)

// ProcessLease fences generation cleanup against the complete Runner lifetime,
// including hardware startup and any software fallback attempt.
type ProcessLease struct {
	lease *ReaderLease
}

func (l *ProcessLease) Release() {
	if l != nil && l.lease != nil {
		l.lease.Release()
	}
}

func (m *Manager) AcquireProcess(sessionID string, generationID uint64) (*ProcessLease, error) {
	session, err := m.getSession(sessionID)
	if err != nil {
		return nil, err
	}

	session.mu.RLock()
	defer session.mu.RUnlock()
	if session.closing {
		return nil, ErrSessionClosing
	}
	generation := session.generations[generationID]
	if generation == nil {
		return nil, ErrGenerationNotFound
	}
	gate := generation.ensureProcessGate()
	if err := gate.acquire(); err != nil {
		return nil, err
	}
	return &ProcessLease{lease: &ReaderLease{release: gate.release}}, nil
}

func (g *Generation) ensureProcessGate() *readerGate {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.processGate == nil {
		g.processGate = newReaderGate()
	}
	return g.processGate
}

func (g *Generation) waitForProcessExit(ctx context.Context) error {
	// A suspended process may not observe graceful termination on every
	// platform. Resume first; the cancelled generation context then owns exit.
	_ = g.resumeIfSuspended()
	return g.ensureProcessGate().closeAndWait(ctx)
}

// StopCurrentGenerationProcess cancels the running generation's encoder before
// a replacement generation starts. The bounded governor pool has a single
// transcode slot, so the replacement cannot acquire it while the previous
// generation keeps its lease; the old generation is normally retired only after
// the replacement activates (see ActivateGeneration), which would deadlock a
// seek/quality restart. Resuming and cancelling here releases the process and
// its governor slot while the already-materialized playlist and segments stay
// readable for buffered playback.
func (m *Manager) StopCurrentGenerationProcess(sessionID string) error {
	session, err := m.getSession(sessionID)
	if err != nil {
		return err
	}

	session.mu.Lock()
	if session.closing {
		session.mu.Unlock()
		return ErrSessionClosing
	}
	currentID := session.currentGenerationID
	generation := session.generations[currentID]
	session.mu.Unlock()

	if currentID == 0 || generation == nil {
		return nil
	}

	// A SIGSTOP-throttled process cannot observe context cancellation. Resume
	// it first, then cancel so exec.CommandContext terminates the encoder.
	_ = generation.resumeIfSuspended()
	generation.cancel()

	waitCtx, cancel := context.WithTimeout(context.Background(), m.cfg.CloseDrainTimeout)
	defer cancel()
	if err := generation.waitForProcessExit(waitCtx); err != nil {
		return fmt.Errorf("stop current generation process: %w", err)
	}
	return nil
}
