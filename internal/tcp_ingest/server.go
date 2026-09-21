// Package tcp_ingest accepts raw radar echo frames over TCP, parses them,
// runs ice analysis, and fans results out to the Redis cache and PostgreSQL.
package tcp_ingest

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/polar/icebreakerradar/internal/echo_parser"
	"github.com/polar/icebreakerradar/internal/ice_analyzer"
	"github.com/polar/icebreakerradar/internal/model"
)

// Cacher is the recent-echo cache (Redis).
type Cacher interface {
	Add(ctx context.Context, env model.Envelope) error
}

// Persister writes envelopes to durable storage.
type Persister interface {
	InsertEnvelope(ctx context.Context, env model.Envelope) error
}

// Stats are counters exposed to the HTTP layer.
type Stats struct {
	ConnectionsAccepted int64 `json:"connections_accepted"`
	ConnectionsActive   int64 `json:"connections_active"`
	FramesParsed        int64 `json:"frames_parsed"`
	ParseErrors         int64 `json:"parse_errors"`
	CacheWrites         int64 `json:"cache_writes"`
	Persisted           int64 `json:"persisted"`
	PersistErrors       int64 `json:"persist_errors"`
	DroppedBackpressure int64 `json:"dropped_backpressure"`
}

// PipelineConfig controls the ingest pipeline.
type PipelineConfig struct {
	ListenAddr      string
	MaxConnections  int
	ChannelBuffer   int
	BatchSize       int
	BatchFlushEvery time.Duration
	WriteTimeout    time.Duration
}

// DefaultPipelineConfig returns production-oriented pipeline defaults.
func DefaultPipelineConfig(addr string, maxConn int) PipelineConfig {
	return PipelineConfig{
		ListenAddr:      addr,
		MaxConnections:  maxConn,
		ChannelBuffer:   4096,
		BatchSize:       64,
		BatchFlushEvery: time.Second,
		WriteTimeout:    3 * time.Second,
	}
}

// Server is the TCP ingest lifecycle owner.
type Server struct {
	cfg       PipelineConfig
	analyzer  *ice_analyzer.Analyzer
	cache     Cacher
	persister Persister
	log       *slog.Logger

	listener  net.Listener
	envelopes chan model.Envelope
	persistCh chan model.Envelope
	slots     chan struct{}

	mu    sync.RWMutex
	stats Stats

	connsMu sync.Mutex
	conns   map[net.Conn]struct{}

	connWG   sync.WaitGroup // active connection goroutines
	workerWG sync.WaitGroup // cache + persist workers
	serveWG  sync.WaitGroup // the ListenAndServe call itself
	closed   chan struct{}
	closeOne sync.Once
}

// NewServer wires the pipeline without opening the socket.
func NewServer(cfg PipelineConfig, an *ice_analyzer.Analyzer, cache Cacher,
	persister Persister, log *slog.Logger) *Server {
	if cfg.MaxConnections <= 0 {
		cfg.MaxConnections = 16
	}
	if cfg.ChannelBuffer <= 0 {
		cfg.ChannelBuffer = 4096
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 64
	}
	if cfg.BatchFlushEvery <= 0 {
		cfg.BatchFlushEvery = time.Second
	}
	if cfg.WriteTimeout <= 0 {
		cfg.WriteTimeout = 3 * time.Second
	}
	return &Server{
		cfg:       cfg,
		analyzer:  an,
		cache:     cache,
		persister: persister,
		log:       log,
		envelopes: make(chan model.Envelope, cfg.ChannelBuffer),
		persistCh: make(chan model.Envelope, cfg.ChannelBuffer),
		slots:     make(chan struct{}, cfg.MaxConnections),
		conns:     make(map[net.Conn]struct{}),
		closed:    make(chan struct{}),
	}
}

// ListenAndServe binds the configured port and serves until Shutdown.
func (s *Server) ListenAndServe() error {
	ln, err := net.Listen("tcp", s.cfg.ListenAddr)
	if err != nil {
		return fmt.Errorf("tcp ingest listen: %w", err)
	}
	return s.Serve(ln)
}

// Serve accepts connections on an already-bound listener until Shutdown.
func (s *Server) Serve(ln net.Listener) error {
	s.listener = ln
	s.log.Info("radar TCP ingest listening", "addr", s.cfg.ListenAddr,
		"max_connections", s.cfg.MaxConnections)

	s.serveWG.Add(1)
	defer s.serveWG.Done()
	s.workerWG.Add(2)
	go s.cacheWorker()
	go s.persistWorker()

	for {
		conn, err := ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		select {
		case s.slots <- struct{}{}:
		default:
			s.log.Warn("connection limit reached, rejecting radar client",
				"remote", conn.RemoteAddr())
			_ = conn.Close()
			continue
		}
		s.inc(func(st *Stats) { st.ConnectionsAccepted++ })
		s.connWG.Add(1)
		go s.handleConn(conn)
	}
}

func (s *Server) handleConn(conn net.Conn) {
	defer func() {
		_ = conn.Close()
		s.connsMu.Lock()
		delete(s.conns, conn)
		s.connsMu.Unlock()
		<-s.slots
		s.inc(func(st *Stats) { st.ConnectionsActive-- })
		s.connWG.Done()
	}()
	remote := conn.RemoteAddr().String()
	s.connsMu.Lock()
	s.conns[conn] = struct{}{}
	s.connsMu.Unlock()
	s.inc(func(st *Stats) { st.ConnectionsActive++ })
	s.log.Info("radar client connected", "remote", remote)

	reader := echo_parser.NewFrameReader(conn)
	for {
		sweep, err := reader.Read()
		if err != nil {
			if !errors.Is(err, net.ErrClosed) {
				s.log.Debug("radar stream ended", "remote", remote, "err", err)
			}
			return
		}
		if err := validateSweep(sweep); err != nil {
			s.inc(func(st *Stats) { st.ParseErrors++ })
			s.log.Warn("invalid sweep", "remote", remote, "sweep", sweep.ID, "err", err)
			continue
		}
		env := model.Envelope{Sweep: sweep, Analysis: s.analyzer.Analyze(sweep)}
		s.inc(func(st *Stats) { st.FramesParsed++ })
		select {
		case s.envelopes <- env:
		default:
			// Never block a radar client; shed load under overload.
			s.inc(func(st *Stats) { st.DroppedBackpressure++ })
			s.log.Warn("pipeline full, dropping envelope", "sweep", sweep.ID)
		}
	}
}

// cacheWorker updates Redis immediately, then hands the envelope to the
// persistence worker. It closes persistCh once the inbound channel drains,
// which is the signal for a clean shutdown.
func (s *Server) cacheWorker() {
	defer s.workerWG.Done()
	defer close(s.persistCh)
	for env := range s.envelopes {
		ctx, cancel := context.WithTimeout(context.Background(), s.cfg.WriteTimeout)
		if err := s.cache.Add(ctx, env); err != nil {
			s.log.Error("redis cache write failed", "sweep", env.Sweep.ID, "err", err)
		} else {
			s.inc(func(st *Stats) { st.CacheWrites++ })
		}
		cancel()
		s.persistCh <- env
	}
}

// persistWorker flushes envelopes to PostgreSQL in batches by size or timer.
func (s *Server) persistWorker() {
	defer s.workerWG.Done()
	batch := make([]model.Envelope, 0, s.cfg.BatchSize)
	ticker := time.NewTicker(s.cfg.BatchFlushEvery)
	defer ticker.Stop()

	flush := func() {
		if len(batch) == 0 {
			return
		}
		due := batch
		batch = make([]model.Envelope, 0, s.cfg.BatchSize)
		s.writeBatch(due)
	}

	for {
		select {
		case env, ok := <-s.persistCh:
			if !ok {
				flush() // pipeline fully drained
				return
			}
			batch = append(batch, env)
			if len(batch) >= s.cfg.BatchSize {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}

func (s *Server) writeBatch(batch []model.Envelope) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, env := range batch {
		if err := s.persister.InsertEnvelope(ctx, env); err != nil {
			s.inc(func(st *Stats) { st.PersistErrors++ })
			s.log.Error("persist envelope failed", "sweep", env.Sweep.ID, "err", err)
			continue
		}
		s.inc(func(st *Stats) { st.Persisted++ })
	}
}

// Shutdown stops accepting, closes active connections, and drains the pipeline
// so every buffered envelope reaches Redis and PostgreSQL before return.
func (s *Server) Shutdown(ctx context.Context) error {
	var listenErr error
	s.closeOne.Do(func() {
		close(s.closed)
		if s.listener != nil {
			listenErr = s.listener.Close()
		}
		s.connsMu.Lock()
		for c := range s.conns {
			_ = c.Close()
		}
		s.connsMu.Unlock()
	})

	// Wait for the accept loop to finish, then for connection goroutines.
	// Once no producer remains, close envelopes so the cache worker drains it,
	// which in turn drains persistCh for the persist worker.
	serveDone := make(chan struct{})
	go func() {
		s.serveWG.Wait()
		s.connWG.Wait()
		close(s.envelopes)
		s.workerWG.Wait()
		close(serveDone)
	}()
	select {
	case <-serveDone:
		return listenErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Snapshot returns a copy of pipeline counters.
func (s *Server) Snapshot() Stats {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.stats
}

func (s *Server) inc(fn func(*Stats)) {
	s.mu.Lock()
	fn(&s.stats)
	s.mu.Unlock()
}

func validateSweep(s *model.Sweep) error {
	if s == nil {
		return errors.New("nil sweep")
	}
	if len(s.Intensity) == 0 {
		return errors.New("empty intensity payload")
	}
	if s.ShipLat < -90 || s.ShipLat > 90 || s.ShipLon < -180 || s.ShipLon > 180 {
		return fmt.Errorf("ship coordinates out of range: lat=%.4f lon=%.4f",
			s.ShipLat, s.ShipLon)
	}
	if s.Time.IsZero() {
		return errors.New("missing timestamp")
	}
	return nil
}
