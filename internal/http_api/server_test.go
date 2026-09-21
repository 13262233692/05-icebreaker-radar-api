package http_api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/polar/icebreakerradar/internal/model"
	"github.com/polar/icebreakerradar/internal/tcp_ingest"
)

type fakeCache struct {
	envs []model.Envelope
}

func (f *fakeCache) Recent(_ context.Context, flt model.IceFilter) ([]model.Envelope, error) {
	return filterEnvs(f.envs, flt), nil
}

func (f *fakeCache) Range(_ context.Context, flt model.IceFilter) ([]model.Envelope, error) {
	return filterEnvs(f.envs, flt), nil
}

func (f *fakeCache) Ping(_ context.Context) error { return nil }

func filterEnvs(envs []model.Envelope, f model.IceFilter) []model.Envelope {
	var out []model.Envelope
	for _, e := range envs {
		if !e.Sweep.Time.Before(f.From) && !e.Sweep.Time.After(f.To) {
			if f.HasBBox {
				box := model.BBox{
					MinLat: e.Analysis.MinLat, MaxLat: e.Analysis.MaxLat,
					MinLon: e.Analysis.MinLon, MaxLon: e.Analysis.MaxLon,
				}
				if !f.BBox.Intersects(box) {
					continue
				}
			}
			out = append(out, e)
			if len(out) >= f.Limit {
				break
			}
		}
	}
	return out
}

type fakeDB struct {
	obs []model.SweepAnalysis
	rdg []model.Ridge
}

func (f *fakeDB) QueryObservations(_ context.Context, flt model.IceFilter) ([]model.SweepAnalysis, error) {
	var out []model.SweepAnalysis
	for _, o := range f.obs {
		if !o.Time.Before(flt.From) && !o.Time.After(flt.To) {
			if flt.HasBBox {
				box := model.BBox{
					MinLat: o.MinLat, MaxLat: o.MaxLat,
					MinLon: o.MinLon, MaxLon: o.MaxLon,
				}
				if !flt.BBox.Intersects(box) {
					continue
				}
			}
			out = append(out, o)
		}
	}
	return out, nil
}

func (f *fakeDB) QueryRidges(_ context.Context, flt model.IceFilter) ([]model.Ridge, error) {
	var out []model.Ridge
	for _, r := range f.rdg {
		if !r.Time.Before(flt.From) && !r.Time.After(flt.To) {
			if flt.HasBBox && !flt.BBox.ContainsPoint(r.Lat, r.Lon) {
				continue
			}
			out = append(out, r)
		}
	}
	return out, nil
}

func (f *fakeDB) Ping(_ context.Context) error { return nil }

func newTestServer(envs []model.Envelope, obs []model.SweepAnalysis, rdg []model.Ridge) http.Handler {
	cache := &fakeCache{envs: envs}
	db := &fakeDB{obs: obs, rdg: rdg}
	srv := NewServer(cache, db, cache,
		func() tcp_ingest.Stats { return tcp_ingest.Stats{FramesParsed: 3} })
	return srv.Handler()
}

func TestEchoesTimeAndBBoxFilter(t *testing.T) {
	now := time.Now().UTC()
	mkEnv := func(id uint64, lat, lon float64, when time.Time) model.Envelope {
		s := &model.Sweep{ID: id, Time: when, ShipLat: lat, ShipLon: lon,
			RangeStart: 1, RangeBin: 1, Intensity: []byte{200}}
		box := model.BBox{MinLat: lat - .01, MaxLat: lat + .01,
			MinLon: lon - .01, MaxLon: lon + .01}
		a := &model.SweepAnalysis{
			SweepID: id, Time: when, ShipLat: lat, ShipLon: lon,
			RangeBins: 1, Concentration: 1,
			MinLat: box.MinLat, MaxLat: box.MaxLat,
			MinLon: box.MinLon, MaxLon: box.MaxLon,
		}
		return model.Envelope{Sweep: s, Analysis: a}
	}
	envs := []model.Envelope{
		mkEnv(1, 78.0, 15.0, now.Add(-2*time.Minute)),
		mkEnv(2, 79.0, 16.0, now.Add(-2*time.Minute)),  // outside bbox
		mkEnv(3, 78.01, 15.01, now.Add(-30*time.Hour)), // outside time
	}
	h := newTestServer(envs, nil, nil)

	q := fmt.Sprintf("/api/v1/echoes?min_lat=77.9&max_lat=78.1&min_lon=14.9&max_lon=15.1&from=%d&to=%d",
		now.Add(-time.Hour).Unix(), now.Add(time.Hour).Unix())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, q, nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Count int `json:"count"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Count != 1 {
		t.Fatalf("count = %d, want 1", body.Count)
	}
}

func TestInvalidBBoxRejected(t *testing.T) {
	h := newTestServer(nil, nil, nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/ice/observations?min_lat=80&max_lat=70", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestSummaryAggregation(t *testing.T) {
	now := time.Now().UTC()
	obs := []model.SweepAnalysis{
		{SweepID: 2, Time: now, Concentration: 0.8,
			MinLat: 78, MaxLat: 78, MinLon: 15, MaxLon: 15,
			ShipLat: 78, ShipLon: 15},
		{SweepID: 1, Time: now.Add(-time.Minute), Concentration: 0.4,
			MinLat: 78, MaxLat: 78, MinLon: 15, MaxLon: 15,
			ShipLat: 78, ShipLon: 15},
	}
	rdg := []model.Ridge{
		{Time: now, Lat: 78, Lon: 15}, {Time: now, Lat: 78, Lon: 15},
	}
	h := newTestServer(nil, obs, rdg)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/ice/summary", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Summary struct {
			Observations      int     `json:"observations"`
			RidgeCount        int     `json:"ridge_count"`
			MeanConcentration float64 `json:"mean_concentration"`
			MaxConcentration  float64 `json:"max_concentration"`
		} `json:"summary"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Summary.Observations != 2 || body.Summary.RidgeCount != 2 {
		t.Fatalf("counts = %+v", body.Summary)
	}
	if diff := body.Summary.MeanConcentration - 0.6; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("mean = %.9f", body.Summary.MeanConcentration)
	}
	if body.Summary.MaxConcentration != 0.8 {
		t.Fatalf("max = %.2f", body.Summary.MaxConcentration)
	}
}

func TestHealthOK(t *testing.T) {
	h := newTestServer(nil, nil, nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d", rec.Code)
	}
}
