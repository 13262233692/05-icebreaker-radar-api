// Package echo_parser decodes the binary radar echo wire protocol.
//
// Frame layout (little-endian):
//
//	offset  size  field
//	0       2     magic 0x49 0x52 ("IR")
//	2       1     protocol version (1)
//	3       1     reserved
//	4       4     frame_id        uint32
//	8       4     sweep_id        uint32
//	12      8     timestamp_ns    int64  (Unix nanoseconds)
//	20      8     vessel_lat      float64
//	28      8     vessel_lon      float64
//	36      8     heading_deg     float64
//	44      4     bin_spacing_m   float32
//	48      4     az_start_deg    float32
//	52      4     az_end_deg      float32
//	56      2     range_bins      uint16
//	58      4     reserved        uint32
//	62      N     intensity[N]    uint8 per range bin
//	62+N    4     CRC32-IEEE over the header and payload
package echo_parser

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"time"

	"github.com/icebreaker/ice-radar-api/internal/model"
)

const (
	magic0    byte = 0x49 // 'I'
	magic1    byte = 0x52 // 'R'
	version   byte = 1
	HeaderLen      = 62
	CRCLen         = 4
	MaxBins        = 4096
)

var (
	ErrBadMagic     = errors.New("echo_parser: bad frame magic")
	ErrBadVersion   = errors.New("echo_parser: unsupported protocol version")
	ErrBadCRC       = errors.New("echo_parser: crc32 mismatch")
	ErrFrameTooBig  = errors.New("echo_parser: range_bins exceeds maximum")
	ErrInvalidFrame = errors.New("echo_parser: invalid frame fields")
)

// EncodeFrame serializes one echo frame into a complete binary datagram.
func EncodeFrame(f *model.EchoFrame) ([]byte, error) {
	if f == nil {
		return nil, fmt.Errorf("%w: nil frame", ErrInvalidFrame)
	}
	n := int(f.RangeBins)
	if n == 0 {
		n = len(f.Intensity)
	}
	if n <= 0 || n > MaxBins {
		return nil, fmt.Errorf("%w: range_bins=%d", ErrFrameTooBig, n)
	}
	if len(f.Intensity) != n {
		return nil, fmt.Errorf("%w: intensity length %d != range_bins %d", ErrInvalidFrame, len(f.Intensity), n)
	}

	buf := make([]byte, HeaderLen+n+CRCLen)
	buf[0] = magic0
	buf[1] = magic1
	buf[2] = version
	binary.LittleEndian.PutUint32(buf[4:], f.FrameID)
	binary.LittleEndian.PutUint32(buf[8:], f.SweepID)
	binary.LittleEndian.PutUint64(buf[12:], uint64(f.Timestamp.UnixNano()))
	binary.LittleEndian.PutUint64(buf[20:], math.Float64bits(f.VesselLat))
	binary.LittleEndian.PutUint64(buf[28:], math.Float64bits(f.VesselLon))
	binary.LittleEndian.PutUint64(buf[36:], math.Float64bits(f.Heading))
	binary.LittleEndian.PutUint32(buf[44:], math.Float32bits(float32(f.BinSpacing)))
	binary.LittleEndian.PutUint32(buf[48:], math.Float32bits(float32(f.AzStart)))
	binary.LittleEndian.PutUint32(buf[52:], math.Float32bits(float32(f.AzEnd)))
	binary.LittleEndian.PutUint16(buf[56:], uint16(n))
	copy(buf[HeaderLen:HeaderLen+n], f.Intensity)

	crc := crc32Checksum(buf[:HeaderLen+n])
	binary.LittleEndian.PutUint32(buf[HeaderLen+n:], crc)
	return buf, nil
}

// ReadFrame reads and validates the next frame, re-synchronizing on the magic
// bytes if the stream is misaligned. io.EOF is returned unwrapped on clean
// disconnect.
func ReadFrame(r io.Reader) (*model.EchoFrame, error) {
	br, ok := r.(*bufio.Reader)
	if !ok {
		br = bufio.NewReaderSize(r, HeaderLen+MaxBins+CRCLen)
	}

	if err := syncMagic(br); err != nil {
		return nil, err
	}

	header := make([]byte, HeaderLen)
	header[0], header[1] = magic0, magic1
	if _, err := io.ReadFull(br, header[2:]); err != nil {
		return nil, err
	}

	if header[2] != version {
		return nil, fmt.Errorf("%w: got %d", ErrBadVersion, header[2])
	}
	n := int(binary.LittleEndian.Uint16(header[56:]))
	if n <= 0 || n > MaxBins {
		return nil, fmt.Errorf("%w: range_bins=%d", ErrFrameTooBig, n)
	}

	body := make([]byte, n+CRCLen)
	if _, err := io.ReadFull(br, body); err != nil {
		return nil, err
	}

	payload := body[:n]
	wantCRC := binary.LittleEndian.Uint32(body[n:])
	gotCRC := crc32Checksum2(header, payload)
	if wantCRC != gotCRC {
		return nil, fmt.Errorf("%w: frame_id=%d", ErrBadCRC, binary.LittleEndian.Uint32(header[4:]))
	}

	f := &model.EchoFrame{
		FrameID:    binary.LittleEndian.Uint32(header[4:]),
		SweepID:    binary.LittleEndian.Uint32(header[8:]),
		Timestamp:  time.Unix(0, int64(binary.LittleEndian.Uint64(header[12:]))).UTC(),
		VesselLat:  math.Float64frombits(binary.LittleEndian.Uint64(header[20:])),
		VesselLon:  math.Float64frombits(binary.LittleEndian.Uint64(header[28:])),
		Heading:    math.Float64frombits(binary.LittleEndian.Uint64(header[36:])),
		BinSpacing: float64(math.Float32frombits(binary.LittleEndian.Uint32(header[44:]))),
		AzStart:    float64(math.Float32frombits(binary.LittleEndian.Uint32(header[48:]))),
		AzEnd:      float64(math.Float32frombits(binary.LittleEndian.Uint32(header[52:]))),
		RangeBins:  uint16(n),
		Intensity:  append([]byte(nil), payload...),
	}
	if err := validate(f); err != nil {
		return nil, err
	}
	return f, nil
}

func validate(f *model.EchoFrame) error {
	if f.Timestamp.IsZero() {
		return fmt.Errorf("%w: zero timestamp", ErrInvalidFrame)
	}
	if f.VesselLat < -90 || f.VesselLat > 90 || f.VesselLon < -180 || f.VesselLon > 180 {
		return fmt.Errorf("%w: vessel position out of range", ErrInvalidFrame)
	}
	if f.BinSpacing <= 0 {
		return fmt.Errorf("%w: bin_spacing must be positive", ErrInvalidFrame)
	}
	return nil
}

// syncMagic consumes bytes until the two-byte magic sequence is found.
func syncMagic(br *bufio.Reader) error {
	for {
		b, err := br.ReadByte()
		if err != nil {
			return err
		}
		if b != magic0 {
			continue
		}
		b, err = br.ReadByte()
		if err != nil {
			return err
		}
		if b == magic1 {
			return nil
		}
		if b == magic0 {
			// Second byte may itself start the next magic sequence.
			if err := br.UnreadByte(); err != nil {
				return err
			}
		}
	}
}
