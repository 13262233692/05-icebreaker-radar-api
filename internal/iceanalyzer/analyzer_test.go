package iceanalyzer

import (
	"math"
	"testing"
	"time"

	"icebreaker-radar/internal/echoparser"
)

func syntheticSweep() *echoparser.Sweep {
	const rays, bins = 360, 400
	const rangeM = 6000.0
	s := &echoparser.Sweep{
		Timestamp:       time.Now().UTC(),
		ShipLat:         69.65,
		ShipLon:         18.50,
		RangeM:          rangeM,
		BinSpacingM:     rangeM / bins,
		StartBearingRad: 0,
		AngularStepRad:  float32(2 * math.Pi / float64(rays)),
		Rays:            make([][]byte, rays),
	}
	for r := range s.Rays {
		s.Rays[r] = make([]byte, bins)
	}
	return s
}

// paintBrightLine fills bins within widthM of an oriented line segment,
// mirroring the simulator's ridge geometry.
func paintBrightLine(s *echoparser.Sweep, bearing, dist, length, width, angle float64, v byte) {
	rays, bins := s.RayCount(), s.BinsPerRay()
	for r := 0; r < rays; r++ {
		brz := float64(r) * 2 * math.Pi / float64(rays)
		for b := 0; b < bins; b++ {
			d := (float64(b) + 0.5) * float64(s.BinSpacingM)
			px, py := d*math.Sin(brz), d*math.Cos(brz)
			cx, cy := dist*math.Sin(bearing), dist*math.Cos(bearing)
			dx, dy := px-cx, py-cy
			ux, uy := math.Sin(angle), math.Cos(angle)
			along := dx*ux + dy*uy
			perp := math.Abs(dx*uy - dy*ux)
			if math.Abs(along) <= length/2 && perp <= width {
				s.Rays[r][b] = v
			}
		}
	}
}

func TestEmptyOceanSweep(t *testing.T) {
	s := syntheticSweep()
	res := Analyze(s, DefaultConfig())
	if res.Concentration != 0 || res.IceBins != 0 {
		t.Fatalf("expected zero concentration, got %f ice=%d", res.Concentration, res.IceBins)
	}
	if !math.IsNaN(res.MinLat) {
		t.Fatalf("extents should be NaN with no ice, got %v", res.MinLat)
	}
	if len(res.Ridges) != 0 {
		t.Fatalf("expected no ridges, got %d", len(res.Ridges))
	}
}

func TestRidgeDetectedAndGeoLocated(t *testing.T) {
	s := syntheticSweep()
	paintBrightLine(s, 0.5, 3000, 900, 30, 0.7, 230)

	cfg := DefaultConfig()
	res := Analyze(s, cfg)
	if res.Concentration <= 0 || res.IceBins == 0 {
		t.Fatalf("expected ice, got concentration=%f bins=%d", res.Concentration, res.IceBins)
	}
	if len(res.Ridges) == 0 {
		t.Fatalf("expected at least one ridge; ice=%d", res.IceBins)
	}
	var best Ridge
	for _, rg := range res.Ridges {
		if rg.LengthM > best.LengthM {
			best = rg
		}
	}
	if best.LengthM < 400 {
		t.Fatalf("ridge too short: %.1f m", best.LengthM)
	}
	if best.Aspect < cfg.MinRidgeAspect {
		t.Fatalf("ridge not elongated: aspect=%.2f", best.Aspect)
	}
	// Centroid should be roughly 3 km from the ship.
	dx := (best.Lon - s.ShipLon) * MetersPerDegreeLon(s.ShipLat)
	dy := (best.Lat - s.ShipLat) * metersPerDegreeLat
	if d := math.Hypot(dx, dy); math.Abs(d-3000) > 500 {
		t.Fatalf("ridge centroid at %.0f m, expected ~3000", d)
	}
	if best.BearingRad < 0 || best.BearingRad > 2*math.Pi {
		t.Fatalf("bearing out of range: %f", best.BearingRad)
	}
}

func TestCellCoordinateRoundTrip(t *testing.T) {
	s := syntheticSweep()
	lat, lon := CellCoordinate(s, 45 /*ray*/, 200 /*bin*/)
	if math.Abs(lat-s.ShipLat) > 0.1 || math.Abs(lon-s.ShipLon) > 0.2 {
		t.Fatalf("cell geo unreasonable: %v,%v", lat, lon)
	}
}
