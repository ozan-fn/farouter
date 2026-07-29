package kiro

import (
	"context"
	"fmt"
	"io"
	"log"
	"sync"
	"sync/atomic"
	"time"
)

// VansRouter ref: open-sse/utils/base.js — StreamState + readWithTimeout
// VansRouter ref: open-sse/executors/kiro.js — stream pipeline (FETCH_CONNECT_TIMEOUT_MS etc.)
//
//   StreamController       → base.js StreamState (disconnect/abort lifecycle)
//   NewStreamController    → base.js createStreamState
//   PipeWithDisconnect     → base.js readWithTimeout (dual timeout: TTFT + stall)
//   upstreamTapReader      → base.js upstreamTapReader / usage tracking
//
// Constants:
//   StreamStallTimeout     → kiroConstants.js STREAM_STALL_TIMEOUT_MS (360s)
//   StreamFirstChunkTimeout → kiroConstants.js STREAM_FIRST_CHUNK_TIMEOUT_MS (200s)
//   FetchConnectTimeout    → kiroConstants.js FETCH_CONNECT_TIMEOUT_MS (60s)

const (
	StreamStallTimeout     = 360 * time.Second
	StreamFirstChunkTimeout = 200 * time.Second
	FetchConnectTimeout     = 60 * time.Second
)

// StreamController manages stream lifecycle with disconnect detection and abort.
// VansRouter ref: base.js StreamState — startTime, disconnected, abortTimer
type StreamController struct {
	ctx          context.Context
	cancel       context.CancelFunc
	disconnected atomic.Bool
	startTime    time.Time
	mu           sync.Mutex
	abortTimer   *time.Timer
}

// NewStreamController creates a new stream lifecycle controller.
// VansRouter ref: base.js createStreamState
func NewStreamController(ctx context.Context) *StreamController {
	ctx, cancel := context.WithCancel(ctx)
	return &StreamController{
		ctx:       ctx,
		cancel:    cancel,
		startTime: time.Now(),
	}
}

func (sc *StreamController) Signal() context.Context { return sc.ctx }

func (sc *StreamController) IsConnected() bool { return !sc.disconnected.Load() }

func (sc *StreamController) HandleDisconnect(reason string) {
	if !sc.disconnected.CompareAndSwap(false, true) {
		return
	}
	log.Printf("STREAM DISCONNECT: %s | dur=%v", reason, time.Since(sc.startTime))
	sc.mu.Lock()
	sc.abortTimer = time.AfterFunc(500*time.Millisecond, sc.cancel)
	sc.mu.Unlock()
}

func (sc *StreamController) HandleComplete() {
	if !sc.disconnected.CompareAndSwap(false, true) {
		return
	}
	sc.mu.Lock()
	if sc.abortTimer != nil {
		sc.abortTimer.Stop()
		sc.abortTimer = nil
	}
	sc.mu.Unlock()
}

func (sc *StreamController) HandleError(err error) {
	if !sc.disconnected.CompareAndSwap(false, true) {
		return
	}
	sc.mu.Lock()
	if sc.abortTimer != nil {
		sc.abortTimer.Stop()
		sc.abortTimer = nil
	}
	sc.mu.Unlock()
	log.Printf("STREAM ERROR: %v | dur=%v", err, time.Since(sc.startTime))
}

func (sc *StreamController) Abort() {
	sc.mu.Lock()
	if sc.abortTimer != nil {
		sc.abortTimer.Stop()
		sc.abortTimer = nil
	}
	sc.mu.Unlock()
	sc.cancel()
}

// PipeWithDisconnect wraps an io.Reader with disconnect detection and dual timeouts.
// VansRouter ref: base.js readWithTimeout — same dual-timeout pattern
func PipeWithDisconnect(r io.Reader, sc *StreamController, ttftTimeout, stallTimeout time.Duration, model string) io.Reader {
	if ttftTimeout <= 0 {
		ttftTimeout = stallTimeout
	}

	pr, pw := io.Pipe()

	go func() {
		defer pw.Close()

		type readResult struct {
			n   int
			buf []byte
			err error
		}

		readCh := make(chan readResult, 1)
		buf := make([]byte, 32768)
		sawChunk := false

		doRead := func() {
			n, err := r.Read(buf)
			data := make([]byte, n)
			copy(data, buf[:n])
			readCh <- readResult{n, data, err}
		}

		go doRead()
		// First chunk uses TTFT timeout, subsequent chunks use stall timeout (VansRouter pattern)
		currentTimeout := ttftTimeout
		timer := time.NewTimer(currentTimeout)
		defer timer.Stop()

		chunkCount := 0
		totalBytes := int64(0)
		t0 := time.Now()

		for {
			select {
			case <-sc.ctx.Done():
				return

			case <-timer.C:
				phase := "stalled"
				if !sawChunk {
					phase = "timed out before first chunk"
				}
				log.Printf("STREAM %s | timeout=%v chunks=%d bytes=%d dur=%v",
					phase, currentTimeout, chunkCount, totalBytes, time.Since(t0))
				writeStreamError(pw, 502, fmt.Sprintf("upstream %s — no data received for %v", phase, currentTimeout))
				pw.Write([]byte(SSEDone))
				sc.HandleError(io.ErrUnexpectedEOF)
				return

			case res := <-readCh:
				if res.n > 0 {
					chunkCount++
					totalBytes += int64(res.n)
					if !sawChunk {
						sawChunk = true
					}
					// Switch to stall timeout after first chunk (VansRouter pattern)
					timer.Reset(stallTimeout)
					if _, werr := pw.Write(res.buf); werr != nil {
						return
					}
				}
				if res.err != nil {
					if res.err != io.EOF {
						log.Printf("STREAM upstream read error: %v | chunks=%d bytes=%d dur=%v", res.err, chunkCount, totalBytes, time.Since(t0))
					} else {
						log.Printf("STREAM upstream EOF | chunks=%d bytes=%d dur=%v", chunkCount, totalBytes, time.Since(t0))
					}
					return
				}
				go doRead()
			}
		}
	}()

	return pr
}
