package model

import (
	"time"
)

// EchoFrame is one decoded radar sweep: a full azimuth scan of range bins.
type EchoFrame struct {
	FrameID    uint32    `json:"frame_id"`
	SweepID    uint32    `json:"sweep_id"`
	Timestamp  time.Time `json:"timestamp"`
	VesselLat  float64   `json:"vessel_lat"`
	VesselLon  float64   `json:"vessel_lon"`
	Heading    float64   `json:"heading_deg"`
	AzStart    float64   `json:"az_start_deg"`
	AzEnd      float64   `json:"az_end_deg"`
	BinSpacing float64   `json:"bin_spacing_m"`
	RangeBins  uint16    `json:"range_bins"`
	Intensity  []uint8   `json:"intensity"`
}

// FrameStats aggregates intensity statistics for one sweep.
type FrameStats struct {
	MeanIntensity    float64 `json:"mean_intensity"`
	MaxIntensity     uint8   `json:"max_intensity"`
	IceBins          int     `json:"ice_bins"`
	TotalBins        int     `json:"total_bins"`
	IceConcentration float64 `json:"ice_concentration"`
	RidgeCount       int     `json:"ridge_count"`
}

// Ridge is a detected high-backscatter cluster (ice ridge) inside a sweep.
type Ridge struct {
	SweepID       uint32  `json:"sweep_id"`
	FrameID       uint32  `json:"frame_id"`
	RidgeIdx      int     `json:"ridge_idx"`
	AzimuthDeg    float64 `json:"azimuth_deg"`
	StartBin      int     `json:"start_bin"`
	EndBin        int     `json:"end_bin"`
	PeakBin       int     `json:"peak_bin"`
	PeakIntensity uint8   `json:"peak_intensity"`
	MeanIntensity float64 `json:"mean_intensity"`
	RangeStartM   float64 `json:"range_start_m"`
	RangeEndM     float64 `json:"range_end_m"`
	RangePeakM    float64 `json:"range_peak_m"`
	Lat           float64 `json:"lat"`
	Lon           float64 `json:"lon"`
}

// AnalyzedFrame bundles a decoded frame with its derived statistics.
type AnalyzedFrame struct {
	Frame  EchoFrame
	Stats  FrameStats
	Ridges []Ridge
}

// CachedFrame is the Redis representation of a recent frame (full echo slice).
type CachedFrame struct {
	FrameID          uint32    `json:"frame_id"`
	SweepID          uint32    `json:"sweep_id"`
	Timestamp        time.Time `json:"timestamp"`
	VesselLat        float64   `json:"vessel_lat"`
	VesselLon        float64   `json:"vessel_lon"`
	Heading          float64   `json:"heading_deg"`
	AzStart          float64   `json:"az_start_deg"`
	AzEnd            float64   `json:"az_end_deg"`
	BinSpacing       float64   `json:"bin_spacing_m"`
	RangeBins        uint16    `json:"range_bins"`
	Intensity        []uint8   `json:"intensity"`
	MeanIntensity    float64   `json:"mean_intensity"`
	MaxIntensity     uint8     `json:"max_intensity"`
	IceConcentration float64   `json:"ice_concentration"`
	RidgeCount       int       `json:"ridge_count"`
}

// StatsRow is one persisted ice-statistics row returned by API queries.
type StatsRow struct {
	FrameID          uint32    `json:"frame_id"`
	SweepID          uint32    `json:"sweep_id"`
	Timestamp        time.Time `json:"timestamp"`
	VesselLat        float64   `json:"vessel_lat"`
	VesselLon        float64   `json:"vessel_lon"`
	AzStart          float64   `json:"az_start_deg"`
	AzEnd            float64   `json:"az_end_deg"`
	RangeBins        int       `json:"range_bins"`
	MeanIntensity    float64   `json:"mean_intensity"`
	MaxIntensity     uint8     `json:"max_intensity"`
	IceConcentration float64   `json:"ice_concentration"`
	RidgeCount       int       `json:"ridge_count"`
}

// RidgeRow is one persisted ridge row returned by API queries.
type RidgeRow struct {
	FrameID       uint32    `json:"frame_id"`
	SweepID       uint32    `json:"sweep_id"`
	RidgeIdx      int       `json:"ridge_idx"`
	Timestamp     time.Time `json:"timestamp"`
	AzimuthDeg    float64   `json:"azimuth_deg"`
	RangeStartM   float64   `json:"range_start_m"`
	RangeEndM     float64   `json:"range_end_m"`
	RangePeakM    float64   `json:"range_peak_m"`
	PeakIntensity uint8     `json:"peak_intensity"`
	MeanIntensity float64   `json:"mean_intensity"`
	Lat           float64   `json:"lat"`
	Lon           float64   `json:"lon"`
}

// QueryFilter constrains time-range and geo-rectangle API queries.
type QueryFilter struct {
	Start   time.Time
	End     time.Time
	MinLat  float64
	MaxLat  float64
	MinLon  float64
	MaxLon  float64
	HasBBox bool
	Limit   int
}
