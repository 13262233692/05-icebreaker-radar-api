package echo_parser

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"io"
	"sync/atomic"

	"github.com/polar/icebreakerradar/internal/model"
)

// FrameReader streams framed sweeps from a radar TCP connection.
//
// It tolerates garbage and corrupt frames: each magic-marked candidate is
// fully validated (version, size, CRC) with Peek before being consumed. A
// rejected candidate advances by one byte, so a false magic inside a bad frame
// can never desynchronise the stream or swallow the next valid frame.
type FrameReader struct {
	br      *bufio.Reader
	skipped uint64
}

// NewFrameReader wraps r in a 64 KiB buffered reader (several frames).
func NewFrameReader(r io.Reader) *FrameReader {
	return &FrameReader{br: bufio.NewReaderSize(r, 64*1024)}
}

// SkippedBytes reports bytes discarded while searching for valid frames.
func (r *FrameReader) SkippedBytes() uint64 {
	return atomic.LoadUint64(&r.skipped)
}

// Read returns the next valid sweep. io.EOF (or a connection error) is
// returned when no complete frame can be read.
func (r *FrameReader) Read() (*model.Sweep, error) {
	magic := []byte(model.FrameMagic)
	var hdr [model.HeaderSize]byte

	for {
		window, err := r.br.Peek(len(magic))
		if err != nil {
			return nil, err
		}
		if !bytes.Equal(window, magic) {
			_, _ = r.br.Discard(1)
			atomic.AddUint64(&r.skipped, 1)
			continue
		}

		// Candidate starts at buffer position 0. Header must be readable.
		head, err := r.br.Peek(model.HeaderSize)
		if err != nil {
			return nil, err
		}

		version := binary.LittleEndian.Uint16(head[4:6])
		bins := int(binary.LittleEndian.Uint32(head[64:68]))
		if version != model.ProtocolVersion || bins > model.MaxRangeBins {
			// This "ICBR" is not a frame start; skip past the marker.
			_, _ = r.br.Discard(1)
			atomic.AddUint64(&r.skipped, 1)
			continue
		}

		total := model.HeaderSize + bins + model.CRCSize
		full, err := r.br.Peek(total)
		if err != nil {
			// Truncated frame at end of stream: cannot be part of a valid
			// frame since the marker has been located; surface the error.
			return nil, err
		}

		want := binary.LittleEndian.Uint32(full[total-4:])
		got := crc32.Checksum(full[:total-4], crcTable)
		if want != got {
			// False magic / corrupted frame: advance one byte and rescan.
			_, _ = r.br.Discard(1)
			atomic.AddUint64(&r.skipped, 1)
			continue
		}

		// Valid frame: commit it.
		if _, err := io.ReadFull(r.br, hdr[:]); err != nil {
			return nil, err
		}
		payload := make([]byte, bins)
		if _, err := io.ReadFull(r.br, payload); err != nil {
			return nil, err
		}
		var crc [model.CRCSize]byte
		if _, err := io.ReadFull(r.br, crc[:]); err != nil {
			return nil, err
		}
		return parseSweep(hdr[:], payload), nil
	}
}
