// Package ice_analyzer derives sea-ice statistics from decoded radar sweeps:
// echo intensity stats, sea-ice concentration and ice-ridge detections.
package ice_analyzer

import (
	"math"

	"github.com/icebreaker/ice-radar-api/internal/model"
)

// Config holds detection thresholds.
type Config struct {
	// IceThreshold: bins at/above this level count as ice-covered.
	IceThreshold uint8
	// RidgeThreshold: bins at/above this level may belong to an ice ridge.
	RidgeThreshold uint8
	// RidgeMinBins: minimum total strong bins inside a contiguous run.
	RidgeMinBins int
	// RidgeMinRun: minimum contiguous high-backscatter bins for a ridge.
	RidgeMinRun int
}

// Analyzer performs threshold based ice analysis.
type Analyzer struct {
	cfg Config
}

func New(cfg Config) *Analyzer {
	if cfg.IceThreshold == 0 {
		cfg.IceThreshold = 120
	}
	if cfg.RidgeThreshold == 0 {
		cfg.RidgeThreshold = 200
	}
	if cfg.RidgeMinBins <= 0 {
		cfg.RidgeMinBins = 4
	}
	if cfg.RidgeMinRun <= 0 {
		cfg.RidgeMinRun = 2
	}
	return &Analyzer{cfg: cfg}
}

// Analyze computes per-sweep statistics and ridge detections.
func (a *Analyzer) Analyze(f *model.EchoFrame) model.AnalyzedFrame {
	n := len(f.Intensity)
	stats := model.FrameStats{TotalBins: n}

	var sum uint64
	var maxVal uint8
	for _, v := range f.Intensity {
		sum += uint64(v)
		if v > maxVal {
			maxVal = v
		}
		if v >= a.cfg.IceThreshold {
			stats.IceBins++
		}
	}
	if n > 0 {
		stats.MeanIntensity = float64(sum) / float64(n)
		stats.MaxIntensity = maxVal
		stats.IceConcentration = float64(stats.IceBins) / float64(n)
	}

	ridges := a.detectRidges(f)
	stats.RidgeCount = len(ridges)

	return model.AnalyzedFrame{
		Frame:  *f,
		Stats:  stats,
		Ridges: ridges,
	}
}

func (a *Analyzer) detectRidges(f *model.EchoFrame) []model.Ridge {
	var ridges []model.Ridge

	start := -1
	var runSum uint64
	peakBin, peakVal := 0, uint8(0)

	flush := func(end int) {
		length := end - start
		if length < a.cfg.RidgeMinRun || (end-start) < a.cfg.RidgeMinBins {
			return
		}
		az := binAzimuth(f, start, end)
		peakRange := float64(peakBin)*f.BinSpacing + f.BinSpacing/2
		lat, lon := destinationPoint(
			f.VesselLat, f.VesselLon,
			wrap360(f.Heading+az),
			peakRange,
		)
		ridges = append(ridges, model.Ridge{
			SweepID:       f.SweepID,
			FrameID:       f.FrameID,
			RidgeIdx:      len(ridges),
			AzimuthDeg:    az,
			StartBin:      start,
			EndBin:        end - 1,
			PeakBin:       peakBin,
			PeakIntensity: peakVal,
			MeanIntensity: float64(runSum) / float64(length),
			RangeStartM:   float64(start) * f.BinSpacing,
			RangeEndM:     float64(end) * f.BinSpacing,
			RangePeakM:    peakRange,
			Lat:           lat,
			Lon:           lon,
		})
	}

	for i, v := range f.Intensity {
		if v >= a.cfg.RidgeThreshold {
			if start < 0 {
				start = i
				runSum = 0
				peakBin, peakVal = i, v
			}
			runSum += uint64(v)
			if v > peakVal {
				peakVal, peakBin = v, i
			}
			continue
		}
		if start >= 0 {
			flush(i)
			start = -1
		}
	}
	if start >= 0 {
		flush(len(f.Intensity))
	}
	return ridges
}

// binAzimuth maps a range-bin span to an absolute azimuth within the sweep
// sector, handling sectors that wrap across 0/360 degrees.
func binAzimuth(f *model.EchoFrame, start, end int) float64 {
	n := float64(len(f.Intensity))
	midFrac := (float64(start) + float64(end)) / 2 / n

	az0, az1 := f.AzStart, f.AzEnd
	var span float64
	if az1 >= az0 {
		span = az1 - az0
	} else {
		span = az1 + 360 - az0
	}
	return wrap360(az0 + span*midFrac)
}

func wrap360(d float64) float64 {
	d = math.Mod(d, 360)
	if d < 0 {
		d += 360
	}
	return d
}
