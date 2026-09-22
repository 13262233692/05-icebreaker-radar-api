// Command radar_sim simulates a radar front end: it connects over TCP and
// streams encoded binary echo sweeps with ice-like backscatter and ridges.
package main

import (
	"flag"
	"log"
	"math"
	"math/rand"
	"net"
	"time"

	"github.com/icebreaker/ice-radar-api/internal/echo_parser"
	"github.com/icebreaker/ice-radar-api/internal/model"
)

func main() {
	addr := flag.String("addr", "localhost:9101", "TCP ingest address")
	interval := flag.Duration("interval", 200*time.Millisecond, "time between sweeps")
	bins := flag.Int("bins", 360, "range bins per sweep")
	lat := flag.Float64("lat", 78.2, "vessel latitude")
	lon := flag.Float64("lon", 15.6, "vessel longitude")
	flag.Parse()

	conn, err := net.Dial("tcp", *addr)
	if err != nil {
		log.Fatalf("dial %s: %v", *addr, err)
	}
	defer conn.Close()
	log.Printf("radar simulator connected to %s", *addr)

	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	var frameID uint32

	for sweep := uint32(0); ; sweep++ {
		intensity := make([]uint8, *bins)

		// Background water/noise plus a broad ice band.
		for i := range intensity {
			base := 30 + 20*math.Sin(float64(i)*0.05+float64(sweep)*0.1)
			intensity[i] = uint8(math.Max(0, math.Min(255, base+rng.NormFloat64()*8)))
			if i > 60 && i < 300 {
				intensity[i] += 90
			}
		}

		// Inject one or two sharp high-backscatter ridges.
		ridgeStart := 120 + (int(sweep)*17)%80
		for i := ridgeStart; i < ridgeStart+8 && i < *bins; i++ {
			intensity[i] = uint8(210 + rng.Intn(45))
		}
		if sweep%3 == 0 {
			for i := 250; i < 256; i++ {
				intensity[i] = uint8(205 + rng.Intn(50))
			}
		}

		frameID++
		frame := &model.EchoFrame{
			FrameID:    frameID,
			SweepID:    sweep + 1,
			Timestamp:  time.Now().UTC(),
			VesselLat:  *lat + 0.0005*math.Sin(float64(sweep)*0.02),
			VesselLon:  *lon + 0.0008*math.Cos(float64(sweep)*0.02),
			Heading:    45 + 10*math.Sin(float64(sweep)*0.05),
			AzStart:    0,
			AzEnd:      360,
			BinSpacing: 7.5,
			RangeBins:  uint16(*bins),
			Intensity:  intensity,
		}

		packet, err := echo_parser.EncodeFrame(frame)
		if err != nil {
			log.Fatalf("encode: %v", err)
		}
		if _, err := conn.Write(packet); err != nil {
			log.Fatalf("write: %v", err)
		}

		if frameID%50 == 0 {
			log.Printf("sent %d frames", frameID)
		}
		time.Sleep(*interval)
	}
}
