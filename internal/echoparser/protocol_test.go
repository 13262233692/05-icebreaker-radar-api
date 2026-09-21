package echoparser

import (
	"bytes"
	"io"
	"testing"
	"time"
)

func makeSweep(t *testing.T) *Sweep {
	t.Helper()
	s := &Sweep{
		SweepID:         42,
		Timestamp:       time.Unix(1_700_000_000, 123).UTC(),
		ShipLat:         69.6492,
		ShipLon:         18.4670,
		RangeM:          6000,
		BinSpacingM:     15,
		StartBearingRad: 0,
		AngularStepRad:  0.01745,
		RadarFreqGHz:    9.4,
		GainDB:          24,
		Rays:            make([][]byte, 8),
	}
	for r := range s.Rays {
		s.Rays[r] = make([]byte, 16)
		for b := range s.Rays[r] {
			s.Rays[r][b] = byte((r*16 + b) % 256)
		}
	}
	return s
}

func TestRoundTripSweep(t *testing.T) {
	orig := makeSweep(t)
	wire, err := EncodeFrame(&Frame{Type: FrameSweep, SweepID: 42, Timestamp: orig.Timestamp, Sweep: orig})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if len(wire) != int(HeaderLength)+SweepHeaderLen+8*16 {
		t.Fatalf("unexpected wire length %d", len(wire))
	}

	r := NewReader(bytes.NewReader(wire))
	f, err := r.Next()
	if err != nil {
		t.Fatalf("next: %v", err)
	}
	if f.Type != FrameSweep || f.Sweep == nil {
		t.Fatalf("bad frame: %+v", f)
	}
	got := f.Sweep
	if got.ShipLat != orig.ShipLat || got.ShipLon != orig.ShipLon {
		t.Fatalf("position mismatch: %v,%v", got.ShipLat, got.ShipLon)
	}
	if got.RangeM != orig.RangeM || got.BinSpacingM != orig.BinSpacingM {
		t.Fatalf("range params mismatch")
	}
	if got.RayCount() != 8 || got.BinsPerRay() != 16 {
		t.Fatalf("dims mismatch: %dx%d", got.RayCount(), got.BinsPerRay())
	}
	for i := 0; i < 8; i++ {
		for j := 0; j < 16; j++ {
			if got.Rays[i][j] != orig.Rays[i][j] {
				t.Fatalf("sample %d,%d = %d want %d", i, j, got.Rays[i][j], orig.Rays[i][j])
			}
		}
	}
}

func TestHeartbeatAndEOF(t *testing.T) {
	ts := time.Now().UTC()
	hb, err := EncodeFrame(&Frame{Type: FrameHeartbeat, Timestamp: ts})
	if err != nil {
		t.Fatal(err)
	}
	r := NewReader(bytes.NewReader(hb))
	f, err := r.Next()
	if err != nil {
		t.Fatal(err)
	}
	if f.Type != FrameHeartbeat || f.Sweep != nil {
		t.Fatalf("bad heartbeat: %+v", f)
	}
	if _, err := r.Next(); err != io.EOF {
		t.Fatalf("want EOF, got %v", err)
	}
}

func TestResyncAfterGarbage(t *testing.T) {
	orig := makeSweep(t)
	wire, err := EncodeFrame(&Frame{Type: FrameSweep, Timestamp: orig.Timestamp, Sweep: orig})
	if err != nil {
		t.Fatal(err)
	}
	// Leading junk containing decoy bytes but no full magic.
	junk := []byte{0x00, 0xFF, 0x49, 0x43, 0x00, 0x12, 0x34, Magic0, Magic1, Magic2}
	stream := append(junk, wire...)

	r := NewReader(bytes.NewReader(stream))
	f, err := r.Next()
	if err != nil {
		t.Fatalf("did not resync: %v", err)
	}
	if f.Sweep == nil || f.Sweep.RayCount() != 8 {
		t.Fatalf("bad sweep after resync: %+v", f)
	}
	if r.Stats().Resyncs == 0 {
		t.Fatalf("expected resync counter to increment, stats=%+v", r.Stats())
	}
}

func TestRejectTruncatedPayload(t *testing.T) {
	orig := makeSweep(t)
	wire, _ := EncodeFrame(&Frame{Type: FrameSweep, Timestamp: orig.Timestamp, Sweep: orig})
	// Truncate mid-payload; reader must report EOF, not a bogus frame.
	r := NewReader(bytes.NewReader(wire[:len(wire)-40]))
	if _, err := r.Next(); err != io.EOF {
		t.Fatalf("want EOF on truncated payload, got %v", err)
	}
}

func TestRejectRaggedRays(t *testing.T) {
	s := makeSweep(t)
	s.Rays[3] = s.Rays[3][:10]
	if _, err := EncodeFrame(&Frame{Type: FrameSweep, Timestamp: time.Now(), Sweep: s}); err == nil {
		t.Fatal("expected encode error on ragged rays")
	}
}
