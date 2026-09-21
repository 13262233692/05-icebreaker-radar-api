package model

// Wire protocol for a single radar echo slice (one azimuth ray).
//
// All multi-byte integers are little-endian. Layout:
//
//	header  (HeaderSize bytes, fixed)
//	payload (RangeBins bytes, one uint8 echo intensity per range bin)
//	crc32   (4 bytes, IEEE polynomial over header+payload)
//
// Header fields:
//
//	magic        [4]byte  = FrameMagic ("ICBR")
//	version      uint16
//	flags        uint16
//	sweep_id     uint64
//	timestamp_ns int64    (UTC unix nanoseconds)
//	ship_lat     float64  (degrees, WGS-84)
//	ship_lon     float64  (degrees, WGS-84)
//	heading_deg  float64  (ship heading, true north, 0..360)
//	azimuth_deg  float32  (ray azimuth relative to ship heading)
//	elev_deg     float32  (antenna elevation)
//	range_start  float32  (metres, first range bin)
//	range_bin    float32  (metres, bin spacing)
//	range_bins   uint32
//	tx_gain_db   float32
//	reserved     [16]byte

const (
	// FrameMagic marks the start of an echo frame.
	FrameMagic = "ICBR"
	// ProtocolVersion is the only wire version accepted by the parser.
	ProtocolVersion uint16 = 1
	// HeaderSize is the fixed binary header length in bytes.
	HeaderSize = 80
	// CRCSize is the trailing checksum length.
	CRCSize = 4
	// MaxRangeBins bounds memory used by a single frame.
	MaxRangeBins = 16384
)
