// Package ice_analyzer turns raw radar echo slices into sea-ice statistics:
// ice concentration and pressure-ridge detections with geo-locations.
package ice_analyzer

import (
	"math"

	"github.com/polar/icebreakerradar/internal/model"
)

// Analyzer performs pure-functional analysis; it is safe for concurrent use
// because AnalysisParams is treated as read-only after construction.
type Analyzer struct {
	params model.AnalysisParams
}

// New builds an Analyzer. Invalid params fall back to the defaults.
func New(params model.AnalysisParams) *Analyzer {
	d := model.DefaultAnalysisParams()
	if params.IceThreshold <= 0 || params.IceThreshold > 255 {
		params.IceThreshold = d.IceThreshold
	}
	if params.RidgeThreshold < params.IceThreshold {
		params.RidgeThreshold = d.RidgeThreshold
	}
	if params.MinRidgeBins <= 0 {
		params.MinRidgeBins = d.MinRidgeBins
	}
	return &Analyzer{params: params}
}

// Analyze computes ice concentration, summary statistics, ridge positions and
// the slice coverage box for one sweep.
func (a *Analyzer) Analyze(s *model.Sweep) *model.SweepAnalysis {
	bearing := s.TrueBearingDeg()
	res := &model.SweepAnalysis{
		SweepID:   s.ID,
		Time:      s.Time,
		ShipLat:   s.ShipLat,
		ShipLon:   s.ShipLon,
		Bearing:   bearing,
		RangeBins: len(s.Intensity),
		MaxRangeM: s.MaxRangeM(),
	}

	var sum, iceSum, maxVal int
	minBins := 1
	// Seed coverage with the ship position so zero/short slices still have a
	// finite box.
	res.MinLat, res.MaxLat = s.ShipLat, s.ShipLat
	res.MinLon, res.MaxLon = s.ShipLon, s.ShipLon

	for i, v := range s.Intensity {
		sum += int(v)
		if int(v) > maxVal {
			maxVal = int(v)
		}
		if int(v) >= a.params.IceThreshold {
			res.IceBins++
			iceSum += int(v)
		}
		// Sampling every bin is cheap (max 16k); keep the coverage box exact.
		rangeM := float64(s.RangeStart) + float64(i)*float64(s.RangeBin)
		lat, lon := model.BinGeoPoint(s.ShipLat, s.ShipLon, bearing, rangeM)
		growBox(res, lat, lon)
	}
	if len(s.Intensity) > minBins {
		res.MeanIntensity = float64(sum) / float64(len(s.Intensity))
		res.Concentration = float64(res.IceBins) / float64(len(s.Intensity))
	}
	if res.IceBins > 0 {
		res.MeanIceIntensity = float64(iceSum) / float64(res.IceBins)
	}
	res.MaxIntensity = maxVal
	res.Ridges = a.findRidges(s, bearing)
	return res
}

// AnalyzeMany is a convenience used by tests / batch reprocessing.
func (a *Analyzer) AnalyzeMany(sweeps []*model.Sweep) []*model.SweepAnalysis {
	out := make([]*model.SweepAnalysis, 0, len(sweeps))
	for _, s := range sweeps {
		out = append(out, a.Analyze(s))
	}
	return out
}

// findRidges scans for maximal runs of bins at/above the ridge threshold and
// keeps runs spanning at least MinRidgeBins. A single radar ray maps a ridge
// to one run, so 1-D run detection is the appropriate model.
func (a *Analyzer) findRidges(s *model.Sweep, bearing float64) []model.Ridge {
	var ridges []model.Ridge
	n := len(s.Intensity)
	for i := 0; i < n; {
		if int(s.Intensity[i]) < a.params.RidgeThreshold {
			i++
			continue
		}
		start := i
		sum := 0
		peak := 0
		peakBin := i
		for i < n && int(s.Intensity[i]) >= a.params.RidgeThreshold {
			v := int(s.Intensity[i])
			sum += v
			if v > peak {
				peak = v
				peakBin = i
			}
			i++
		}
		end := i - 1 // inclusive
		if end-start+1 < a.params.MinRidgeBins {
			continue
		}
		sr := float64(s.RangeStart) + float64(start)*float64(s.RangeBin)
		er := float64(s.RangeStart) + float64(end)*float64(s.RangeBin)
		pr := float64(s.RangeStart) + float64(peakBin)*float64(s.RangeBin)
		lat, lon := model.BinGeoPoint(s.ShipLat, s.ShipLon, bearing, pr)
		ridges = append(ridges, model.Ridge{
			SweepID:       s.ID,
			Time:          s.Time,
			StartBin:      start,
			EndBin:        end,
			PeakBin:       peakBin,
			StartRangeM:   sr,
			EndRangeM:     er,
			WidthM:        er - sr + float64(s.RangeBin),
			PeakIntensity: peak,
			MeanIntensity: int(math.Round(float64(sum) / float64(end-start+1))),
			Lat:           lat,
			Lon:           lon,
		})
	}
	return ridges
}

func growBox(a *model.SweepAnalysis, lat, lon float64) {
	if lat < a.MinLat {
		a.MinLat = lat
	}
	if lat > a.MaxLat {
		a.MaxLat = lat
	}
	if lon < a.MinLon {
		a.MinLon = lon
	}
	if lon > a.MaxLon {
		a.MaxLon = lon
	}
}
