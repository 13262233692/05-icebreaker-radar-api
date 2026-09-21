package iceanalyzer

import (
	"math"

	"icebreaker-radar/internal/echoparser"
)

// Config controls sea-ice / ridge classification.
type Config struct {
	// IceThreshold is the echo intensity (0..255) at/above which a bin is ice.
	IceThreshold byte
	// MinRidgeBins rejects components too small to be pressure ridges.
	MinRidgeBins int
	// MinRidgeLengthM rejects round floes; ridges must be elongated.
	MinRidgeLengthM float64
	// MinRidgeAspect ratio of long/short PCA axis required for a ridge.
	MinRidgeAspect float64
}

func DefaultConfig() Config {
	return Config{
		IceThreshold:    120,
		MinRidgeBins:    24,
		MinRidgeLengthM: 60,
		MinRidgeAspect:  2.0,
	}
}

// Ridge is one detected pressure ridge feature.
type Ridge struct {
	Lat          float64 `json:"lat"`
	Lon          float64 `json:"lon"`
	BearingRad   float64 `json:"bearing_rad"`
	LengthM      float64 `json:"length_m"`
	Aspect       float64 `json:"aspect"`
	BinCount     int     `json:"bin_count"`
	MaxIntensity int     `json:"max_intensity"`
}

// Result is the analysis output for one sweep.
type Result struct {
	Concentration float64 `json:"concentration"` // ice bins / valid bins, 0..1
	IceBins       int     `json:"ice_bins"`
	TotalBins     int     `json:"total_bins"`
	MeanIntensity float64 `json:"mean_intensity"`
	MaxIntensity  int     `json:"max_intensity"`

	// Extents of ice-classified bins. NaN when the sweep has no ice.
	MinLat float64 `json:"min_lat"`
	MaxLat float64 `json:"max_lat"`
	MinLon float64 `json:"min_lon"`
	MaxLon float64 `json:"max_lon"`

	Ridges []Ridge `json:"ridges"`
}

const metersPerDegreeLat = 111_320.0

// MetersPerDegreeLon returns the longitude-degree scale at a given latitude.
func MetersPerDegreeLon(lat float64) float64 {
	return metersPerDegreeLat * math.Cos(lat*math.Pi/180)
}

// CellCoordinate projects a polar (ray, bin) sample onto WGS84-ish lat/lon
// using an equirectangular local frame around the ship. Good at polar ranges
// (a few nautical miles) and near the poles where longitude distortion is high.
func CellCoordinate(s *echoparser.Sweep, ray, bin int) (lat, lon float64) {
	bearing := float64(s.StartBearingRad) + float64(ray)*float64(s.AngularStepRad)
	rng := (float64(bin) + 0.5) * float64(s.BinSpacingM)

	north := rng * math.Cos(bearing)
	east := rng * math.Sin(bearing)

	lat = s.ShipLat + north/metersPerDegreeLat
	lon = s.ShipLon + east/MetersPerDegreeLon(s.ShipLat)
	return lat, lon
}

// Analyze computes concentration, intensity statistics and ridge features.
func Analyze(s *echoparser.Sweep, cfg Config) Result {
	rays := s.RayCount()
	bins := s.BinsPerRay()
	res := Result{
		TotalBins: rays * bins,
		MinLat:    math.NaN(),
		MaxLat:    math.NaN(),
		MinLon:    math.NaN(),
		MaxLon:    math.NaN(),
	}
	if rays == 0 || bins == 0 {
		return res
	}

	mask := make([]bool, rays*bins)
	var intensitySum uint64
	for r := 0; r < rays; r++ {
		ray := s.Rays[r]
		for b := 0; b < bins; b++ {
			v := ray[b]
			intensitySum += uint64(v)
			if int(v) > res.MaxIntensity {
				res.MaxIntensity = int(v)
			}
			if v >= cfg.IceThreshold {
				mask[r*bins+b] = true
				res.IceBins++
				lat, lon := CellCoordinate(s, r, b)
				if res.IceBins == 1 {
					res.MinLat, res.MaxLat = lat, lat
					res.MinLon, res.MaxLon = lon, lon
				} else {
					res.MinLat = math.Min(res.MinLat, lat)
					res.MaxLat = math.Max(res.MaxLat, lat)
					res.MinLon = math.Min(res.MinLon, lon)
					res.MaxLon = math.Max(res.MaxLon, lon)
				}
			}
		}
	}
	res.MeanIntensity = float64(intensitySum) / float64(res.TotalBins)
	res.Concentration = float64(res.IceBins) / float64(res.TotalBins)

	res.Ridges = findRidges(s, mask, cfg)
	return res
}

type component struct {
	cells        []int // ray*bins+bin indices
	maxIntensity int
}

// findRidges labels 8-connected ice components (rays wrap around the antenna
// revolution) and keeps components that are long, elongated and bright enough.
func findRidges(s *echoparser.Sweep, mask []bool, cfg Config) []Ridge {
	rays := s.RayCount()
	bins := s.BinsPerRay()
	visited := make([]bool, len(mask))
	var ridges []Ridge

	for start := 0; start < len(mask); start++ {
		if !mask[start] || visited[start] {
			continue
		}
		comp := component{}
		stack := []int{start}
		visited[start] = true

		for len(stack) > 0 {
			idx := stack[len(stack)-1]
			stack = stack[:len(stack)-1]

			r, b := idx/bins, idx%bins
			if int(s.Rays[r][b]) > comp.maxIntensity {
				comp.maxIntensity = int(s.Rays[r][b])
			}
			comp.cells = append(comp.cells, idx)

			for dr := -1; dr <= 1; dr++ {
				nr := r + dr
				if nr < 0 {
					nr = rays - 1
				} else if nr >= rays {
					nr = 0
				}
				for db := -1; db <= 1; db++ {
					if dr == 0 && db == 0 {
						continue
					}
					nb := b + db
					if nb < 0 || nb >= bins {
						continue
					}
					nidx := nr*bins + nb
					if mask[nidx] && !visited[nidx] {
						visited[nidx] = true
						stack = append(stack, nidx)
					}
				}
			}
		}

		if ridge, ok := characterize(s, comp, cfg); ok {
			ridges = append(ridges, ridge)
		}
	}
	return ridges
}

func characterize(s *echoparser.Sweep, c component, cfg Config) (Ridge, bool) {
	if len(c.cells) < cfg.MinRidgeBins {
		return Ridge{}, false
	}

	lat0 := s.ShipLat
	mpdLon := MetersPerDegreeLon(lat0)

	var sumX, sumY float64
	pts := make([][2]float64, len(c.cells))
	for i, idx := range c.cells {
		r, b := idx/s.BinsPerRay(), idx%s.BinsPerRay()
		lat, lon := CellCoordinate(s, r, b)
		x := (lon - s.ShipLon) * mpdLon
		y := (lat - lat0) * metersPerDegreeLat
		pts[i] = [2]float64{x, y}
		sumX += x
		sumY += y
	}
	n := float64(len(pts))
	meanX, meanY := sumX/n, sumY/n

	var sxx, syy, sxy float64
	var centroidLat, centroidLon float64
	for _, p := range pts {
		dx, dy := p[0]-meanX, p[1]-meanY
		sxx += dx * dx
		syy += dy * dy
		sxy += dx * dy
	}
	sxx /= n
	syy /= n
	sxy /= n
	for _, p := range pts {
		centroidLon += s.ShipLon + p[0]/mpdLon
		centroidLat += lat0 + p[1]/metersPerDegreeLat
	}
	centroidLat /= n
	centroidLon /= n

	// Eigenvalues of the 2x2 covariance matrix.
	tr := sxx + syy
	disc := math.Sqrt(math.Max(0, (sxx-syy)*(sxx-syy)+4*sxy*sxy))
	lambda1 := (tr + disc) / 2
	lambda2 := (tr - disc) / 2

	// Extent ±2σ along each axis.
	longAxis := 4 * math.Sqrt(math.Max(0, lambda1))
	shortAxis := 4 * math.Sqrt(math.Max(0, lambda2))
	if longAxis < cfg.MinRidgeLengthM {
		return Ridge{}, false
	}
	aspect := math.Inf(1)
	if shortAxis > 1e-6 {
		aspect = longAxis / shortAxis
	}
	if aspect < cfg.MinRidgeAspect {
		return Ridge{}, false
	}

	// Principal-axis orientation; bearing measured clockwise from true north.
	bearing := math.Atan2(2*sxy, (sxx-syy)) / 2
	bearingNorth := math.Pi/2 - bearing
	if bearingNorth < 0 {
		bearingNorth += 2 * math.Pi
	}

	return Ridge{
		Lat:          centroidLat,
		Lon:          centroidLon,
		BearingRad:   bearingNorth,
		LengthM:      longAxis,
		Aspect:       aspect,
		BinCount:     len(pts),
		MaxIntensity: c.maxIntensity,
	}, true
}
