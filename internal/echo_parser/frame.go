// Package echo_parser 定义雷达回波二进制帧协议并提供编解码能力。
//
// 帧布局（大端序）：
//
//	+--------+---------+----------+-----------+-----------+-----------+--------------+----------+
//	| Magic  | Version | MsgType  | Timestamp | Latitude  | Longitude | SampleCount  | Samples  |
//	| 2B     | 1B      | 1B       | 8B (ns)   | 8B f64    | 8B f64    | 2B (uint16)  | N*2B u16 |
//	+--------+---------+----------+-----------+-----------+-----------+--------------+----------+
//
// Magic 固定为 0x4943 ("IC")，Version 当前为 1，MsgType 0x01 表示回波帧。
// 每个采样点为 16bit 回波强度（0-65535）。
package echo_parser

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"time"
)

const (
	MagicNumber  uint16 = 0x4943 // "IC"
	ProtocolVer  uint8  = 1
	MsgTypeEcho  uint8  = 0x01
	HeaderLen           = 2 + 1 + 1 + 8 + 8 + 8 + 2 // 30 字节
	MaxSamples          = 4096
	MaxFrameLen         = HeaderLen + MaxSamples*2
	MaxIntensity        = 65535
)

var (
	ErrBadMagic      = errors.New("echo_parser: bad magic number")
	ErrBadVersion    = errors.New("echo_parser: unsupported protocol version")
	ErrBadMsgType    = errors.New("echo_parser: unsupported message type")
	ErrFrameTooShort = errors.New("echo_parser: frame too short")
	ErrTooManySample = errors.New("echo_parser: sample count exceeds limit")
	ErrBadLatLon     = errors.New("echo_parser: invalid latitude/longitude")
)

// EchoFrame 是一帧解析后的雷达回波数据。
type EchoFrame struct {
	Timestamp time.Time `json:"timestamp"`
	Latitude  float64   `json:"latitude"`
	Longitude float64   `json:"longitude"`
	Samples   []uint16  `json:"samples"`
}

// FrameLen 返回该帧序列化后的总字节数。
func (f *EchoFrame) FrameLen() int { return HeaderLen + len(f.Samples)*2 }

// Encode 将回波帧序列化为协议字节流（供雷达前端/模拟器使用）。
func Encode(f *EchoFrame) ([]byte, error) {
	if len(f.Samples) > MaxSamples {
		return nil, ErrTooManySample
	}
	buf := make([]byte, f.FrameLen())
	binary.BigEndian.PutUint16(buf[0:2], MagicNumber)
	buf[2] = ProtocolVer
	buf[3] = MsgTypeEcho
	binary.BigEndian.PutUint64(buf[4:12], uint64(f.Timestamp.UnixNano()))
	binary.BigEndian.PutUint64(buf[12:20], math.Float64bits(f.Latitude))
	binary.BigEndian.PutUint64(buf[20:28], math.Float64bits(f.Longitude))
	binary.BigEndian.PutUint16(buf[28:30], uint16(len(f.Samples)))
	for i, s := range f.Samples {
		binary.BigEndian.PutUint16(buf[HeaderLen+i*2:], s)
	}
	return buf, nil
}

// Decode 从完整帧字节切片解析回波帧。data 必须恰好包含一帧。
func Decode(data []byte) (*EchoFrame, error) {
	if len(data) < HeaderLen {
		return nil, ErrFrameTooShort
	}
	if binary.BigEndian.Uint16(data[0:2]) != MagicNumber {
		return nil, ErrBadMagic
	}
	if data[2] != ProtocolVer {
		return nil, fmt.Errorf("%w: %d", ErrBadVersion, data[2])
	}
	if data[3] != MsgTypeEcho {
		return nil, fmt.Errorf("%w: 0x%02x", ErrBadMsgType, data[3])
	}
	sampleCount := int(binary.BigEndian.Uint16(data[28:30]))
	if sampleCount > MaxSamples {
		return nil, ErrTooManySample
	}
	if len(data) < HeaderLen+sampleCount*2 {
		return nil, ErrFrameTooShort
	}
	lat := math.Float64frombits(binary.BigEndian.Uint64(data[12:20]))
	lon := math.Float64frombits(binary.BigEndian.Uint64(data[20:28]))
	if lat < -90 || lat > 90 || lon < -180 || lon > 180 {
		return nil, ErrBadLatLon
	}
	frame := &EchoFrame{
		Timestamp: time.Unix(0, int64(binary.BigEndian.Uint64(data[4:12]))).UTC(),
		Latitude:  lat,
		Longitude: lon,
		Samples:   make([]uint16, sampleCount),
	}
	for i := 0; i < sampleCount; i++ {
		frame.Samples[i] = binary.BigEndian.Uint16(data[HeaderLen+i*2:])
	}
	return frame, nil
}
