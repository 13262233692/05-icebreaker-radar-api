package ice_analyzer

import (
	"testing"

	"github.com/icebreaker/ice-radar-api/internal/model"
)

func TestConcentrationAndRidges(t *testing.T) {
	an := New(Config{
		IceThreshold:   100,
		RidgeThreshold: 200,
		RidgeMinBins:   3,
		RidgeMinRun:    3,
	})

	frame := &model.EchoFrame{
		FrameID:    1,
		SweepID:    1,
		VesselLat:  78.0,
		VesselLon:  15.0,
		Heading:    0,
		BinSpacing: 10,
		AzStart:    0,
		AzEnd:      360,
		RangeBins:  10,
		Intensity:  []uint8{10, 150, 120, 200, 210, 220, 30, 110, 250, 90},
	}

	res := an.Analyze(frame)

	// Ice bins: 150,120,200,210,220,110,250 => 7 of 10.
	if got := res.Stats.IceBins; got != 7 {
		t.Fatalf("ice bins = %d, want 7", got)
	}
	if res.Stats.IceConcentration != 0.7 {
		t.Fatalf("concentration = %v, want 0.7", res.Stats.IceConcentration)
	}
	if res.Stats.MaxIntensity != 250 {
		t.Fatalf("max = %d", res.Stats.MaxIntensity)
	}

	// Only the run [200,210,220] has length >= 3; lone 250 is ignored.
	if len(res.Ridges) != 1 {
		t.Fatalf("ridges = %d, want 1: %+v", len(res.Ridges), res.Ridges)
	}
	r := res.Ridges[0]
	if r.StartBin != 3 || r.EndBin != 5 || r.PeakBin != 5 {
		t.Fatalf("ridge span = [%d,%d], peak %d", r.StartBin, r.EndBin, r.PeakBin)
	}
	if r.RangeStartM != 30 || r.RangeEndM != 60 {
		t.Fatalf("ridge range = [%v,%v]", r.RangeStartM, r.RangeEndM)
	}
	if r.Lat == 0 || r.Lon == 0 {
		t.Fatal("ridge geoposition not computed")
	}
	if res.Stats.RidgeCount != 1 {
		t.Fatalf("ridge count in stats = %d", res.Stats.RidgeCount)
	}
}

func TestRidgeAcrossWrappingSector(t *testing.T) {
	az := binAzimuth(&model.EchoFrame{AzStart: 350, AzEnd: 10,
		RangeBins: 4, Intensity: make([]uint8, 4)}, 0, 2)
	// Midpoint of a 20-degree wrapping sector should sit near 0 degrees.
	if az < 355 && az > 5 {
		t.Fatalf("wrapping azimuth = %v", az)
	}
}
