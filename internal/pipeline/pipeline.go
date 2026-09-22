// Package pipeline wires decoded frames through ice analysis, Redis caching,
// batched PostgreSQL persistence.
package pipeline

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/icebreaker/ice-radar-api/internal/cache"
	"github.com/icebreaker/ice-radar-api/internal/config"
	"github.com/icebreaker/ice-radar-api/internal/ice_analyzer"
	"github.com/icebreaker/ice-radar-api/internal/model"
	"github.com/icebreaker/ice-radar-api/internal/storage"
)

// Pipeline implements tcp_ingest.FrameSink.
type Pipeline struct {
	analyzer *ice_analyzer.Analyzer
	cache    *cache.EchoCache
	store    *storage.Postgres
	logger   *slog.Logger

	frames chan model.EchoFrame

	batch   []model.AnalyzedFrame
	batchMu sync.Mutex
	timer   *time.Timer

	cfg config.Config

	wg     sync.WaitGroup
	cancel context.CancelFunc

	queued    int64
	ingested  int64
	dropped   int64
	persisted int64
	cacheErr  int64
}

func New(cfg config.Config, analyzer *ice_analyzer.Analyzer,
	echoCache *cache.EchoCache, store *storage.Postgres, logger *slog.Logger) *Pipeline {
	p := &Pipeline{
		analyzer: analyzer,
		cache:    echoCache,
		store:    store,
		logger:   logger,
		frames:   make(chan model.EchoFrame, 4096),
		cfg:      cfg,
	}
	p.timer = time.AfterFunc(cfg.BatchFlushAfter, p.flushTimer)
	p.timer.Stop()
	return p
}

// Stats are simple processing counters.
type Stats struct {
	Queued    int64 `json:"queued"`
	Ingested  int64 `json:"ingested"`
	Dropped   int64 `json:"dropped"`
	Persisted int64 `json:"persisted"`
	CacheErrs int64 `json:"cache_errors"`
}

func (p *Pipeline) StatsSnapshot() Stats {
	p.batchMu.Lock()
	defer p.batchMu.Unlock()
	return Stats{
		Queued:    p.queued,
		Ingested:  p.ingested,
		Dropped:   p.dropped,
		Persisted: p.persisted,
		CacheErrs: p.cacheErr,
	}
}

// Ingest enqueues a frame; returns false when the queue is full and the frame
// is dropped under back-pressure.
func (p *Pipeline) Ingest(ctx context.Context, frame model.EchoFrame) bool {
	select {
	case p.frames <- frame:
		p.bump(func() { p.queued++ })
		return true
	default:
		p.bump(func() { p.dropped++ })
		return false
	}
}

func (p *Pipeline) bump(fn func()) {
	p.batchMu.Lock()
	fn()
	p.batchMu.Unlock()
}

// Run consumes frames until the context is canceled, then flushes the batch.
func (p *Pipeline) Run(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	p.cancel = cancel
	p.wg.Add(1)
	defer p.wg.Done()

	for {
		select {
		case <-ctx.Done():
			p.flush(context.Background())
			return
		case frame := <-p.frames:
			p.process(ctx, frame)
		}
	}
}

func (p *Pipeline) process(ctx context.Context, frame model.EchoFrame) {
	af := p.analyzer.Analyze(&frame)

	if err := p.cache.Put(ctx, af); err != nil {
		p.bump(func() { p.cacheErr++ })
		p.logger.Warn("cache write failed", "frame_id", frame.FrameID, "error", err.Error())
	}

	p.batchMu.Lock()
	p.batch = append(p.batch, af)
	p.ingested++
	n := len(p.batch)
	if n == 1 {
		p.timer.Reset(p.cfg.BatchFlushAfter)
	}
	full := n >= p.cfg.BatchSize
	p.batchMu.Unlock()

	if full {
		p.flush(ctx)
	}
}

func (p *Pipeline) flushTimer() {
	p.flush(context.Background())
}

func (p *Pipeline) flush(ctx context.Context) {
	p.batchMu.Lock()
	if !p.timer.Stop() {
		select {
		case <-p.timer.C:
		default:
		}
	}
	batch := p.batch
	p.batch = nil
	p.batchMu.Unlock()

	if len(batch) == 0 {
		return
	}

	flushCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := p.store.BatchInsert(flushCtx, batch); err != nil {
		p.logger.Error("persist batch failed", "frames", len(batch), "error", err.Error())
		return
	}
	p.bump(func() { p.persisted += int64(len(batch)) })
	p.logger.Debug("batch persisted", "frames", len(batch))
}

// Close stops processing after draining buffered frames.
func (p *Pipeline) Close() {
	if p.cancel != nil {
		p.cancel()
	}
	p.wg.Wait()
	p.timer.Stop()
}
