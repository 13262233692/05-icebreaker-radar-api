// Package ice_analyzer 基于回波强度序列计算海冰密集度并识别冰脊位置。
package ice_analyzer

import (
	"time"

	"icebreaker-radar/internal/echo_parser"
)

// Config 为分析阈值配置。
type Config struct {
	// IceThreshold 判定某采样点为海冰的回波强度阈值（0-65535）。
	IceThreshold uint16
	// RidgeThreshold 判定冰脊峰值的回波强度阈值。
	RidgeThreshold uint16
	// RidgeMinProminence 冰脊峰相对邻近谷值的最小突出度。
	RidgeMinProminence uint16
	// RidgeMinDistance 相邻冰脊峰的最小采样点间距，用于去抖。
	RidgeMinDistance int
	// MetersPerSample 每个采样点对应的水平距离（米），用于换算冰脊位置。
	MetersPerSample float64
}

func DefaultConfig() Config {
	return Config{
		IceThreshold:       18000,
		RidgeThreshold:     45000,
		RidgeMinProminence: 8000,
		RidgeMinDistance:   8,
		MetersPerSample:    7.5,
	}
}

// Ridge 表示一处识别到的冰脊。
type Ridge struct {
	SampleIndex int     `json:"sample_index"` // 峰值所在采样点下标
	RangeMeters float64 `json:"range_meters"` // 距雷达的水平距离（米）
	Peak        uint16  `json:"peak"`         // 峰值回波强度
	Prominence  uint16  `json:"prominence"`   // 相对邻近谷值的突出度
}

// Result 为单帧回波的分析结果。
type Result struct {
	Timestamp      time.Time `json:"timestamp"`
	Latitude       float64   `json:"latitude"`
	Longitude      float64   `json:"longitude"`
	Concentration  float64   `json:"concentration"` // 海冰密集度 0.0-1.0
	MeanIntensity  float64   `json:"mean_intensity"`
	MaxIntensity   uint16    `json:"max_intensity"`
	SampleCount    int       `json:"sample_count"`
	IceSampleCount int       `json:"ice_sample_count"`
	Ridges         []Ridge   `json:"ridges"`
}

// Analyze 对一帧回波执行密集度计算与冰脊识别。
func Analyze(frame *echo_parser.EchoFrame, cfg Config) *Result {
	res := &Result{
		Timestamp: frame.Timestamp,
		Latitude:  frame.Latitude,
		Longitude: frame.Longitude,
		Ridges:    []Ridge{},
	}
	n := len(frame.Samples)
	res.SampleCount = n
	if n == 0 {
		return res
	}

	var sum uint64
	iceCount := 0
	var maxV uint16
	for _, s := range frame.Samples {
		sum += uint64(s)
		if s >= cfg.IceThreshold {
			iceCount++
		}
		if s > maxV {
			maxV = s
		}
	}
	res.IceSampleCount = iceCount
	res.Concentration = float64(iceCount) / float64(n)
	res.MeanIntensity = float64(sum) / float64(n)
	res.MaxIntensity = maxV
	res.Ridges = detectRidges(frame.Samples, cfg)
	return res
}

// detectRidges 识别局部极大值点：峰值超过阈值、相对两侧谷值有足够突出度，
// 并按最小间距去抖（保留更突出的峰）。
func detectRidges(samples []uint16, cfg Config) []Ridge {
	n := len(samples)
	ridges := []Ridge{}
	if n < 3 {
		return ridges
	}
	lastAccepted := -cfg.RidgeMinDistance - 1
	for i := 1; i < n-1; i++ {
		v := samples[i]
		if v < cfg.RidgeThreshold {
			continue
		}
		if v < samples[i-1] || v < samples[i+1] {
			continue // 非局部极大值
		}
		// 计算突出度：峰 - max(左侧谷, 右侧谷)
		leftMin, rightMin := v, v
		for j := i - 1; j >= 0 && samples[j] <= samples[j+1]; j-- {
			if samples[j] < leftMin {
				leftMin = samples[j]
			}
		}
		for j := i + 1; j < n && samples[j] <= samples[j-1]; j++ {
			if samples[j] < rightMin {
				rightMin = samples[j]
			}
		}
		base := leftMin
		if rightMin > base {
			base = rightMin
		}
		prominence := v - base
		if prominence < cfg.RidgeMinProminence {
			continue
		}
		if i-lastAccepted < cfg.RidgeMinDistance {
			// 距离过近：若当前峰更突出则替换上一个
			if len(ridges) > 0 && prominence > ridges[len(ridges)-1].Prominence {
				ridges[len(ridges)-1] = Ridge{
					SampleIndex: i,
					RangeMeters: float64(i) * cfg.MetersPerSample,
					Peak:        v,
					Prominence:  prominence,
				}
				lastAccepted = i
			}
			continue
		}
		ridges = append(ridges, Ridge{
			SampleIndex: i,
			RangeMeters: float64(i) * cfg.MetersPerSample,
			Peak:        v,
			Prominence:  prominence,
		})
		lastAccepted = i
	}
	return ridges
}
