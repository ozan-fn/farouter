package kiro

import (
	"context"
	"io"
	"log"
	"sync"
	"sync/atomic"
	"time"
)

const (
	StreamStallTimeoutMs  = 360 * time.Second
	StreamFirstChunkTimeoutMs = 200 * time.Second
	FetchConnectTimeoutMs = 60 * time.Second
)

type StreamController struct {
	ctx          context.Context
	cancel       context.CancelFunc
	disconnected atomic.Bool
	startTime    time.Time
	mu           sync.Mutex
	abortTimer   *time.Timer
}

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

type upstreamTapReader struct {
	reader       io.Reader
	sc           *StreamController
	stallTimeout time.Duration
	chunkCount   int
	totalBytes   int64
	lastChunkAt  time.Time
	t0           time.Time
	done         atomic.Bool
}

func newUpstreamTapReader(r io.Reader, sc *StreamController, stallTimeout time.Duration) *upstreamTapReader {
	return &upstreamTapReader{
		reader:       r,
		sc:           sc,
		stallTimeout: stallTimeout,
		lastChunkAt:  time.Now(),
		t0:           time.Now(),
	}
}

func (u *upstreamTapReader) Read(p []byte) (int, error) {
	if u.done.Load() {
		return 0, io.EOF
	}
	n, err := u.reader.Read(p)
	if n > 0 {
		u.chunkCount++
		u.totalBytes += int64(n)
		now := time.Now()
		u.lastChunkAt = now
	}
	if err == io.EOF {
		u.done.Store(true)
		log.Printf("STREAM upstream EOF | chunks=%d bytes=%d dur=%v", u.chunkCount, u.totalBytes, time.Since(u.t0))
	}
	return n, err
}

func PipeWithDisconnect(r io.Reader, sc *StreamController, stallTimeout time.Duration, model string) io.Reader {
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

		doRead := func() {
			n, err := r.Read(buf)
			data := make([]byte, n)
			copy(data, buf[:n])
			readCh <- readResult{n, data, err}
		}

		go doRead()
		stall := time.NewTimer(stallTimeout)
		defer stall.Stop()

		chunkCount := 0
		totalBytes := int64(0)
		t0 := time.Now()

		for {
			select {
			case <-sc.ctx.Done():
				return

			case <-stall.C:
				log.Printf("STREAM STALL TIMEOUT %v | chunks=%d bytes=%d dur=%v",
					stallTimeout, chunkCount, totalBytes, time.Since(t0))
				writeStreamError(pw, 502, "upstream stalled — no data received for "+stallTimeout.String())
				pw.Write([]byte(SSEDone))
				sc.HandleError(io.ErrUnexpectedEOF)
				return

			case res := <-readCh:
				if res.n > 0 {
					chunkCount++
					totalBytes += int64(res.n)
					stall.Reset(stallTimeout)
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
