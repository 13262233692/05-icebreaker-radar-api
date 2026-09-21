package echoparser

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"time"
)

// Stats summarizes decoder activity, useful for observability.
type Stats struct {
	FramesOK       uint64
	Resyncs        uint64
	Malformed      uint64
	BytesProcessed uint64
}

// Reader incrementally decodes frames from an arbitrary byte stream.
// Corrupted bytes are skipped by resynchronizing on the next magic marker.
type Reader struct {
	br    *bufio.Reader
	stats Stats
}

func NewReader(r io.Reader) *Reader {
	return &Reader{br: bufio.NewReaderSize(r, 1<<20)}
}

// Stats returns a copy of the decoder counters.
func (r *Reader) Stats() Stats { return r.stats }

var magic = []byte{Magic0, Magic1, Magic2, Magic3}

// Next blocks until a complete frame is available. It returns io.EOF only when
// the stream is cleanly closed between frames.
func (r *Reader) Next() (*Frame, error) {
	for {
		if err := r.findMagic(); err != nil {
			return nil, err
		}

		// Magic (4) + version/type (2) + header length (2) + payload length (4) + ts (8).
		rest := make([]byte, HeaderLength-4)
		if _, err := io.ReadFull(r.br, rest); err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				return nil, io.EOF
			}
			return nil, fmt.Errorf("read frame header: %w", err)
		}

		version := rest[0]
		frameType := rest[1]
		headerLen := binary.BigEndian.Uint16(rest[2:4])
		payloadLen := binary.BigEndian.Uint32(rest[4:8])
		tsNanos := binary.BigEndian.Uint64(rest[8:16])

		if version != ProtocolVersion || headerLen != HeaderLength || payloadLen > MaxPayloadBytes {
			// Header is corrupt: drop the consumed magic and resync.
			r.stats.Malformed++
			r.stats.Resyncs++
			continue
		}

		payload := make([]byte, payloadLen)
		if _, err := io.ReadFull(r.br, payload); err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				return nil, io.EOF
			}
			return nil, fmt.Errorf("read frame payload: %w", err)
		}
		r.stats.BytesProcessed += uint64(HeaderLength) + uint64(payloadLen)

		// SweepID is derived from the frame timestamp for protocol compatibility
		// with heartbeats; persistence reassigns its own primary key.
		f := &Frame{
			Type:      frameType,
			Timestamp: time.Unix(0, int64(tsNanos)).UTC(),
		}
		f.SweepID = uint32(uint64(tsNanos) & 0xffffffff)
		if err := DecodePayload(f, payload); err != nil {
			r.stats.Malformed++
			continue
		}
		r.stats.FramesOK++
		return f, nil
	}
}

// findMagic consumes bytes until the magic marker is the next 4 bytes.
func (r *Reader) findMagic() error {
	matched := 0
	for matched < len(magic) {
		b, err := r.br.ReadByte()
		if err != nil {
			return io.EOF
		}
		r.stats.BytesProcessed++
		if b == magic[matched] {
			matched++
			continue
		}
		if matched > 0 {
			r.stats.Resyncs++
		}
		matched = 0
		if b == magic[0] {
			matched = 1
		}
	}
	return nil
}
