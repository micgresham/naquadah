package sim

import (
	"testing"
	"time"

	dev "github.com/b0ch3nski/go-starlink/model/api-protoc/device"
)

func makeSample(down float32) *Sample {
	return &Sample{Ts: time.Now(), DishStatus: &dev.DishGetStatusResponse{DownlinkThroughputBps: down, UplinkThroughputBps: 10, PopPingLatencyMs: 30}}
}

func TestPlaybackProviderLoopAndScale(t *testing.T) {
	samples := []*Sample{makeSample(1), makeSample(2), makeSample(3)}
	p := NewPlaybackProvider(samples, true, 1.5)
	var seen []float32
	for i := 0; i < 5; i++ {
		seen = append(seen, p.Next(time.Now()).DishStatus.DownlinkThroughputBps)
	}
	if len(seen) != 5 {
		t.Fatalf("expected 5 samples")
	}
	// Ensure looping occurred (last value should not be strictly monotonic sequence 1..3)
	if seen[4] == 5 {
		t.Fatalf("unexpected sequence")
	}
}

func TestBaselineProviderJitter(t *testing.T) {
	samples := []*Sample{makeSample(100)}
	b := NewBaselineProvider(samples, 42)
	s1 := b.Next(time.Now()).DishStatus.DownlinkThroughputBps
	s2 := b.Next(time.Now()).DishStatus.DownlinkThroughputBps
	if s1 == 100 || s2 == 100 {
		t.Fatalf("expected jitter applied")
	}
}
