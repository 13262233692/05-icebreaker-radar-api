// 雷达前端模拟器：向 TCP 接入服务推送合成回波帧，用于联调与演示。
package main

import (
	"flag"
	"log"
	"math"
	"math/rand"
	"net"
	"time"

	"icebreaker-radar/internal/echo_parser"
)

func main() {
	addr := flag.String("addr", "localhost:9000", "TCP ingest address")
	interval := flag.Duration("interval", 500*time.Millisecond, "frame interval")
	samples := flag.Int("samples", 256, "samples per frame")
	flag.Parse()

	conn, err := net.Dial("tcp", *addr)
	if err != nil {
		log.Fatalf("dial %s: %v", *addr, err)
	}
	defer conn.Close()
	log.Printf("connected to %s, pushing frames every %s", *addr, *interval)

	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	baseLat, baseLon := 78.2232, 15.6267 // 斯瓦尔巴附近
	tick := 0
	for {
		// 模拟破冰船缓慢移动
		lat := baseLat + 0.0001*float64(tick)*math.Sin(float64(tick)/50)
		lon := baseLon + 0.0003*float64(tick)
		frame := &echo_parser.EchoFrame{
			Timestamp: time.Now().UTC(),
			Latitude:  lat,
			Longitude: lon,
			Samples:   make([]uint16, *samples),
		}
		for i := range frame.Samples {
			// 背景噪声 + 海冰回波基底
			v := 12000.0 + 15000.0*math.Abs(math.Sin(float64(i)/30+float64(tick)/10)) + rng.Float64()*4000
			// 周期性叠加冰脊尖峰
			if i%64 == 32 {
				v += 30000 + rng.Float64()*8000
			}
			if v > echo_parser.MaxIntensity {
				v = echo_parser.MaxIntensity
			}
			frame.Samples[i] = uint16(v)
		}
		data, err := echo_parser.Encode(frame)
		if err != nil {
			log.Fatalf("encode: %v", err)
		}
		if _, err := conn.Write(data); err != nil {
			log.Fatalf("write: %v", err)
		}
		tick++
		time.Sleep(*interval)
	}
}
