package ice_analyzer

import (
	"math"
	"testing"
	"time"

	"github.com/polar/icebreakerradar/internal/model"
)

func makeSweep(intensity []byte) *model.Sweep {
	return &model.Sweep{
		ID:         1,
		Time:       time.Now(),
		ShipLat:    78.0,
		ShipLon:    15.0,
		HeadingDeg: 0, // ray at 90 deg azimuth => due east
		AzimuthDeg: 90,
		RangeStart: 100,
		RangeBin:   100,
		Intensity:  intensity,
	}
}

func TestConcentration(t *testing.T) {
	an := New(model.AnalysisParams{
		IceThreshold: 100, RidgeThreshold: 200, MinRidgeBins: 3})
	// 6 of 10 bins are ice.
	s := makeSweep([]byte{10, 120, 200, 100, 30, 250, 110, 40, 150, 60})
	a := an.Analyze(s)
	if math.Abs(a.Concentration-0.6) > 1e-9 {
		t.Fatalf("concentration = %.2f", a.Concentration)
	}
	if a.IceBins != 6 {
		t.Fatalf("ice bins = %d", a.IceBins)
	}
	if a.MaxIntensity != 250 {
		t.Fatalf("max = %d", a.MaxIntensity)
	}
}

func TestRidgeDetection(t *testing.T) {
	an := New(model.AnalysisParams{
		IceThreshold: 100, RidgeThreshold: 200, MinRidgeBins: 3})
	// One qualifying run [4..6], one too-short run [8..9].
	s := makeSweep([]byte{0, 0, 0, 0, 210, 220, 230, 0, 240, 205})
	a := an.Analyze(s)
	if len(a.Ridges) != 1 {
		t.Fatalf("ridges = %d", len(a.Ridges))
	}
	r := a.Ridges[0]
	if r.StartBin != 4 || r.EndBin != 6 || r.PeakBin != 6 {
		t.Fatalf("ridge extent = %+v", r)
	}
	if r.PeakIntensity != 230 {
		t.Fatalf("peak = %d", r.PeakIntensity)
	}
	// Peak bin 6 at 100m start + 6*100m = 700m due east: lon should grow,
	// latitude essentially unchanged.
	if math.Abs(r.Lat-78.0) > 1e-6 {
		t.Fatalf("ridge lat = %.6f", r.Lat)
	}
	if r.Lon <= 15.0 {
		t.Fatalf("ridge lon should be east of ship: %.6f", r.Lon)
	}
}

func TestCoverageBoxBearingNorth(t *testing.T) {
	an := New(model.DefaultAnalysisParams())
	s := makeSweep([]byte{50, 50, 50})
	s.AzimuthDeg = 0 // due north
	a := an.Analyze(s)
	if a.MaxLat <= 78.0 {
		t.Fatalf("north ray max lat = %.6f", a.MaxLat)
	}
	if math.Abs(a.MinLon-15.0) > 1e-9 || math.Abs(a.MaxLon-15.0) > 1e-9 {
		t.Fatalf("north ray longitude should stay at ship: %.6f..%.6f",
			a.MinLon, a.MaxLon)
	}
}

func TestBBoxIntersects(t *testing.T) {
	a := model.BBox{MinLat: 10, MaxLat: 20, MinLon: 10, MaxLon: 20}
	if !a.Intersects(model.BBox{MinLat: 15, MaxLat: 25, MinLon: 15, MaxLon: 25}) {
		t.Fatal("overlapping boxes reported disjoint")
	}
	if a.Intersects(model.BBox{MinLat: 21, MaxLat: 30, MinLon: 10, MaxLon: 20}) {
		t.Fatal("disjoint boxes reported overlapping")
	}
	if !a.ContainsPoint(15, 15) || a.ContainsPoint(5, 15) {
		t.Fatal("contains point wrong")
	}
}
