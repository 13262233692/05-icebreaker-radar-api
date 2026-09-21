package tcp_ingest

import (
	"context"
	"io"
	"log/slog"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/polar/icebreakerradar/internal/echo_parser"
	"github.com/polar/icebreakerradar/internal/ice_analyzer"
	"github.com/polar/icebreakerradar/internal/model"
)

type fakeCache struct {
	mu    sync.Mutex
	items []model.Envelope
}

func (f *fakeCache) Add(_ context.Context, env model.Envelope) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.items = append(f.items, env)
	return nil
}

func (f *fakeCache) snapshot() []model.Envelope {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]model.Envelope, len(f.items))
	copy(out, f.items)
	return out
}

type fakeDB struct {
	mu    sync.Mutex
	items []model.Envelope
}

func (f *fakeDB) InsertEnvelope(_ context.Context, env model.Envelope) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.items = append(f.items, env)
	return nil
}

func (f *fakeDB) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.items)
}

func startTestServer(t *testing.T) (*Server, *fakeCache, *fakeDB, string) {
	t.Helper()
	cache := &fakeCache{}
	db := &fakeDB{}
	cfg := PipelineConfig{
		ListenAddr:      "127.0.0.1:0", // replaced below with real listener
		MaxConnections:  4,
		ChannelBuffer:   64,
		BatchSize:       4,
		BatchFlushEvery: 50 * time.Millisecond,
		WriteTimeout:    time.Second,
	}
	// Bind an ephemeral port up front so the client knows where to dial.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := NewServer(cfg, ice_analyzer.New(model.DefaultAnalysisParams()),
		cache, db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	})
	go func() { _ = srv.Serve(ln) }()
	return srv, cache, db, ln.Addr().String()
}

func TestIngestEndToEnd(t *testing.T) {
	srv, cache, db, addr := startTestServer(t)

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	const n = 20
	for i := 0; i < n; i++ {
		intensity := make([]byte, 128)
		for j := range intensity {
			intensity[j] = byte((j + i) % 256)
		}
		s := &model.Sweep{
			ID:      uint64(i + 1),
			Time:    time.Now().UTC(),
			ShipLat: 78, ShipLon: 15,
			HeadingDeg: 0, AzimuthDeg: 0,
			RangeStart: 10, RangeBin: 10,
			Intensity: intensity,
		}
		frame, err := echo_parser.EncodeFrame(s)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := conn.Write(frame); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if len(cache.snapshot()) == n && db.count() == n {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := len(cache.snapshot()); got != n {
		t.Fatalf("cached sweeps = %d, want %d", got, n)
	}
	if got := db.count(); got != n {
		t.Fatalf("persisted sweeps = %d, want %d", got, n)
	}
	st := srv.Snapshot()
	if st.FramesParsed != n {
		t.Fatalf("frames parsed = %d", st.FramesParsed)
	}

	// Every cached envelope must carry analysis results.
	for _, env := range cache.snapshot() {
		if env.Analysis == nil || env.Analysis.RangeBins != 128 {
			t.Fatalf("missing analysis for sweep %d", env.Sweep.ID)
		}
	}
}

func TestIngestSkipsCorruptBytes(t *testing.T) {
	_, cache, db, addr := startTestServer(t)
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	good, _ := echo_parser.EncodeFrame(&model.Sweep{
		ID: 1, Time: time.Now().UTC(), ShipLat: 78, ShipLon: 15,
		RangeStart: 1, RangeBin: 1, Intensity: make([]byte, 10),
	})
	if _, err := conn.Write(append([]byte("GARBAGE!!!"), good...)); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && db.count() == 0 {
		time.Sleep(10 * time.Millisecond)
	}
	if db.count() != 1 || len(cache.snapshot()) != 1 {
		t.Fatalf("expected one recovered frame: cache=%d db=%d",
			len(cache.snapshot()), db.count())
	}
}
