// Command radar_sim simulates a ship-borne radar front-end pushing binary
// echo frames over TCP. It rotates the antenna and paints two synthetic ice
// fields plus pressure ridges so the whole pipeline can be exercised locally.
package main

import (
	"context"
	"flag"
	"log"
	"math"
	"math/rand"
	"net"
	"os/signal"
	"syscall"
	"time"

	"github.com/polar/icebreakerradar/internal/echo_parser"
	"github.com/polar/icebreakerradar/internal/model"
)

type wedge struct {
	azCenter float64 // degrees relative to ship heading
	azWidth  float64
	rStart   float64 // metres
	rEnd     float64
	strength float64 // base intensity
}

type ridgeSpec struct {
	azCenter float64
	rCenter  float64
	width    float64 // metres
	azSpread float64 // degrees
}

func main() {
	addr := flag.String("addr", "127.0.0.1:9101", "ingest TCP address")
	bins := flag.Int("bins", 1024, "range bins per slice")
	rangeStart := flag.Float64("range-start", 50, "first range bin (m)")
	rangeBin := flag.Float64("range-bin", 15, "range bin spacing (m)")
	rpm := flag.Float64("rpm", 6, "antenna revolutions per minute")
	slicesPerRev := flag.Int("slices", 360, "azimuth slices per revolution")
	shipLat := flag.Float64("lat", 78.2297, "ship latitude (Longyearbyen area)")
	shipLon := flag.Float64("lon", 15.1203, "ship longitude")
	flag.Parse()

	ctx, cancel := signal.NotifyContext(context.Background(),
		syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	rng := rand.New(rand.NewSource(time.Now().UnixNano()))

	// Synthetic scene: two drifting ice floes ahead and to starboard, plus a
	// pressure ridge line at the starboard floe edge.
	floes := []wedge{
		{azCenter: 12, azWidth: 26, rStart: 2400, rEnd: 11500, strength: 150},
		{azCenter: 64, azWidth: 18, rStart: 3200, rEnd: 8600, strength: 132},
	}
	ridges := []ridgeSpec{
		{azCenter: 12, rCenter: 4200, width: 45, azSpread: 1.5},
		{azCenter: 15, rCenter: 8200, width: 60, azSpread: 2},
		{azCenter: 63, rCenter: 5400, width: 50, azSpread: 1.2},
	}

	conn, err := dialRetry(ctx, *addr)
	if err != nil {
		log.Fatalf("connect %s: %v", *addr, err)
	}
	defer conn.Close()
	log.Printf("radar simulator connected to %s; bins=%d rpm=%.1f",
		*addr, *bins, *rpm)

	period := time.Duration(float64(time.Minute) / (*rpm * float64(*slicesPerRev)))
	ticker := time.NewTicker(period)
	defer ticker.Stop()

	var sweepID uint64 = 1
	var az float64
	heading := 315.0

	for {
		select {
		case <-ctx.Done():
			log.Println("simulator stopping")
			return
		case <-ticker.C:
		}
		intensity := paintSweep(*bins, *rangeStart, *rangeBin, az, floes, ridges, rng)
		sweep := &model.Sweep{
			ID:         sweepID,
			Time:       time.Now().UTC(),
			ShipLat:    *shipLat,
			ShipLon:    *shipLon,
			HeadingDeg: heading,
			AzimuthDeg: float32(az),
			ElevDeg:    0,
			RangeStart: float32(*rangeStart),
			RangeBin:   float32(*rangeBin),
			TxGainDb:   72,
			Intensity:  intensity,
		}
		frame, err := echo_parser.EncodeFrame(sweep)
		if err != nil {
			log.Fatalf("encode: %v", err)
		}
		if _, err := conn.Write(frame); err != nil {
			log.Printf("write: %v; reconnecting", err)
			_ = conn.Close()
			conn, err = dialRetry(ctx, *addr)
			if err != nil {
				log.Fatalf("reconnect: %v", err)
			}
			continue
		}
		sweepID++
		az += 360.0 / float64(*slicesPerRev)
		if az >= 360 {
			az -= 360
		}
	}
}

func paintSweep(bins int, rangeStart, rangeBin, az float64,
	floes []wedge, ridges []ridgeSpec, rng *rand.Rand) []byte {
	out := make([]byte, bins)
	for i := 0; i < bins; i++ {
		r := rangeStart + float64(i)*rangeBin
		// Open-water clutter, slightly rising then fading with range.
		v := 18 + 8*math.Exp(-r/6000) + rng.NormFloat64()*3
		for _, f := range floes {
			if r >= f.rStart && r <= f.rEnd && angleWithin(az, f.azCenter, f.azWidth/2) {
				// Floe texture plus radar falloff.
				falloff := 1 - (r-f.rStart)/(f.rEnd-f.rStart)*0.25
				v = f.strength*falloff + rng.NormFloat64()*12
			}
		}
		for _, rd := range ridges {
			if math.Abs(r-rd.rCenter) <= rd.width/2 &&
				angleWithin(az, rd.azCenter, rd.azSpread) {
				v = 238 + rng.NormFloat64()*6
			}
		}
		out[i] = clampByte(v)
	}
	return out
}

func angleWithin(a, center, halfWidth float64) bool {
	d := math.Abs(a - center)
	if d > 180 {
		d = 360 - d
	}
	return d <= halfWidth
}

func clampByte(v float64) byte {
	switch {
	case v < 0:
		return 0
	case v > 255:
		return 255
	default:
		return byte(v)
	}
}

func dialRetry(ctx context.Context, addr string) (net.Conn, error) {
	delay := time.Second
	for {
		d := net.Dialer{Timeout: 2 * time.Second}
		conn, err := d.DialContext(ctx, "tcp", addr)
		if err == nil {
			return conn, nil
		}
		log.Printf("waiting for ingest at %s (%v)", addr, err)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(delay):
		}
		if delay < 10*time.Second {
			delay *= 2
		}
	}
}
