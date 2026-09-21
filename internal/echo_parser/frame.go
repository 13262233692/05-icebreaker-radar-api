// Package echo_parser decodes binary radar echo frames from a TCP stream.
//
// The wire format is defined in internal/model/protocol.go. The parser is
// resync-safe: corrupt or truncated frames are skipped byte-by-byte until the
// next magic marker, so a single bad frame never desynchronises the stream.
package echo_parser

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
	"math"
	"time"

	"github.com/polar/icebreakerradar/internal/model"
)

var (
	// ErrBadVersion is returned when a frame advertises an unknown protocol.
	ErrBadVersion = errors.New("echo_parser: unsupported protocol version")
	// ErrFrameTooLarge is returned when RangeBins exceeds MaxRangeBins.
	ErrFrameTooLarge = errors.New("echo_parser: range bin count exceeds limit")
	// ErrCRC is returned when the trailing checksum does not match.
	ErrCRC = errors.New("echo_parser: crc32 mismatch")
)

var crcTable = crc32.MakeTable(crc32.IEEE)

// EncodeFrame serialises a sweep into one self-delimited wire frame.
func EncodeFrame(s *model.Sweep) ([]byte, error) {
	n := len(s.Intensity)
	if n > model.MaxRangeBins {
		return nil, ErrFrameTooLarge
	}
	buf := make([]byte, model.HeaderSize+n+model.CRCSize)
	copy(buf[0:4], model.FrameMagic)
	binary.LittleEndian.PutUint16(buf[4:6], model.ProtocolVersion)
	binary.LittleEndian.PutUint16(buf[6:8], 0) // flags
	binary.LittleEndian.PutUint64(buf[8:16], s.ID)
	binary.LittleEndian.PutUint64(buf[16:24], uint64(s.Time.UnixNano()))
	binary.LittleEndian.PutUint64(buf[24:32], math.Float64bits(s.ShipLat))
	binary.LittleEndian.PutUint64(buf[32:40], math.Float64bits(s.ShipLon))
	binary.LittleEndian.PutUint64(buf[40:48], math.Float64bits(s.HeadingDeg))
	binary.LittleEndian.PutUint32(buf[48:52], math.Float32bits(s.AzimuthDeg))
	binary.LittleEndian.PutUint32(buf[52:56], math.Float32bits(s.ElevDeg))
	binary.LittleEndian.PutUint32(buf[56:60], math.Float32bits(s.RangeStart))
	binary.LittleEndian.PutUint32(buf[60:64], math.Float32bits(s.RangeBin))
	binary.LittleEndian.PutUint32(buf[64:68], uint32(n))
	binary.LittleEndian.PutUint32(buf[68:72], math.Float32bits(s.TxGainDb))
	// buf[72:80] reserved, already zeroed
	copy(buf[model.HeaderSize:], s.Intensity)
	sum := crc32.Checksum(buf[:model.HeaderSize+n], crcTable)
	binary.LittleEndian.PutUint32(buf[model.HeaderSize+n:], sum)
	return buf, nil
}

func parseSweep(hdr, payload []byte) *model.Sweep {
	return &model.Sweep{
		ID:         binary.LittleEndian.Uint64(hdr[8:16]),
		Time:       time.Unix(0, int64(binary.LittleEndian.Uint64(hdr[16:24]))).UTC(),
		ShipLat:    math.Float64frombits(binary.LittleEndian.Uint64(hdr[24:32])),
		ShipLon:    math.Float64frombits(binary.LittleEndian.Uint64(hdr[32:40])),
		HeadingDeg: math.Float64frombits(binary.LittleEndian.Uint64(hdr[40:48])),
		AzimuthDeg: math.Float32frombits(binary.LittleEndian.Uint32(hdr[48:52])),
		ElevDeg:    math.Float32frombits(binary.LittleEndian.Uint32(hdr[52:56])),
		RangeStart: math.Float32frombits(binary.LittleEndian.Uint32(hdr[56:60])),
		RangeBin:   math.Float32frombits(binary.LittleEndian.Uint32(hdr[60:64])),
		TxGainDb:   math.Float32frombits(binary.LittleEndian.Uint32(hdr[68:72])),
		Intensity:  payload,
	}
}
