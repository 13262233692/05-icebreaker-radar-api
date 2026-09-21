package model

import "time"

// AnalysisParams tunes ice / ridge classification. Echo intensity is an
// 8-bit value: 0 open water (specular reflection away from antenna),
// 255 strongest return.
type AnalysisParams struct {
	// IceThreshold classifies a bin as ice-covered at/above this intensity.
	IceThreshold int `json:"ice_threshold"`
	// RidgeThreshold classifies a bin as a ridge reflector at/above it.
	RidgeThreshold int `json:"ridge_threshold"`
	// MinRidgeBins is the minimum run length of strong bins to call a ridge.
	MinRidgeBins int `json:"min_ridge_bins"`
}

// DefaultAnalysisParams returns conservative sea-ice classification values.
func DefaultAnalysisParams() AnalysisParams {
	return AnalysisParams{
		IceThreshold:   100,
		RidgeThreshold: 170,
		MinRidgeBins:   3,
	}
}

// SweepAnalysis is the ice summary derived for one echo slice.
type SweepAnalysis struct {
	SweepID   uint64    `json:"sweep_id"`
	Time      time.Time `json:"time"`
	ShipLat   float64   `json:"ship_lat"`
	ShipLon   float64   `json:"ship_lon"`
	Bearing   float64   `json:"bearing_deg"`
	RangeBins int       `json:"range_bins"`
	MaxRangeM float64   `json:"max_range_m"`

	// Concentration is the fraction of valid bins classified as ice (0..1).
	Concentration float64 `json:"concentration"`
	// MeanIntensity averages all bins; MeanIceIntensity averages ice bins.
	MeanIntensity    float64 `json:"mean_intensity"`
	MeanIceIntensity float64 `json:"mean_ice_intensity"`
	MaxIntensity     int     `json:"max_intensity"`
	IceBins          int     `json:"ice_bins"`

	// Coverage bounding box of every bin, used for rectangle queries.
	MinLat float64 `json:"min_lat"`
	MaxLat float64 `json:"max_lat"`
	MinLon float64 `json:"min_lon"`
	MaxLon float64 `json:"max_lon"`

	Ridges []Ridge `json:"ridges"`
}

// Ridge is one pressure-ridge detection along a slice.
type Ridge struct {
	SweepID       uint64    `json:"sweep_id"`
	Time          time.Time `json:"time"`
	StartBin      int       `json:"start_bin"`
	EndBin        int       `json:"end_bin"`
	PeakBin       int       `json:"peak_bin"`
	StartRangeM   float64   `json:"start_range_m"`
	EndRangeM     float64   `json:"end_range_m"`
	WidthM        float64   `json:"width_m"`
	PeakIntensity int       `json:"peak_intensity"`
	MeanIntensity int       `json:"mean_intensity"`
	Lat           float64   `json:"lat"`
	Lon           float64   `json:"lon"`
}
