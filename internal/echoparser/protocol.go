package echoparser

import (
	"encoding/binary"
	"fmt"
	"math"
	"time"
)

// Wire protocol (all multi-byte integers are big-endian / network order):
//
//	Header (20 bytes, fixed):
//	  [0:4]   magic    = "ICER" (0x49 0x43 0x45 0x52)
//	  [4]     version  = 1
//	  [5]     type     = 1 sweep, 2 heartbeat
//	  [6:8]   header length (=20)
//	  [8:12]  payload length (bytes)
//	  [12:20] timestamp, unix nanoseconds (uint64)
//
// Sweep frame (type=1) payload:
//
//	fixed sweep header (48 bytes) followed by ray_count*bins_per_ray uint8
//	echo-intensity samples in ray-major order.
const (
	Magic0 byte = 0x49 // 'I'
	Magic1 byte = 0x43 // 'C'
	Magic2 byte = 0x45 // 'E'
	Magic3 byte = 0x52 // 'R'

	ProtocolVersion uint8  = 1
	HeaderLength    uint16 = 20
	SweepHeaderLen         = 48

	FrameSweep     uint8 = 1
	FrameHeartbeat uint8 = 2

	MaxPayloadBytes = 64 * 1024 * 1024
)

// Frame is a decoded protocol frame. Sweep is non-nil only for type=FrameSweep.
type Frame struct {
	Type      uint8
	SweepID   uint32
	Timestamp time.Time
	Sweep     *Sweep
}

// Sweep is one full antenna revolution of echo intensity samples.
type Sweep struct {
	SweepID   uint32
	Timestamp time.Time

	ShipLat float64
	ShipLon float64

	RangeM          float32 // maximum sampled range
	BinSpacingM     float32
	StartBearingRad float32
	AngularStepRad  float32
	RadarFreqGHz    float32
	GainDB          float32
	Flags           uint32

	// Rays[row] holds bins_per_ray uint8 samples (0..255 echo intensity).
	Rays [][]byte
}

func (s *Sweep) RayCount() int { return len(s.Rays) }
func (s *Sweep) BinsPerRay() int {
	if len(s.Rays) == 0 {
		return 0
	}
	return len(s.Rays[0])
}

// EncodeFrame serializes a frame (sweep or heartbeat) in wire format.
func EncodeFrame(f *Frame) ([]byte, error) {
	var payload []byte
	switch f.Type {
	case FrameHeartbeat:
		payload = nil
	case FrameSweep:
		if f.Sweep == nil {
			return nil, fmt.Errorf("sweep frame missing payload")
		}
		var err error
		payload, err = encodeSweepPayload(f.Sweep)
		if err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("unknown frame type %d", f.Type)
	}

	if len(payload) > MaxPayloadBytes {
		return nil, fmt.Errorf("payload too large: %d bytes", len(payload))
	}

	out := make([]byte, int(HeaderLength)+len(payload))
	out[0] = Magic0
	out[1] = Magic1
	out[2] = Magic2
	out[3] = Magic3
	out[4] = ProtocolVersion
	out[5] = f.Type
	binary.BigEndian.PutUint16(out[6:8], HeaderLength)
	binary.BigEndian.PutUint32(out[8:12], uint32(len(payload)))
	binary.BigEndian.PutUint64(out[12:20], uint64(f.Timestamp.UnixNano()))
	copy(out[int(HeaderLength):], payload)
	return out, nil
}

func encodeSweepPayload(s *Sweep) ([]byte, error) {
	rays := s.RayCount()
	bins := s.BinsPerRay()
	if rays == 0 || bins == 0 {
		return nil, fmt.Errorf("sweep must contain at least one ray and one bin")
	}
	for _, ray := range s.Rays {
		if len(ray) != bins {
			return nil, fmt.Errorf("ragged ray data: want %d bins, got %d", bins, len(ray))
		}
	}
	if math.IsNaN(s.ShipLat) || math.IsNaN(s.ShipLon) {
		return nil, fmt.Errorf("ship position must not be NaN")
	}

	payload := make([]byte, SweepHeaderLen+rays*bins)
	putFloat64(payload[0:8], s.ShipLat)
	putFloat64(payload[8:16], s.ShipLon)
	putFloat32(payload[16:20], s.RangeM)
	binary.BigEndian.PutUint16(payload[20:22], uint16(rays))
	binary.BigEndian.PutUint16(payload[22:24], uint16(bins))
	putFloat32(payload[24:28], s.BinSpacingM)
	putFloat32(payload[28:32], s.StartBearingRad)
	putFloat32(payload[32:36], s.AngularStepRad)
	putFloat32(payload[36:40], s.RadarFreqGHz)
	putFloat32(payload[40:44], s.GainDB)
	binary.BigEndian.PutUint32(payload[44:48], s.Flags)

	off := SweepHeaderLen
	for _, ray := range s.Rays {
		copy(payload[off:], ray)
		off += bins
	}
	return payload, nil
}

// DecodePayload parses the payload of a header-validated frame.
func DecodePayload(f *Frame, payload []byte) error {
	switch f.Type {
	case FrameHeartbeat:
		if len(payload) != 0 {
			return fmt.Errorf("heartbeat frame must have empty payload, got %d bytes", len(payload))
		}
		return nil
	case FrameSweep:
		s, err := decodeSweepPayload(payload)
		if err != nil {
			return err
		}
		s.SweepID = f.SweepID
		s.Timestamp = f.Timestamp
		f.Sweep = s
		return nil
	default:
		// Unknown frame types are tolerated; the caller skips their payload.
		return nil
	}
}

func decodeSweepPayload(payload []byte) (*Sweep, error) {
	if len(payload) < SweepHeaderLen {
		return nil, fmt.Errorf("sweep payload too short: %d < %d", len(payload), SweepHeaderLen)
	}
	rays := int(binary.BigEndian.Uint16(payload[20:22]))
	bins := int(binary.BigEndian.Uint16(payload[22:24]))
	if rays == 0 || bins == 0 {
		return nil, fmt.Errorf("invalid sweep dimensions: rays=%d bins=%d", rays, bins)
	}
	want := SweepHeaderLen + rays*bins
	if len(payload) != want {
		return nil, fmt.Errorf("sweep payload length mismatch: got %d, want %d (rays=%d bins=%d)",
			len(payload), want, rays, bins)
	}

	s := &Sweep{
		ShipLat:         math.Float64frombits(binary.BigEndian.Uint64(payload[0:8])),
		ShipLon:         math.Float64frombits(binary.BigEndian.Uint64(payload[8:16])),
		RangeM:          math.Float32frombits(binary.BigEndian.Uint32(payload[16:20])),
		BinSpacingM:     math.Float32frombits(binary.BigEndian.Uint32(payload[24:28])),
		StartBearingRad: math.Float32frombits(binary.BigEndian.Uint32(payload[28:32])),
		AngularStepRad:  math.Float32frombits(binary.BigEndian.Uint32(payload[32:36])),
		RadarFreqGHz:    math.Float32frombits(binary.BigEndian.Uint32(payload[36:40])),
		GainDB:          math.Float32frombits(binary.BigEndian.Uint32(payload[40:44])),
		Flags:           binary.BigEndian.Uint32(payload[44:48]),
		Rays:            make([][]byte, rays),
	}

	off := SweepHeaderLen
	for i := 0; i < rays; i++ {
		s.Rays[i] = payload[off : off+bins : off+bins]
		off += bins
	}
	return s, nil
}

func putFloat64(b []byte, v float64) { binary.BigEndian.PutUint64(b, math.Float64bits(v)) }
func putFloat32(b []byte, v float32) { binary.BigEndian.PutUint32(b, math.Float32bits(v)) }
