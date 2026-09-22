package echo_parser

import (
	"bytes"
	"testing"
	"time"
)

func TestEncodeDecodeRoundTrip(t *testing.T) {
	orig := &EchoFrame{
		Timestamp: time.Unix(1750000000, 123000000).UTC(),
		Latitude:  78.2232,
		Longitude: 15.6267,
		Samples:   []uint16{0, 1000, 65535, 42},
	}
	data, err := Encode(orig)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if len(data) != orig.FrameLen() {
		t.Fatalf("frame len = %d, want %d", len(data), orig.FrameLen())
	}
	got, err := Decode(data)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !got.Timestamp.Equal(orig.Timestamp) || got.Latitude != orig.Latitude || got.Longitude != orig.Longitude {
		t.Fatalf("header mismatch: %+v", got)
	}
	if !bytes.Equal(uint16Bytes(got.Samples), uint16Bytes(orig.Samples)) {
		t.Fatalf("samples mismatch: %v", got.Samples)
	}
}

func uint16Bytes(s []uint16) []byte {
	b := make([]byte, len(s)*2)
	for i, v := range s {
		b[i*2] = byte(v >> 8)
		b[i*2+1] = byte(v)
	}
	return b
}

func TestDecodeBadMagic(t *testing.T) {
	data, _ := Encode(&EchoFrame{Timestamp: time.Now(), Latitude: 1, Longitude: 1, Samples: []uint16{1}})
	data[0] = 0xFF
	if _, err := Decode(data); err != ErrBadMagic {
		t.Fatalf("want ErrBadMagic, got %v", err)
	}
}

func TestDecodeInvalidLatLon(t *testing.T) {
	f := &EchoFrame{Timestamp: time.Now(), Latitude: 91, Longitude: 0, Samples: []uint16{1}}
	data, _ := Encode(f)
	if _, err := Decode(data); err != ErrBadLatLon {
		t.Fatalf("want ErrBadLatLon, got %v", err)
	}
}

func TestStreamReaderMultipleFrames(t *testing.T) {
	var buf bytes.Buffer
	for i := 0; i < 3; i++ {
		f := &EchoFrame{
			Timestamp: time.Unix(int64(1000+i), 0).UTC(),
			Latitude:  78.0, Longitude: 15.0,
			Samples: []uint16{uint16(i), 2, 3},
		}
		data, _ := Encode(f)
		buf.Write(data)
	}
	sr := NewStreamReader(&buf)
	for i := 0; i < 3; i++ {
		f, err := sr.Next()
		if err != nil {
			t.Fatalf("frame %d: %v", i, err)
		}
		if f.Samples[0] != uint16(i) {
			t.Fatalf("frame %d first sample = %d", i, f.Samples[0])
		}
	}
	if _, err := sr.Next(); err == nil {
		t.Fatal("want EOF error after last frame")
	}
}
