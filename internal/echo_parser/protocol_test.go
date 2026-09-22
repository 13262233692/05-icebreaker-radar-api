package echo_parser

import (
	"bufio"
	"bytes"
	"io"
	"testing"
	"time"

	"github.com/icebreaker/ice-radar-api/internal/model"
)

func sampleFrame() *model.EchoFrame {
	return &model.EchoFrame{
		FrameID:    42,
		SweepID:    7,
		Timestamp:  time.Unix(1_700_000_000, 123).UTC(),
		VesselLat:  78.2293,
		VesselLon:  15.6112,
		Heading:    12.5,
		BinSpacing: 7.5,
		AzStart:    0,
		AzEnd:      360,
		RangeBins:  8,
		Intensity:  []uint8{10, 20, 200, 210, 220, 50, 60, 90},
	}
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	orig := sampleFrame()
	packet, err := EncodeFrame(orig)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	got, err := ReadFrame(bytes.NewReader(packet))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.FrameID != orig.FrameID || got.SweepID != orig.SweepID {
		t.Fatalf("ids mismatch: %+v", got)
	}
	if !got.Timestamp.Equal(orig.Timestamp) {
		t.Fatalf("timestamp mismatch: %v != %v", got.Timestamp, orig.Timestamp)
	}
	if got.BinSpacing != orig.BinSpacing || got.RangeBins != orig.RangeBins {
		t.Fatalf("geometry mismatch: %+v", got)
	}
	if !bytes.Equal(got.Intensity, orig.Intensity) {
		t.Fatalf("intensity mismatch: %v", got.Intensity)
	}
}

func TestReadFrameResyncsAfterGarbage(t *testing.T) {
	packet, err := EncodeFrame(sampleFrame())
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	var buf bytes.Buffer
	buf.Write([]byte{0x00, 0xFF, magic0, 0x00}) // noise incl. partial magic
	buf.Write(packet)

	got, err := ReadFrame(&buf)
	if err != nil {
		t.Fatalf("decode after garbage: %v", err)
	}
	if got.FrameID != 42 {
		t.Fatalf("unexpected frame: %+v", got)
	}
}

func TestReadFrameRejectsBadCRC(t *testing.T) {
	packet, _ := EncodeFrame(sampleFrame())
	packet[len(packet)-1] ^= 0xFF

	// CRC failure returns an error; append a valid second frame to prove the
	// stream can continue after resync.
	packet = append(packet, mustEncode(sampleFrame2())...)

	got, err := ReadFrame(bytes.NewReader(packet))
	if err == nil {
		t.Fatalf("expected CRC error, got frame %+v", got)
	}
}

func TestReadTwoFrames(t *testing.T) {
	p1, _ := EncodeFrame(sampleFrame())
	p2, _ := EncodeFrame(sampleFrame2())
	r := bufio.NewReader(bytes.NewReader(append(p1, p2...)))

	if _, err := ReadFrame(r); err != nil {
		t.Fatalf("frame 1: %v", err)
	}
	f2, err := ReadFrame(r)
	if err != nil {
		t.Fatalf("frame 2: %v", err)
	}
	if f2.FrameID != 43 {
		t.Fatalf("frame 2 id = %d", f2.FrameID)
	}
	if _, err := ReadFrame(r); err != io.EOF {
		t.Fatalf("expected EOF, got %v", err)
	}
}

func TestEncodeRejectsMismatchedLength(t *testing.T) {
	f := sampleFrame()
	f.RangeBins = 16
	if _, err := EncodeFrame(f); err == nil {
		t.Fatal("expected length mismatch error")
	}
}

func sampleFrame2() *model.EchoFrame {
	f := sampleFrame()
	f.FrameID = 43
	return f
}

func mustEncode(f *model.EchoFrame) []byte {
	b, err := EncodeFrame(f)
	if err != nil {
		panic(err)
	}
	return b
}
