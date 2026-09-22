package ice_analyzer

import (
	"testing"
	"time"

	"icebreaker-radar/internal/echo_parser"
)

func testConfig() Config {
	return Config{
		IceThreshold:       100,
		RidgeThreshold:     500,
		RidgeMinProminence: 200,
		RidgeMinDistance:   3,
		MetersPerSample:    10,
	}
}

func TestConcentration(t *testing.T) {
	frame := &echo_parser.EchoFrame{
		Timestamp: time.Now(),
		Latitude:  78, Longitude: 15,
		Samples: []uint16{50, 150, 200, 80, 300}, // 阈值 100 -> 3/5
	}
	res := Analyze(frame, testConfig())
	if res.IceSampleCount != 3 {
		t.Fatalf("ice count = %d, want 3", res.IceSampleCount)
	}
	if got, want := res.Concentration, 0.6; got != want {
		t.Fatalf("concentration = %v, want %v", got, want)
	}
	if res.MeanIntensity != 156 {
		t.Fatalf("mean = %v, want 156", res.MeanIntensity)
	}
	if res.MaxIntensity != 300 {
		t.Fatalf("max = %d, want 300", res.MaxIntensity)
	}
}

func TestRidgeDetection(t *testing.T) {
	samples := make([]uint16, 40) // 全 0 背景
	samples[10] = 900             // 冰脊峰
	samples[25] = 800             // 冰脊峰
	samples[26] = 700             // 距离过近，应被去抖
	frame := &echo_parser.EchoFrame{Timestamp: time.Now(), Latitude: 78, Longitude: 15, Samples: samples}
	res := Analyze(frame, testConfig())
	if len(res.Ridges) != 2 {
		t.Fatalf("ridges = %v, want 2", res.Ridges)
	}
	if res.Ridges[0].SampleIndex != 10 || res.Ridges[1].SampleIndex != 25 {
		t.Fatalf("ridge indexes = %d,%d", res.Ridges[0].SampleIndex, res.Ridges[1].SampleIndex)
	}
	if res.Ridges[0].RangeMeters != 100 {
		t.Fatalf("range = %v, want 100", res.Ridges[0].RangeMeters)
	}
}

func TestRidgeProminenceFilter(t *testing.T) {
	samples := []uint16{0, 550, 0, 0, 0, 480, 0} // 550 过阈值且突出度足够；480 低于 RidgeThreshold=500
	frame := &echo_parser.EchoFrame{Timestamp: time.Now(), Latitude: 78, Longitude: 15, Samples: samples}
	res := Analyze(frame, testConfig())
	if len(res.Ridges) != 1 || res.Ridges[0].SampleIndex != 1 {
		t.Fatalf("ridges = %+v", res.Ridges)
	}
}

func TestEmptyFrame(t *testing.T) {
	res := Analyze(&echo_parser.EchoFrame{Timestamp: time.Now()}, testConfig())
	if res.SampleCount != 0 || res.Concentration != 0 || len(res.Ridges) != 0 {
		t.Fatalf("empty frame result = %+v", res)
	}
}
