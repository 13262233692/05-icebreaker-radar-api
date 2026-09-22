package echo_parser

import (
	"hash/crc32"
)

func crc32Checksum(b []byte) uint32 {
	return crc32.ChecksumIEEE(b)
}

func crc32Checksum2(a, b []byte) uint32 {
	h := crc32.NewIEEE()
	h.Write(a)
	h.Write(b)
	return h.Sum32()
}
