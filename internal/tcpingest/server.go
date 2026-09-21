package tcpingest

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"icebreaker-radar/internal/echoparser"
	"icebreaker-radar/internal/iceanalyzer"
	"icebreaker-radar/internal/store"
)

// Server listens for radar-frontend TCP streams, parses frames, analyzes ice
// conditions, persists statistics to Postgres and caches raw echo slices.
type Server struct {
	addr      string
	analyzer  iceanalyzer.Config
	pg        *store.Postgres
	cache     *store.RedisCache
	log       *slog.Logger
	workers   int
	queueSize int

	listener net.Listener
	queue    chan *echoparser.Sweep
	wg       sync.WaitGroup

	activeConns atomic.Int64
	sweepsIn    atomic.Uint64
	heartbeats  atomic.Uint64
	persisted   atomic.Uint64
	parseErrors atomic.Uint64
}

// Metrics is a point-in-time snapshot of ingest counters.
type Metrics struct {
	ActiveConns int64  `json:"active_conns"`
	SweepsIn    uint64 `json:"sweeps_in"`
	Heartbeats  uint64 `json:"heartbeats"`
	Persisted   uint64 `json:"persisted"`
	ParseErrors uint64 `json:"parse_errors"`
}

func NewServer(addr string, analyzerCfg iceanalyzer.Config, pg *store.Postgres, cache *store.RedisCache, workers int, log *slog.Logger) *Server {
	if workers <= 0 {
		workers = 4
	}
	return &Server{
		addr:      addr,
		analyzer:  analyzerCfg,
		pg:        pg,
		cache:     cache,
		log:       log,
		workers:   workers,
		queueSize: 256,
	}
}

// Serve starts the listener and worker pool, blocking until ctx is canceled.
func (s *Server) Serve(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return err
	}
	s.listener = ln
	s.queue = make(chan *echoparser.Sweep, s.queueSize)
	s.log.Info("tcp ingest listening", "addr", ln.Addr().String(), "workers", s.workers)

	for i := 0; i < s.workers; i++ {
		s.wg.Add(1)
		go s.worker(ctx, i)
	}

	go func() {
		<-ctx.Done()
		ln.Close()
	}()

	var acceptWg sync.WaitGroup
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				break
			}
			s.log.Error("accept failed", "err", err)
			continue
		}
		acceptWg.Add(1)
		go func(c net.Conn) {
			defer acceptWg.Done()
			s.handleConn(ctx, c)
		}(conn)
	}
	acceptWg.Wait()
	close(s.queue)
	s.wg.Wait()
	return nil
}

func (s *Server) Metrics() Metrics {
	return Metrics{
		ActiveConns: s.activeConns.Load(),
		SweepsIn:    s.sweepsIn.Load(),
		Heartbeats:  s.heartbeats.Load(),
		Persisted:   s.persisted.Load(),
		ParseErrors: s.parseErrors.Load(),
	}
}

func (s *Server) handleConn(ctx context.Context, conn net.Conn) {
	remote := conn.RemoteAddr().String()
	s.activeConns.Add(1)
	defer func() {
		s.activeConns.Add(-1)
		conn.Close()
	}()
	s.log.Info("radar frontend connected", "remote", remote)

	reader := echoparser.NewReader(conn)
	for {
		frame, err := reader.Next()
		if err != nil {
			if errors.Is(err, io.EOF) || ctx.Err() != nil {
				s.log.Info("radar frontend disconnected", "remote", remote)
			} else {
				s.log.Error("read frame", "remote", remote, "err", err)
			}
			return
		}

		switch frame.Type {
		case echoparser.FrameHeartbeat:
			s.heartbeats.Add(1)
			if err := s.cache.TouchHeartbeat(ctx, frame.Timestamp); err != nil {
				s.log.Warn("record heartbeat", "err", err)
			}
		case echoparser.FrameSweep:
			s.sweepsIn.Add(1)
			select {
			case s.queue <- frame.Sweep:
			case <-ctx.Done():
				return
			}
		default:
			// Unknown frame types were already payload-skipped by the reader.
		}
	}
}

func (s *Server) worker(ctx context.Context, id int) {
	defer s.wg.Done()
	for sweep := range s.queue {
		s.process(ctx, sweep)
	}
}

func (s *Server) process(ctx context.Context, sweep *echoparser.Sweep) {
	result := iceanalyzer.Analyze(sweep, s.analyzer)

	var intensitySum uint64
	for _, ray := range sweep.Rays {
		for _, v := range ray {
			intensitySum += uint64(v)
		}
	}
	mean := float64(intensitySum) / float64(sweep.RayCount()*sweep.BinsPerRay())
	ts := sweep.Timestamp
	if ts.IsZero() {
		ts = time.Now().UTC()
	}

	if err := s.cache.CacheSweep(ctx, store.EchoSweep{
		ObservedAt:      ts,
		ShipLat:         sweep.ShipLat,
		ShipLon:         sweep.ShipLon,
		RangeM:          sweep.RangeM,
		BinSpacingM:     sweep.BinSpacingM,
		StartBearingRad: sweep.StartBearingRad,
		AngularStepRad:  sweep.AngularStepRad,
		RayCount:        sweep.RayCount(),
		BinsPerRay:      sweep.BinsPerRay(),
		MeanIntensity:   mean,
		Rays:            sweep.Rays,
	}); err != nil {
		s.log.Warn("cache echo slice", "err", err)
	}

	input := store.SweepInput{
		ObservedAt:    ts,
		ShipLat:       sweep.ShipLat,
		ShipLon:       sweep.ShipLon,
		RangeM:        sweep.RangeM,
		Concentration: result.Concentration,
		IceBins:       result.IceBins,
		TotalBins:     result.TotalBins,
		MeanIntensity: result.MeanIntensity,
		MaxIntensity:  result.MaxIntensity,
		MinLat:        floatPtrIfValid(result.MinLat),
		MaxLat:        floatPtrIfValid(result.MaxLat),
		MinLon:        floatPtrIfValid(result.MinLon),
		MaxLon:        floatPtrIfValid(result.MaxLon),
	}
	for _, r := range result.Ridges {
		input.Ridges = append(input.Ridges, store.RidgeInput{
			Lat: r.Lat, Lon: r.Lon, BearingRad: r.BearingRad,
			LengthM: r.LengthM, Aspect: r.Aspect,
			BinCount: r.BinCount, MaxIntensity: r.MaxIntensity,
		})
	}

	if _, err := s.pg.InsertSweep(ctx, input); err != nil {
		s.log.Error("persist sweep", "err", err)
		return
	}
	s.persisted.Add(1)
}

func floatPtrIfValid(v float64) *float64 {
	if v != v { // NaN: sweep contained no ice bins
		return nil
	}
	return &v
}
