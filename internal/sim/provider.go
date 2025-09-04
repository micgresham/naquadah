package sim

import (
	"encoding/json"
	"errors"
	"math/rand"
	"os"
	"sync"
	"time"

	dev "github.com/b0ch3nski/go-starlink/model/api-protoc/device"
)

// Sample represents one recorded snapshot of key responses.
type Sample struct {
	Ts          time.Time                         `json:"ts"`
	DishStatus  *dev.DishGetStatusResponse        `json:"dish_status,omitempty"`
	WifiStatus  *dev.WifiGetStatusResponse        `json:"wifi_status,omitempty"`
	WifiClients *dev.WifiGetClientsResponse       `json:"wifi_clients,omitempty"`
	Speedtest   *dev.SpeedTestResponse            `json:"speedtest,omitempty"`
	Transceiver *dev.TransceiverGetStatusResponse `json:"transceiver_status,omitempty"`
	PingAll     *dev.GetPingResponse              `json:"ping_all,omitempty"`
}

// DataProvider supplies samples to the core; nil result means fall back to random synthesis.
type DataProvider interface{ Next(now time.Time) *Sample }

// PlaybackProvider replays samples; scale>1 walks faster, <1 slower.
type PlaybackProvider struct {
	samples []*Sample
	loop    bool
	scale   float64
	mu      sync.Mutex
	cursorF float64
}

func NewPlaybackProvider(samples []*Sample, loop bool, scale float64) *PlaybackProvider {
	if scale <= 0 {
		scale = 1
	}
	return &PlaybackProvider{samples: samples, loop: loop, scale: scale}
}

func (p *PlaybackProvider) Next(now time.Time) *Sample {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.samples) == 0 {
		return nil
	}
	p.cursorF += p.scale
	idx := int(p.cursorF)
	if idx >= len(p.samples) {
		if !p.loop {
			return p.samples[len(p.samples)-1]
		}
		idx = idx % len(p.samples)
		p.cursorF = float64(idx)
	}
	return p.samples[idx]
}

// BaselineProvider with light jitter.
type BaselineProvider struct {
	base []*Sample
	mu   sync.Mutex
	idx  int
	rnd  *rand.Rand
}

func NewBaselineProvider(samples []*Sample, seed int64) *BaselineProvider {
	return &BaselineProvider{base: samples, rnd: rand.New(rand.NewSource(seed))}
}

func (b *BaselineProvider) Next(now time.Time) *Sample {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.base) == 0 {
		return nil
	}
	if b.idx >= len(b.base) {
		b.idx = 0
	}
	orig := b.base[b.idx]
	b.idx++
	out := *orig
	if out.DishStatus != nil {
		jitterPct := func(v float32) float32 { return v * (0.95 + b.rnd.Float32()*0.10) }
		out.DishStatus = &dev.DishGetStatusResponse{DeviceInfo: out.DishStatus.DeviceInfo, DeviceState: out.DishStatus.DeviceState, PopPingDropRate: out.DishStatus.PopPingDropRate, PopPingLatencyMs: jitterPct(out.DishStatus.PopPingLatencyMs), DownlinkThroughputBps: jitterPct(out.DishStatus.DownlinkThroughputBps), UplinkThroughputBps: jitterPct(out.DishStatus.UplinkThroughputBps), StowRequested: out.DishStatus.StowRequested, IsSnrAboveNoiseFloor: out.DishStatus.IsSnrAboveNoiseFloor}
	}
	if out.WifiStatus != nil {
		jitterPct := func(v float32) float32 { return v * (0.95 + b.rnd.Float32()*0.10) }
		out.WifiStatus = &dev.WifiGetStatusResponse{DeviceInfo: out.WifiStatus.DeviceInfo, DeviceState: out.WifiStatus.DeviceState, PopPingLatencyMs: jitterPct(out.WifiStatus.PopPingLatencyMs), PopPingDropRate: out.WifiStatus.PopPingDropRate, Ipv4WanAddress: out.WifiStatus.Ipv4WanAddress, DishId: out.WifiStatus.DishId, UtcNs: out.WifiStatus.UtcNs}
	}
	if out.Speedtest != nil {
		jitterPct := func(v float32) float32 { return v * (0.95 + b.rnd.Float32()*0.10) }
		out.Speedtest = &dev.SpeedTestResponse{
			DownloadMbps_1TcpConn:  jitterPct(out.Speedtest.DownloadMbps_1TcpConn),
			UploadMbps_1TcpConn:    jitterPct(out.Speedtest.UploadMbps_1TcpConn),
			DownloadMbps_4TcpConn:  jitterPct(out.Speedtest.DownloadMbps_4TcpConn),
			UploadMbps_4TcpConn:    jitterPct(out.Speedtest.UploadMbps_4TcpConn),
			DownloadMbps_16TcpConn: jitterPct(out.Speedtest.DownloadMbps_16TcpConn),
			UploadMbps_16TcpConn:   jitterPct(out.Speedtest.UploadMbps_16TcpConn),
			DownloadMbps_64TcpConn: out.Speedtest.DownloadMbps_64TcpConn,
			UploadMbps_64TcpConn:   out.Speedtest.UploadMbps_64TcpConn,
			RouterSpeedtest:        out.Speedtest.RouterSpeedtest,
		}
	}
	return &out
}

// LoadSamples reads a JSON array of Sample objects.
func LoadSamples(path string) ([]*Sample, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var raw []Sample
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, err
	}
	out := make([]*Sample, 0, len(raw))
	for i := range raw {
		out = append(out, &raw[i])
	}
	return out, nil
}

// Recorder polls the core (or an external real target in future) and appends samples to a file.
type Recorder struct {
	interval time.Duration
	path     string
	core     *Core
	stopCh   chan struct{}
}

func NewRecorder(core *Core, path string, interval time.Duration) *Recorder {
	return &Recorder{core: core, path: path, interval: interval, stopCh: make(chan struct{})}
}

func (r *Recorder) Start() { go r.loop() }
func (r *Recorder) Stop()  { close(r.stopCh) }

func (r *Recorder) loop() {
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			r.captureOnce()
		case <-r.stopCh:
			return
		}
	}
}

func (r *Recorder) captureOnce() {
	// Build synthetic snapshot
	dish := r.core.randDishStatus()
	wifi := r.core.randWifiStatus()
	clients := r.core.randWifiClients()
	speed := r.core.randSpeedtest()
	ping := r.core.randPingAll()
	s := Sample{Ts: time.Now().UTC(), DishStatus: dish, WifiStatus: wifi, WifiClients: clients, Speedtest: speed, PingAll: ping, Transceiver: &dev.TransceiverGetStatusResponse{}}
	// Append atomically: read existing, append, rewrite
	existing, _ := LoadSamples(r.path)
	existing = append(existing, &s)
	tmp := r.path + ".tmp"
	b, _ := json.MarshalIndent(existing, "", "  ")
	_ = os.WriteFile(tmp, b, 0o644)
	_ = os.Rename(tmp, r.path)
}

// BuildRecorderFromFlags configures provider/recorder based on CLI style inputs.
func BuildProvider(playbackPath string, playbackLoop bool, playbackScale float64, baselinePath string, seed int64) (DataProvider, error) {
	if playbackPath != "" {
		sm, err := LoadSamples(playbackPath)
		if err != nil {
			return nil, err
		}
		return NewPlaybackProvider(sm, playbackLoop, playbackScale), nil
	}
	if baselinePath != "" {
		sm, err := LoadSamples(baselinePath)
		if err != nil {
			return nil, err
		}
		return NewBaselineProvider(sm, seed), nil
	}
	return nil, nil
}

// Simple helper to ensure at least one mode chosen when required.
var ErrNoSamples = errors.New("no samples available")
