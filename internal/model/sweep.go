package model

import "time"

// Sweep is one parsed radar echo slice: echo intensities along one ray.
type Sweep struct {
	ID         uint64    `json:"id"`
	Time       time.Time `json:"time"`
	ShipLat    float64   `json:"ship_lat"`
	ShipLon    float64   `json:"ship_lon"`
	HeadingDeg float64   `json:"heading_deg"`
	AzimuthDeg float32   `json:"azimuth_deg"`
	ElevDeg    float32   `json:"elevation_deg"`
	RangeStart float32   `json:"range_start_m"`
	RangeBin   float32   `json:"range_bin_m"`
	TxGainDb   float32   `json:"tx_gain_db"`
	Intensity  []byte    `json:"-"`
}

// RangeBins returns the number of echo samples in the slice.
func (s *Sweep) RangeBins() int { return len(s.Intensity) }

// MaxRangeM returns the slant range of the last bin in metres.
func (s *Sweep) MaxRangeM() float64 {
	if len(s.Intensity) == 0 {
		return float64(s.RangeStart)
	}
	return float64(s.RangeStart) + float64(len(s.Intensity)-1)*float64(s.RangeBin)
}

// TrueBearingDeg returns the ray bearing relative to true north.
func (s *Sweep) TrueBearingDeg() float64 {
	b := s.HeadingDeg + float64(s.AzimuthDeg)
	for b < 0 {
		b += 360
	}
	for b >= 360 {
		b -= 360
	}
	return b
}

// Envelope bundles a raw sweep with its derived ice analysis. It is the unit
// cached in Redis and handed to the persistence layer.
type Envelope struct {
	Sweep    *Sweep
	Analysis *SweepAnalysis
}
