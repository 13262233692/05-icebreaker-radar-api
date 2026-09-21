package echo_parser

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"testing"
	"time"

	"github.com/polar/icebreakerradar/internal/model"
)

func sampleSweep(t *testing.T, n int) *model.Sweep {
	t.Helper()
	buf := make([]byte, n)
	_, _ = rand.Read(buf)
	return &model.Sweep{
		ID:         42,
		Time:       time.Date(2026, 9, 21, 8, 0, 0, 0, time.UTC),
		ShipLat:    78.2,
		ShipLon:    15.1,
		HeadingDeg: 310,
		AzimuthDeg: 24,
		ElevDeg:    1.5,
		RangeStart: 50,
		RangeBin:   12.5,
		TxGainDb:   70,
		Intensity:  buf,
	}
}

func TestEncodeParseRoundTrip(t *testing.T) {
	want := sampleSweep(t, 256)
	frame, err := EncodeFrame(want)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if len(frame) != model.HeaderSize+256+model.CRCSize {
		t.Fatalf("frame size = %d", len(frame))
	}
	got := NewFrameReader(bytes.NewReader(frame))
	s, err := got.Read()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if s.ID != want.ID || !s.Time.Equal(want.Time) {
		t.Fatalf("header mismatch: %+v", s)
	}
	if s.ShipLat != want.ShipLat || s.ShipLon != want.ShipLon ||
		s.HeadingDeg != want.HeadingDeg || s.AzimuthDeg != want.AzimuthDeg {
		t.Fatalf("nav mismatch: %+v", s)
	}
	if !bytes.Equal(s.Intensity, want.Intensity) {
		t.Fatal("payload mismatch")
	}
}

func TestStreamMultipleFrames(t *testing.T) {
	var buf bytes.Buffer
	for i := 0; i < 5; i++ {
		f, err := EncodeFrame(sampleSweep(t, 64+i))
		if err != nil {
			t.Fatal(err)
		}
		buf.Write(f)
	}
	r := NewFrameReader(&buf)
	for i := 0; i < 5; i++ {
		s, err := r.Read()
		if err != nil {
			t.Fatalf("frame %d: %v", i, err)
		}
		if n := s.RangeBins(); n != 64+i {
			t.Fatalf("frame %d bins = %d", i, n)
		}
	}
}

func TestResyncAfterGarbage(t *testing.T) {
	good, err := EncodeFrame(sampleSweep(t, 32))
	if err != nil {
		t.Fatal(err)
	}
	// Garbage prefix containing a magic fragment that is not a real frame,
	// followed by a valid frame.
	prefix := append([]byte{0xFF, 0xEE}, []byte(model.FrameMagic)...)
	prefix = append(prefix, 0xAA, 0xBB, 0xCC)

	r := NewFrameReader(bytes.NewReader(append(prefix, good...)))
	s, err := r.Read()
	if err != nil {
		t.Fatalf("should resync to valid frame: %v", err)
	}
	if s.ID != 42 {
		t.Fatalf("got sweep %d", s.ID)
	}
	if r.SkippedBytes() == 0 {
		t.Fatal("expected skipped byte counter to advance")
	}
}

func TestCRCFailureThenRecovery(t *testing.T) {
	bad, err := EncodeFrame(sampleSweep(t, 16))
	if err != nil {
		t.Fatal(err)
	}
	bad[model.HeaderSize] ^= 0xFF // corrupt a payload byte
	good, err := EncodeFrame(sampleSweep(t, 16))
	if err != nil {
		t.Fatal(err)
	}
	r := NewFrameReader(bytes.NewReader(append(bad, good...)))
	s, err := r.Read()
	if err != nil {
		t.Fatalf("bad frame should be skipped, got err: %v", err)
	}
	if s.ID != 42 {
		t.Fatalf("expected recovered frame, got %d", s.ID)
	}
}

func TestRejectsUnknownVersion(t *testing.T) {
	f, _ := EncodeFrame(sampleSweep(t, 8))
	binary.LittleEndian.PutUint16(f[4:6], 999)
	good, _ := EncodeFrame(sampleSweep(t, 8))
	r := NewFrameReader(bytes.NewReader(append(f, good...)))
	if _, err := r.Read(); err != nil {
		t.Fatalf("unknown-version frame should be skipped not fatal: %v", err)
	}
}
