package echo_parser

import (
	"encoding/binary"
	"io"
)

// StreamReader 从 TCP 流中按帧边界拆包。
// 协议为定长头 + 变长采样体，先读 30 字节头，再按 SampleCount 读采样数据。
type StreamReader struct {
	r   io.Reader
	buf []byte
}

func NewStreamReader(r io.Reader) *StreamReader {
	return &StreamReader{r: r, buf: make([]byte, 0, 64*1024)}
}

// Next 读取并解析下一帧。返回 io.EOF 表示对端正常关闭。
func (s *StreamReader) Next() (*EchoFrame, error) {
	header := make([]byte, HeaderLen)
	if _, err := io.ReadFull(s.r, header); err != nil {
		return nil, err
	}
	if binary.BigEndian.Uint16(header[0:2]) != MagicNumber {
		return nil, ErrBadMagic
	}
	sampleCount := int(binary.BigEndian.Uint16(header[28:30]))
	if sampleCount > MaxSamples {
		return nil, ErrTooManySample
	}
	body := make([]byte, sampleCount*2)
	if _, err := io.ReadFull(s.r, body); err != nil {
		return nil, err
	}
	frame := make([]byte, 0, HeaderLen+len(body))
	frame = append(frame, header...)
	frame = append(frame, body...)
	return Decode(frame)
}
