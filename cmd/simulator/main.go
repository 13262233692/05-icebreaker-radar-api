// Command simulator emulates a shipborne ice radar frontend: it connects to the
// ingest TCP server and pushes binary sweep/heartbeat frames.
package main

import (
	"flag"
	"log"
	"math"
	"math/rand"
	"net"
	"time"

	"icebreaker-radar/internal/echoparser"
)

type ridgeSpec struct {
	bearingRad float64 // ridge center bearing from the ship
	distM      float64
	lengthM    float64
	widthM     float64
	angleRad   float64 // orientation of the long axis
}

func main() {
	addr := flag.String("addr", "localhost:9101", "ingest server host:port")
	interval := flag.Duration("interval", 2*time.Second, "time between sweeps")
	rays := flag.Int("rays", 360, "rays per sweep")
	bins := flag.Int("bins", 400, "bins per ray")
	rangeM := flag.Float64("range", 6000, "radar range in meters")
	startLat := flag.Float64("lat", 69.65, "starting ship latitude")
	startLon := flag.Float64("lon", 18.50, "starting ship longitude")
	flag.Parse()

	rng := rand.New(rand.NewSource(time.Now().UnixNano()))

	// Persistent ice field features (in ship-relative meters) so ridges are
	// stable across successive sweeps.
	ridges := []ridgeSpec{
		{bearingRad: 0.35, distM: 2500, lengthM: 900, widthM: 45, angleRad: 0.6},
		{bearingRad: 1.9, distM: 3800, lengthM: 1400, widthM: 60, angleRad: 2.1},
		{bearingRad: 4.4, distM: 1800, lengthM: 600, widthM: 40, angleRad: -0.4},
		{bearingRad: 5.5, distM: 4700, lengthM: 1100, widthM: 55, angleRad: 1.2},
	}

	binSpacing := *rangeM / float64(*bins)
	lat, lon := *startLat, *startLon
	sweepID := uint32(1)

	for {
		conn, err := net.Dial("tcp", *addr)
		if err != nil {
			log.Printf("dial %s: %v; retrying in 2s", *addr, err)
			time.Sleep(2 * time.Second)
			continue
		}
		log.Printf("connected to ingest at %s", *addr)

		ticker := time.NewTicker(*interval)
		heartbeat := time.NewTicker(5 * time.Second)
		for {
			select {
			case <-heartbeat.C:
				hb, err := echoparser.EncodeFrame(&echoparser.Frame{
					Type:      echoparser.FrameHeartbeat,
					Timestamp: time.Now().UTC(),
				})
				if err != nil {
					log.Printf("encode heartbeat: %v", err)
					continue
				}
				if _, err := conn.Write(hb); err != nil {
					log.Printf("write heartbeat: %v", err)
				}
			case <-ticker.C:
				// Slow drift east-northeast through the pack ice.
				lat += 0.00003
				lon += 0.00005
				sweep := buildSweep(rng, sweepID, *rays, *bins, *rangeM, binSpacing, lat, lon, ridges)
				frame, err := echoparser.EncodeFrame(&echoparser.Frame{
					Type:      echoparser.FrameSweep,
					SweepID:   sweepID,
					Timestamp: time.Now().UTC(),
					Sweep:     sweep,
				})
				if err != nil {
					log.Printf("encode sweep: %v", err)
					continue
				}
				if _, err := conn.Write(frame); err != nil {
					log.Printf("write sweep: %v; reconnecting", err)
				} else {
					log.Printf("sent sweep %d (%d rays x %d bins, %.0f KB)",
						sweepID, *rays, *bins, float64(len(frame))/1024)
				}
				sweepID++
			}
		}
	}
}

func buildSweep(rng *rand.Rand, id uint32, rays, bins int, rangeM, binSpacing float64, shipLat, shipLon float64, ridges []ridgeSpec) *echoparser.Sweep {
	data := make([][]byte, rays)
	baseField := 40.0 + 30.0*math.Sin(float64(id)*0.7) // drifting pack background
	iceField := 95.0 + 25.0*math.Sin(float64(id)*0.31)

	for r := 0; r < rays; r++ {
		bearing := float64(r) * 2 * math.Pi / float64(rays)
		ray := make([]byte, bins)
		for b := 0; b < bins; b++ {
			dist := (float64(b) + 0.5) * binSpacing

			// Range attenuation: far bins are dimmer and noisier.
			atten := 1.0 - 0.45*dist/rangeM
			intensity := baseField*atten + (rng.Float64()-0.5)*22

			// Irregular pack-ice patches: angular + radial lobes.
			patch := math.Sin(bearing*7+float64(id)*0.05)*math.Sin(dist/450+float64(id)*0.08) +
				0.6*math.Sin(bearing*3-dist/900)
			if patch > 0.55 {
				intensity += iceField * atten
			}

			// Bright pressure ridges: distance to oriented line segment.
			for _, rg := range ridges {
				d := distanceToOrientedRidge(bearing, dist, rg)
				if d < rg.widthM {
					core := 1.0 - d/rg.widthM
					intensity += (120 + 70*core) * atten
				}
			}

			// Nearfield blind spot: first few bins are sea clutter near the hull.
			if b < 3 {
				intensity = 30 + rng.Float64()*25
			}

			v := int(intensity)
			if v < 0 {
				v = 0
			}
			if v > 255 {
				v = 255
			}
			ray[b] = byte(v)
		}
		data[r] = ray
	}

	return &echoparser.Sweep{
		SweepID:         id,
		ShipLat:         shipLat,
		ShipLon:         shipLon,
		RangeM:          float32(rangeM),
		BinSpacingM:     float32(binSpacing),
		StartBearingRad: 0,
		AngularStepRad:  float32(2 * math.Pi / float64(rays)),
		RadarFreqGHz:    9.4,
		GainDB:          24,
		Flags:           0,
		Rays:            data,
	}
}

// distanceToOrientedRidge returns perpendicular distance (meters) from a polar
// sample point to an infinite line passing through the ridge center, rotated by
// the ridge orientation.
func distanceToOrientedRidge(bearing, dist float64, rg ridgeSpec) float64 {
	px := dist * math.Sin(bearing)
	py := dist * math.Cos(bearing)
	cx := rg.distM * math.Sin(rg.bearingRad)
	cy := rg.distM * math.Cos(rg.bearingRad)

	dx, dy := px-cx, py-cy
	// Direction vector along the ridge long axis.
	ux := math.Sin(rg.angleRad)
	uy := math.Cos(rg.angleRad)
	along := dx*ux + dy*uy
	if math.Abs(along) > rg.lengthM/2 {
		// Outside segment extent: treat as far away so ridges stay finite.
		return 1e9
	}
	perp := math.Abs(dx*uy - dy*ux)
	return perp
}
