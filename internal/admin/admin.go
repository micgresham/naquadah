package admin

import (
	"encoding/json"
	"math"
	"math/rand"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	dev "github.com/b0ch3nski/go-starlink/model/api-protoc/device"
	"github.com/b0ch3nski/go-starlink/model/internal/auth"
)

// State holds override and weather simulation state for the admin UI.
type State struct {
	mu        sync.RWMutex
	alarms    map[string]bool
	fields    map[string]float64
	rawFields map[string]string

	ErrorNext struct {
		Enable bool
		Code   int32
		Msg    string
	}

	obstruction []float32

	// Latest synthesized (weather) obstruction grid (8x8) when no manual override
	weatherGrid   []float32
	effectiveGrid []float32

	// Rain fade simulation
	rain struct {
		Active          bool
		Intensity       float64       // 0..1 severity multiplier
		Duration        time.Duration // active per iteration
		Iterations      int           // 0=infinite
		Delay           time.Duration // between iterations
		StartedAt       time.Time
		Iter            int
		Residual        float64 // post-iteration residual severity (decays)
		ResidualUpdated time.Time
		PathStartX      int
		PathStartY      int
		PathEndX        int
		PathEndY        int
		PathValid       bool
		ExtraCells      []struct{ X, Y int }
	}

	// Snow accumulation simulation (adds heating alert, heavier obstruction, milder latency)
	snow struct {
		Active          bool
		Intensity       float64
		Duration        time.Duration
		Iterations      int
		Delay           time.Duration
		StartedAt       time.Time
		Iter            int
		Residual        float64
		ResidualUpdated time.Time
		Holes           []int // indices of cleared (non-obstructed) cells under snow
	}

	// lastDish snapshot for UI impacted fields section
	lastDish *dev.DishGetStatusResponse

	// lastAlerts holds the most recently synthesized dish alerts (dynamic + effects)
	lastAlerts map[string]bool
}

func NewState() *State {
	return &State{alarms: map[string]bool{}, fields: map[string]float64{}, rawFields: map[string]string{}, lastAlerts: map[string]bool{}}
}

// ApplyDish mutates a generated dish status response in-place according to overrides.
func (s *State) ApplyDish(d *dev.DishGetStatusResponse) {
	if d == nil {
		return
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.alarms) > 0 {
		if d.Alerts == nil {
			d.Alerts = &dev.DishAlerts{}
		}
		// Reflect-like manual mapping
		for k, v := range s.alarms {
			switch k {
			case "motors_stuck":
				d.Alerts.MotorsStuck = v
			case "thermal_throttle":
				d.Alerts.ThermalThrottle = v
			case "thermal_shutdown":
				d.Alerts.ThermalShutdown = v
			case "mast_not_near_vertical":
				d.Alerts.MastNotNearVertical = v
			case "unexpected_location":
				d.Alerts.UnexpectedLocation = v
			case "slow_ethernet_speeds":
				d.Alerts.SlowEthernetSpeeds = v
			case "roaming":
				d.Alerts.Roaming = v
			case "install_pending":
				d.Alerts.InstallPending = v
			case "is_heating":
				d.Alerts.IsHeating = v
			case "power_supply_thermal_throttle":
				d.Alerts.PowerSupplyThermalThrottle = v
			case "is_power_save_idle":
				d.Alerts.IsPowerSaveIdle = v
			case "moving_while_not_mobile":
				d.Alerts.MovingWhileNotMobile = v
			case "moving_too_fast_for_policy":
				d.Alerts.MovingTooFastForPolicy = v
			case "dbf_telem_stale":
				d.Alerts.DbfTelemStale = v
			case "low_motor_current":
				d.Alerts.LowMotorCurrent = v
			case "lower_signal_than_predicted":
				d.Alerts.LowerSignalThanPredicted = v
			}
		}
	}
	for k, val := range s.fields {
		handled := true
		switch k {
		case "dish.downlink_throughput_bps":
			d.DownlinkThroughputBps = float32(val)
		case "dish.uplink_throughput_bps":
			d.UplinkThroughputBps = float32(val)
		case "dish.pop_ping_latency_ms":
			d.PopPingLatencyMs = float32(val)
		case "dish.eth_speed_mbps":
			d.EthSpeedMbps = int32(val)
		case "dish.pop_ping_drop_rate":
			d.PopPingDropRate = float32(val)
		case "dish.obstruction_fraction":
			if d.ObstructionStats == nil {
				d.ObstructionStats = &dev.DishObstructionStats{}
			}
			d.ObstructionStats.FractionObstructed = float32(val)
		case "dish.boresight_azimuth_deg":
			d.BoresightAzimuthDeg = float32(val)
			if d.AlignmentStats == nil {
				d.AlignmentStats = &dev.AlignmentStats{}
			}
			d.AlignmentStats.BoresightAzimuthDeg = float32(val)
		case "dish.boresight_elevation_deg":
			d.BoresightElevationDeg = float32(val)
			if d.AlignmentStats == nil {
				d.AlignmentStats = &dev.AlignmentStats{}
			}
			d.AlignmentStats.BoresightElevationDeg = float32(val)
		case "dish.software_update_progress_pct":
			if d.SoftwareUpdateStats == nil {
				d.SoftwareUpdateStats = &dev.SoftwareUpdateStats{}
			}
			d.SoftwareUpdateStats.SoftwareUpdateProgress = float32(val)
			p := val
			if p <= 0 {
				d.SoftwareUpdateState = dev.SoftwareUpdateState_IDLE
			} else if p < 100 {
				d.SoftwareUpdateState = dev.SoftwareUpdateState_FETCHING
			} else {
				d.SoftwareUpdateState = dev.SoftwareUpdateState_POST_CHECK
			}
		case "dish.uptime_s":
			if d.DeviceState == nil {
				d.DeviceState = &dev.DeviceState{}
			}
			d.DeviceState.UptimeS = uint64(val)
		default:
			handled = false
		}
		if !handled {
			_ = applyReflectOverrideNumeric(d, k, val)
		}
	}
	for k, raw := range s.rawFields {
		_ = applyReflectOverrideRaw(d, k, raw)
	}

	// Rain fade attenuation (after explicit overrides so it degrades resulting numbers)
	// Weather: rain fade dynamic portion
	if s.rain.Active {
		att := s.rainAttenuationFactor()
		if att > 0 {
			sev := att * s.rain.Intensity
			if sev > 1 {
				sev = 1
			}
			s.applyRainEffects(d, sev)
		}
	}
	// Rain residual decay (exponential) after iterations complete
	if !s.rain.Active && s.rain.Residual > 0 {
		s.mu.Lock()
		decay := math.Exp(-time.Since(s.rain.ResidualUpdated).Minutes() / 5) // 5 min half-ish life
		sev := s.rain.Residual * decay
		if sev < 0.01 {
			sev = 0
		}
		s.rain.Residual = sev
		s.rain.ResidualUpdated = time.Now()
		res := sev
		s.mu.Unlock()
		if res > 0 {
			s.applyRainEffects(d, res)
		}
	}

	// Snow accumulation dynamic
	if s.snow.Active {
		att := s.snowAttenuationFactor()
		if att > 0 {
			sev := att * s.snow.Intensity
			if sev > 1 {
				sev = 1
			}
			s.applySnowEffects(d, sev)
		}
	}
	// Snow residual decay
	if !s.snow.Active && s.snow.Residual > 0 {
		s.mu.Lock()
		decay := math.Exp(-time.Since(s.snow.ResidualUpdated).Minutes() / 10) // slower melt
		sev := s.snow.Residual * decay
		if sev < 0.01 {
			sev = 0
		}
		s.snow.Residual = sev
		s.snow.ResidualUpdated = time.Now()
		res := sev
		s.mu.Unlock()
		if res > 0 {
			s.applySnowEffects(d, res)
		}
	}
	// Consistency: ensure nested alignment matches top-level boresight after overrides
	if d.AlignmentStats != nil {
		if d.AlignmentStats.BoresightAzimuthDeg != d.BoresightAzimuthDeg {
			d.AlignmentStats.BoresightAzimuthDeg = d.BoresightAzimuthDeg
		}
		if d.AlignmentStats.BoresightElevationDeg != d.BoresightElevationDeg {
			d.AlignmentStats.BoresightElevationDeg = d.BoresightElevationDeg
		}
	}
	if len(s.obstruction) == 64 && d.ObstructionStats != nil {
		// Map obstruction: treat low random quality by adjusting FractionObstructed based on holes count
		holes := 0
		for _, v := range s.obstruction {
			if v == 0 {
				holes++
			}
		}
		d.ObstructionStats.FractionObstructed = float32(holes) / 64.0
	}

	// Always synthesize dynamic weather grid
	combinedWeather, _, _ := s.buildWeatherGrid()
	// Compose effective grid: manual holes (if any) override weather (logical AND since 0 means obstructed)
	effective := make([]float32, 64)
	for i := 0; i < 64; i++ {
		mw := combinedWeather[i]
		if len(s.obstruction) == 64 { // manual mask exists
			mw = mw * s.obstruction[i]
		}
		effective[i] = mw
	}
	if d.ObstructionStats != nil {
		obstructed := 0
		for _, v := range effective {
			if v == 0 {
				obstructed++
			}
		}
		frac := float32(obstructed) / 64.0
		// Blend for smoother transitions
		if d.ObstructionStats.FractionObstructed == 0 {
			d.ObstructionStats.FractionObstructed = frac
		} else {
			d.ObstructionStats.FractionObstructed = (d.ObstructionStats.FractionObstructed*0.5 + frac*0.5)
		}
	}
	// store weather + effective copies asynchronously
	go func(wg, eg []float32) {
		wCopy := make([]float32, 64)
		copy(wCopy, wg)
		eCopy := make([]float32, 64)
		copy(eCopy, eg)
		s.mu.Lock()
		s.weatherGrid = wCopy
		s.effectiveGrid = eCopy
		s.mu.Unlock()
	}(combinedWeather, effective)

	// capture key fields + alerts for UI (avoid heavy copy). Do outside RLock via goroutine.
	clone := &dev.DishGetStatusResponse{DownlinkThroughputBps: d.DownlinkThroughputBps, UplinkThroughputBps: d.UplinkThroughputBps, PopPingLatencyMs: d.PopPingLatencyMs, PopPingDropRate: d.PopPingDropRate}
	if d.ObstructionStats != nil {
		clone.ObstructionStats = &dev.DishObstructionStats{FractionObstructed: d.ObstructionStats.FractionObstructed}
	}
	if d.GpsStats != nil {
		clone.GpsStats = &dev.DishGpsStats{GpsSats: d.GpsStats.GpsSats, GpsValid: d.GpsStats.GpsValid}
	}
	// build alerts map (dynamic actual alerts, not just manual overrides)
	alertsMap := map[string]bool{}
	if d.Alerts != nil {
		alertsMap["motors_stuck"] = d.Alerts.MotorsStuck
		alertsMap["thermal_throttle"] = d.Alerts.ThermalThrottle
		alertsMap["thermal_shutdown"] = d.Alerts.ThermalShutdown
		alertsMap["mast_not_near_vertical"] = d.Alerts.MastNotNearVertical
		alertsMap["unexpected_location"] = d.Alerts.UnexpectedLocation
		alertsMap["slow_ethernet_speeds"] = d.Alerts.SlowEthernetSpeeds
		alertsMap["roaming"] = d.Alerts.Roaming
		alertsMap["install_pending"] = d.Alerts.InstallPending
		alertsMap["is_heating"] = d.Alerts.IsHeating
		alertsMap["power_supply_thermal_throttle"] = d.Alerts.PowerSupplyThermalThrottle
		alertsMap["is_power_save_idle"] = d.Alerts.IsPowerSaveIdle
		alertsMap["moving_while_not_mobile"] = d.Alerts.MovingWhileNotMobile
		alertsMap["moving_too_fast_for_policy"] = d.Alerts.MovingTooFastForPolicy
		alertsMap["dbf_telem_stale"] = d.Alerts.DbfTelemStale
		alertsMap["low_motor_current"] = d.Alerts.LowMotorCurrent
		alertsMap["lower_signal_than_predicted"] = d.Alerts.LowerSignalThanPredicted
	}
	go func(cpy *dev.DishGetStatusResponse, am map[string]bool) {
		s.mu.Lock()
		s.lastDish = cpy
		s.lastAlerts = am
		s.mu.Unlock()
	}(clone, alertsMap)
}

// buildWeatherGrid returns combined (0 obstructed), snow layer, rain layer
func (s *State) buildWeatherGrid() (combined, snowLayer, rainLayer []float32) {
	combined = make([]float32, 64)
	snowLayer = make([]float32, 64)
	rainLayer = make([]float32, 64)
	for i := 0; i < 64; i++ {
		combined[i], snowLayer[i], rainLayer[i] = 1, 1, 1
	}
	// snow
	if s.snow.Active || s.snow.Residual > 0 {
		sev := s.snow.Intensity
		if !s.snow.Active {
			sev = s.snow.Residual
		}
		cells := int(sev * 64 * 0.8)
		if cells > 63 {
			cells = 63
		}
		r := rand.New(rand.NewSource(time.Now().UnixNano()))
		indices := r.Perm(64)
		for i := 0; i < cells; i++ {
			snowLayer[indices[i]] = 0
		}
		if sev > 0.2 {
			clearN := int(float64(cells) * 0.15)
			for i := 0; i < clearN && i < 64; i++ {
				snowLayer[indices[len(indices)-1-i]] = 1
			}
		}
	}
	// rain moving cell + extra cells
	if s.rain.Active || s.rain.Residual > 0 {
		sev := s.rain.Intensity
		if !s.rain.Active {
			sev = s.rain.Residual
		}
		if !s.rain.PathValid {
			s.rain.PathStartX, s.rain.PathStartY, s.rain.PathEndX, s.rain.PathEndY = 0, 0, 7, 7
			s.rain.PathValid = true
		}
		progress := s.rainAttenuationFactor()
		if !s.rain.Active {
			progress = 1 - math.Exp(-sev)
		}
		cx := float64(s.rain.PathStartX) + (float64(s.rain.PathEndX)-float64(s.rain.PathStartX))*progress
		cy := float64(s.rain.PathStartY) + (float64(s.rain.PathEndY)-float64(s.rain.PathStartY))*progress
		radius := 1.0 + 2.5*sev
		for y := 0; y < 8; y++ {
			for x := 0; x < 8; x++ {
				dx := float64(x) - cx
				dy := float64(y) - cy
				if dx*dx+dy*dy <= radius*radius {
					rainLayer[y*8+x] = 0
				}
			}
		}
		for _, c := range s.rain.ExtraCells {
			cx2, cy2 := float64(c.X), float64(c.Y)
			for y := 0; y < 8; y++ {
				for x := 0; x < 8; x++ {
					dx := float64(x) - cx2
					dy := float64(y) - cy2
					if dx*dx+dy*dy <= (0.8+1.2*sev)*(0.8+1.2*sev) {
						rainLayer[y*8+x] = 0
					}
				}
			}
		}
	}
	for i := 0; i < 64; i++ {
		if snowLayer[i] == 0 || rainLayer[i] == 0 {
			combined[i] = 0
		}
	}
	return
}

// SetAlarm sets or clears an alarm override.
func (s *State) SetAlarm(name string, value bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if value {
		s.alarms[name] = true
	} else {
		delete(s.alarms, name)
	}
}

// ClearAllAlarms removes all overrides.
func (s *State) ClearAllAlarms() { s.mu.Lock(); s.alarms = map[string]bool{}; s.mu.Unlock() }

func (s *State) SetField(name string, value float64) {
	s.mu.Lock()
	s.fields[name] = value
	s.mu.Unlock()
}
func (s *State) SetRawField(name, value string) {
	s.mu.Lock()
	s.rawFields[name] = value
	s.mu.Unlock()
}
func (s *State) ClearField(name string) {
	s.mu.Lock()
	delete(s.fields, name)
	delete(s.rawFields, name)
	s.mu.Unlock()
}

func (s *State) SetObstructionHole(x, y int) {
	if x < 0 || y < 0 || x >= 8 || y >= 8 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.obstruction == nil {
		s.obstruction = make([]float32, 64)
		for i := range s.obstruction {
			s.obstruction[i] = 1
		}
	}
	// Incoming y is bottom-origin (0 at bottom). Convert to internal top-origin index.
	internalY := 7 - y
	s.obstruction[internalY*8+x] = 0
}

// SetObstructionValue sets a specific cell to 0 or 1 (manual override grid created lazily).
func (s *State) SetObstructionValue(x, y int, val int) {
	if x < 0 || y < 0 || x >= 8 || y >= 8 {
		return
	}
	if val != 0 && val != 1 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.obstruction == nil {
		s.obstruction = make([]float32, 64)
		for i := range s.obstruction {
			s.obstruction[i] = 1
		}
	}
	internalY := 7 - y
	s.obstruction[internalY*8+x] = float32(val)
}

func (s *State) RandomizeObstruction() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.obstruction = make([]float32, 64)
	for i := range s.obstruction {
		if rand.Float32() < 0.1 {
			s.obstruction[i] = 0
		} else {
			s.obstruction[i] = 1
		}
	}
}

// Snapshot returns current override state (redacted)
func (s *State) Snapshot() map[string]interface{} {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var last map[string]interface{}
	if s.lastDish != nil {
		last = map[string]interface{}{
			"downlink_bps":        s.lastDish.DownlinkThroughputBps,
			"uplink_bps":          s.lastDish.UplinkThroughputBps,
			"pop_ping_latency_ms": s.lastDish.PopPingLatencyMs,
			"pop_ping_drop_rate":  s.lastDish.PopPingDropRate,
			"obstruction_fraction": func() float32 {
				if s.lastDish.ObstructionStats != nil {
					return s.lastDish.ObstructionStats.FractionObstructed
				}
				return 0
			}(),
			"gps_sats": func() uint32 {
				if s.lastDish.GpsStats != nil {
					return s.lastDish.GpsStats.GpsSats
				}
				return 0
			}(),
			"gps_valid": func() bool {
				if s.lastDish.GpsStats != nil {
					return s.lastDish.GpsStats.GpsValid
				}
				return false
			}(),
		}
	}
	return map[string]interface{}{
		"alarms":         s.alarms,
		"effective_grid": s.effectiveGrid,
		"dish_alerts":    s.lastAlerts,
		"fields":         s.fields,
		"raw_fields":     s.rawFields,
		"error_next":     s.ErrorNext,
		"obstruction":    s.obstruction,
		"weather_grid":   s.weatherGrid,
		"last_dish":      last,
		"rain": map[string]interface{}{
			"active":      s.rain.Active,
			"intensity":   s.rain.Intensity,
			"duration_ms": s.rain.Duration.Milliseconds(),
			"iterations":  s.rain.Iterations,
			"delay_ms":    s.rain.Delay.Milliseconds(),
			"iter":        s.rain.Iter,
			"path":        map[string]int{"start_x": s.rain.PathStartX, "start_y": s.rain.PathStartY, "end_x": s.rain.PathEndX, "end_y": s.rain.PathEndY},
			"extra_cells": s.rain.ExtraCells,
		},
		"snow": map[string]interface{}{
			"active":      s.snow.Active,
			"intensity":   s.snow.Intensity,
			"duration_ms": s.snow.Duration.Milliseconds(),
			"iterations":  s.snow.Iterations,
			"delay_ms":    s.snow.Delay.Milliseconds(),
			"iter":        s.snow.Iter,
		},
	}
}

// ConsumeError atomically returns and clears a pending one-shot injected error.
func (s *State) ConsumeError() (bool, int32, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.ErrorNext.Enable {
		return false, 0, ""
	}
	code := s.ErrorNext.Code
	msg := s.ErrorNext.Msg
	s.ErrorNext.Enable = false
	return true, code, msg
}

// HTTP wiring
// Handler returns full admin UI + API (legacy behavior).
func (s *State) Handler() http.Handler { return s.HandlerAPI(true) }

// HandlerAPI returns only API endpoints; includeUI optionally adds embedded HTML UI.
func (s *State) HandlerAPI(includeUI bool) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/alarms", s.handleAlarms)
	mux.HandleFunc("/api/fields", s.handleFields)
	mux.HandleFunc("/api/error", s.handleError)
	mux.HandleFunc("/api/obstruction", s.handleObstruction)
	mux.HandleFunc("/api/rainfade", s.handleRainFade)
	mux.HandleFunc("/api/snow", s.handleSnow)
	mux.HandleFunc("/api/weather", s.handleWeather)
	mux.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "ts": time.Now().Unix()})
	})
	// Versioned endpoints
	mux.HandleFunc("/api/v1/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "ts": time.Now().Unix()})
	})
	mux.HandleFunc("/api/v1/alarms", s.handleAlarms)
	mux.HandleFunc("/api/v1/fields", s.handleFields)
	mux.HandleFunc("/api/v1/error", s.handleError)
	mux.HandleFunc("/api/v1/obstruction", s.handleObstruction)
	mux.HandleFunc("/api/v1/rainfade", s.handleRainFade)
	mux.HandleFunc("/api/v1/snow", s.handleSnow)
	mux.HandleFunc("/api/v1/weather", s.handleWeather)
	if includeUI {
		mux.HandleFunc("/", serveIndex)
	}
	return mux
}

// HandlerWithAuth returns handler with UI + auth.
func (s *State) HandlerWithAuth(cfg auth.Config) http.Handler {
	return s.HandlerWithAuthOptions(cfg, true)
}

// HandlerWithAuthOptions returns handler with optional UI + auth.
func (s *State) HandlerWithAuthOptions(cfg auth.Config, includeUI bool) http.Handler {
	base := s.HandlerAPI(includeUI)
	if !cfg.Enabled {
		return base
	}
	v := auth.New(cfg)
	return v.Middleware(base)
}

// Weather endpoint: GET returns snapshot with weather grid; POST updates path / extra cells
func (s *State) handleWeather(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		resp := s.Snapshot()
		s.respondJSON(w, resp)
	case http.MethodPost:
		var body struct {
			RainPath *struct {
				StartX int `json:"start_x"`
				StartY int `json:"start_y"`
				EndX   int `json:"end_x"`
				EndY   int `json:"end_y"`
			} `json:"rain_path"`
			ExtraCells  *[]struct{ X, Y int } `json:"extra_rain_cells"`
			ClearManual bool                  `json:"clear_manual"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		s.mu.Lock()
		if body.RainPath != nil {
			s.rain.PathStartX, s.rain.PathStartY = body.RainPath.StartX, body.RainPath.StartY
			s.rain.PathEndX, s.rain.PathEndY = body.RainPath.EndX, body.RainPath.EndY
			s.rain.PathValid = true
		}
		if body.ExtraCells != nil {
			s.rain.ExtraCells = *body.ExtraCells
		}
		if body.ClearManual {
			s.obstruction = nil
		}
		s.mu.Unlock()
		s.respondJSON(w, s.Snapshot())
	default:
		w.WriteHeader(405)
	}
}

func (s *State) handleAlarms(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.respondJSON(w, s.Snapshot())
	case http.MethodPost:
		var body struct {
			Name  string `json:"name"`
			Value bool   `json:"value"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		if body.Name == "__clear_all__" {
			s.ClearAllAlarms()
		} else {
			s.SetAlarm(strings.ToLower(body.Name), body.Value)
		}
		s.respondJSON(w, s.Snapshot())
	default:
		w.WriteHeader(405)
	}
}

func (s *State) handleFields(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		var body struct {
			Name  string   `json:"name"`
			Value *float64 `json:"value"`
			Raw   *string  `json:"raw"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		if body.Value != nil {
			s.SetField(body.Name, *body.Value)
		} else if body.Raw != nil {
			s.SetRawField(body.Name, *body.Raw)
		} else {
			s.ClearField(body.Name)
		}
		s.respondJSON(w, s.Snapshot())
	default:
		w.WriteHeader(405)
	}
}

func (s *State) handleError(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		var body struct {
			Enable bool   `json:"enable"`
			Code   int32  `json:"code"`
			Msg    string `json:"msg"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		s.mu.Lock()
		s.ErrorNext.Enable = body.Enable
		s.ErrorNext.Code = body.Code
		s.ErrorNext.Msg = body.Msg
		s.mu.Unlock()
		s.respondJSON(w, s.Snapshot())
	default:
		w.WriteHeader(405)
	}
}

func (s *State) handleObstruction(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		var body struct {
			X         *int `json:"x"`
			Y         *int `json:"y"`
			Value     *int `json:"value"`
			Randomize bool `json:"randomize"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		if body.Randomize {
			s.RandomizeObstruction()
		} else if body.X != nil && body.Y != nil {
			if body.Value != nil {
				s.SetObstructionValue(*body.X, *body.Y, *body.Value)
			} else {
				s.SetObstructionHole(*body.X, *body.Y)
			}
		}
		s.respondJSON(w, s.Snapshot())
	case http.MethodGet:
		s.respondJSON(w, s.Snapshot())
	default:
		w.WriteHeader(405)
	}
}

func (s *State) handleRainFade(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		var body struct {
			Action      string   `json:"action"`
			Intensity   float64  `json:"intensity"`
			DurationMs  *int64   `json:"duration_ms,omitempty"`
			DelayMs     *int64   `json:"delay_ms,omitempty"`
			DurationSec *float64 `json:"duration_s,omitempty"`
			DelaySec    *float64 `json:"delay_s,omitempty"`
			Iterations  int      `json:"iterations"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		// Determine duration
		dur := time.Duration(0)
		if body.DurationSec != nil {
			dur = time.Duration(*body.DurationSec * float64(time.Second))
		} else if body.DurationMs != nil {
			dur = time.Duration(*body.DurationMs) * time.Millisecond
		}
		if dur <= 0 {
			dur = 30 * time.Second
		}
		delay := time.Duration(0)
		if body.DelaySec != nil {
			delay = time.Duration(*body.DelaySec * float64(time.Second))
		} else if body.DelayMs != nil {
			delay = time.Duration(*body.DelayMs) * time.Millisecond
		}
		s.mu.Lock()
		s.mu.Unlock()
		if strings.ToLower(body.Action) == "stop" {
			s.StopRainFade()
		} else {
			s.StartRainFade(body.Intensity, dur, body.Iterations, delay)
		}
		s.respondJSON(w, s.Snapshot())
	case http.MethodGet:
		s.respondJSON(w, s.Snapshot())
	default:
		w.WriteHeader(405)
	}
}

func (s *State) handleSnow(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		var body struct {
			Action      string   `json:"action"`
			Intensity   float64  `json:"intensity"`
			DurationMs  *int64   `json:"duration_ms,omitempty"`
			DelayMs     *int64   `json:"delay_ms,omitempty"`
			DurationSec *float64 `json:"duration_s,omitempty"`
			DelaySec    *float64 `json:"delay_s,omitempty"`
			Iterations  int      `json:"iterations"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		dur := time.Duration(0)
		if body.DurationSec != nil {
			dur = time.Duration(*body.DurationSec * float64(time.Second))
		} else if body.DurationMs != nil {
			dur = time.Duration(*body.DurationMs) * time.Millisecond
		}
		if dur <= 0 {
			dur = 60 * time.Second
		}
		delay := time.Duration(0)
		if body.DelaySec != nil {
			delay = time.Duration(*body.DelaySec * float64(time.Second))
		} else if body.DelayMs != nil {
			delay = time.Duration(*body.DelayMs) * time.Millisecond
		}
		if strings.ToLower(body.Action) == "stop" {
			s.StopSnow()
		} else {
			s.StartSnow(body.Intensity, dur, body.Iterations, delay)
		}
		s.respondJSON(w, s.Snapshot())
	case http.MethodGet:
		s.respondJSON(w, s.Snapshot())
	default:
		w.WriteHeader(405)
	}
}

// StartRainFade configures and activates rain fade cycles.
func (s *State) StartRainFade(intensity float64, duration time.Duration, iterations int, delay time.Duration) {
	if intensity < 0 {
		intensity = 0
	}
	if intensity > 1 {
		intensity = 1
	}
	if duration <= 0 {
		duration = 30 * time.Second
	}
	s.mu.Lock()
	s.rain.Active = true
	s.rain.Intensity = intensity
	s.rain.Duration = duration
	s.rain.Iterations = iterations
	s.rain.Delay = delay
	s.rain.StartedAt = time.Now()
	s.rain.Iter = 0
	s.mu.Unlock()
	go s.rainSupervisor()
}

func (s *State) StopRainFade() {
	s.mu.Lock()
	s.rain.Active = false
	s.mu.Unlock()
}

// rainSupervisor advances iterations and handles stop conditions.
func (s *State) rainSupervisor() {
	for {
		s.mu.RLock()
		active := s.rain.Active
		dur := s.rain.Duration
		maxIter := s.rain.Iterations
		delay := s.rain.Delay
		started := s.rain.StartedAt
		s.mu.RUnlock()
		if !active {
			return
		}
		now := time.Now()
		elapsed := now.Sub(started)
		if elapsed > dur { // iteration ended
			s.mu.Lock()
			if !s.rain.Active {
				// capture residual severity (last peak intensity)
				s.rain.Active = false
				s.rain.Residual = s.rain.Intensity * 0.6 // some leftover moisture
				s.rain.ResidualUpdated = time.Now()
				return
			}
			s.rain.Iter++
			if maxIter > 0 && s.rain.Iter >= maxIter {
				s.rain.Active = false
				s.mu.Unlock()
				return
			}
			// schedule next iteration
			s.rain.StartedAt = time.Now().Add(delay)
			s.mu.Unlock()
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// rainAttenuationFactor returns 0..1 progress inside current iteration (ease-in/out bell curve).
func (s *State) rainAttenuationFactor() float64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.rain.Active {
		return 0
	}
	now := time.Now()
	if now.Before(s.rain.StartedAt) {
		return 0
	}
	t := now.Sub(s.rain.StartedAt)
	if t < 0 || t > s.rain.Duration {
		return 0
	}
	// Normalize 0..1
	x := float64(t) / float64(s.rain.Duration)
	// Smooth bell-ish shape: 1 - (2x-1)^2
	y := 1 - (2*x-1)*(2*x-1)
	if y < 0 {
		y = 0
	}
	return y
}

// Snow supervisor similar to rain (optional future expansion)
func (s *State) StartSnow(intensity float64, duration time.Duration, iterations int, delay time.Duration) {
	if intensity < 0 {
		intensity = 0
	}
	if intensity > 1 {
		intensity = 1
	}
	if duration <= 0 {
		duration = 60 * time.Second
	}
	s.mu.Lock()
	s.snow.Active = true
	s.snow.Intensity = intensity
	s.snow.Duration = duration
	s.snow.Iterations = iterations
	s.snow.Delay = delay
	s.snow.StartedAt = time.Now()
	s.snow.Iter = 0
	s.mu.Unlock()
	go s.snowSupervisor()
}

func (s *State) StopSnow() {
	s.mu.Lock()
	s.snow.Active = false
	s.snow.Residual = s.snow.Intensity * 0.8
	s.snow.ResidualUpdated = time.Now()
	s.mu.Unlock()
}

func (s *State) snowSupervisor() {
	for {
		s.mu.RLock()
		active := s.snow.Active
		dur := s.snow.Duration
		maxIter := s.snow.Iterations
		delay := s.snow.Delay
		started := s.snow.StartedAt
		s.mu.RUnlock()
		if !active {
			return
		}
		now := time.Now()
		elapsed := now.Sub(started)
		if elapsed > dur {
			s.mu.Lock()
			if !s.snow.Active {
				s.mu.Unlock()
				return
			}
			s.snow.Iter++
			if maxIter > 0 && s.snow.Iter >= maxIter {
				s.snow.Active = false
				s.snow.Residual = s.snow.Intensity * 0.9
				s.snow.ResidualUpdated = time.Now()
				s.mu.Unlock()
				return
			}
			s.snow.StartedAt = time.Now().Add(delay)
			s.mu.Unlock()
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func (s *State) snowAttenuationFactor() float64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.snow.Active {
		return 0
	}
	now := time.Now()
	if now.Before(s.snow.StartedAt) {
		return 0
	}
	t := now.Sub(s.snow.StartedAt)
	if t < 0 || t > s.snow.Duration {
		return 0
	}
	x := float64(t) / float64(s.snow.Duration)
	// heavier plateau for snow: use sin curve squared
	y := math.Sin(x*math.Pi) * math.Sin(x*math.Pi)
	return y
}

// applyRainEffects clamps values after applying
func (s *State) applyRainEffects(d *dev.DishGetStatusResponse, sev float64) {
	// Stronger throughput & signal degradation: full severity => complete drop (0 bps)
	if d.DownlinkThroughputBps > 0 {
		scale := 1 - 0.95*sev
		if sev >= 0.999 {
			scale = 0
		}
		if scale < 0 {
			scale = 0
		}
		d.DownlinkThroughputBps *= float32(scale)
	}
	if d.UplinkThroughputBps > 0 {
		scale := 1 - 0.95*sev
		if sev >= 0.999 {
			scale = 0
		}
		if scale < 0 {
			scale = 0
		}
		d.UplinkThroughputBps *= float32(scale)
	}
	// Latency growth (non-linear)
	d.PopPingLatencyMs += float32(120 * math.Pow(sev, 1.1))
	// Drop rate pushes toward near total at high severity
	d.PopPingDropRate += float32(0.70 * math.Pow(sev, 1.3))
	if d.PopPingDropRate > 0.995 {
		d.PopPingDropRate = 0.995
	}
	if d.ObstructionStats == nil {
		d.ObstructionStats = &dev.DishObstructionStats{}
	}
	// Slightly stronger obstruction contribution
	d.ObstructionStats.FractionObstructed += float32(0.22 * sev)
	if d.ObstructionStats.FractionObstructed > 1 {
		d.ObstructionStats.FractionObstructed = 1
	}
	d.ObstructionStats.TimeObstructed += float32(10 * sev)
	d.ObstructionStats.AvgProlongedObstructionDurationS += float32(5 * sev)
	d.ObstructionStats.AvgProlongedObstructionIntervalS += float32(20 * sev)
	if d.GpsStats == nil {
		d.GpsStats = &dev.DishGpsStats{}
	}
	// GPS satellites: stronger non‑linear reduction with rain severity.
	baseSats := d.GpsStats.GpsSats
	if baseSats == 0 {
		baseSats = 30
	}
	// Factor curve: mild impact early, accelerates after sev>0.4
	// lossFrac ~ (0.12 + 0.78*sev) * sev^0.9  (caps below 0.9)
	lossFrac := (0.12 + 0.78*sev) * math.Pow(sev, 0.9)
	if lossFrac > 0.9 {
		lossFrac = 0.9
	}
	loss := uint32(float64(baseSats) * lossFrac)
	if loss >= baseSats {
		loss = baseSats - 1
	}
	remaining := baseSats - loss
	if remaining < 4 {
		remaining = 4
	} // keep a sensible floor to avoid zero
	d.GpsStats.GpsSats = remaining
	if sev > 0.85 {
		d.GpsStats.GpsValid = false
	}
	// Auto-manage lower_signal_than_predicted alarm: assert when heavy rain, clear when normal
	if d.Alerts == nil {
		d.Alerts = &dev.DishAlerts{}
	}
	if sev >= 0.65 {
		d.Alerts.LowerSignalThanPredicted = true
	} else if sev < 0.35 {
		// only clear if not manually forced via overrides map
		s.mu.RLock()
		_, forced := s.alarms["lower_signal_than_predicted"]
		s.mu.RUnlock()
		if !forced {
			d.Alerts.LowerSignalThanPredicted = false
		}
	}
}

func (s *State) applySnowEffects(d *dev.DishGetStatusResponse, sev float64) {
	// Snow throughput impact: slightly less aggressive than rain until high sev, then collapse
	if d.DownlinkThroughputBps > 0 {
		scale := 1 - 0.85*sev
		if sev >= 0.999 {
			scale = 0
		}
		if scale < 0 {
			scale = 0
		}
		d.DownlinkThroughputBps *= float32(scale)
	}
	if d.UplinkThroughputBps > 0 {
		scale := 1 - 0.85*sev
		if sev >= 0.999 {
			scale = 0
		}
		if scale < 0 {
			scale = 0
		}
		d.UplinkThroughputBps *= float32(scale)
	}
	// Latency & drop (gentler than rain)
	d.PopPingLatencyMs += float32(80 * math.Pow(sev, 1.1))
	d.PopPingDropRate += float32(0.50 * math.Pow(sev, 1.2))
	if d.PopPingDropRate > 0.99 {
		d.PopPingDropRate = 0.99
	}
	if d.ObstructionStats == nil {
		d.ObstructionStats = &dev.DishObstructionStats{}
	}
	d.ObstructionStats.FractionObstructed += float32(0.30 * sev)
	if d.ObstructionStats.FractionObstructed > 1 {
		d.ObstructionStats.FractionObstructed = 1
	}
	d.ObstructionStats.TimeObstructed += float32(15 * sev)
	d.ObstructionStats.AvgProlongedObstructionDurationS += float32(8 * sev)
	d.ObstructionStats.AvgProlongedObstructionIntervalS += float32(25 * sev)
	if d.GpsStats == nil {
		d.GpsStats = &dev.DishGpsStats{}
	}
	baseSats := d.GpsStats.GpsSats
	if baseSats == 0 {
		baseSats = 30
	}
	// Snow reduces a bit less aggressively; quadratic easing.
	lossFrac := 0.4 * (sev * sev) // max 0.4 at sev=1
	loss := uint32(float64(baseSats) * lossFrac)
	if loss >= baseSats {
		loss = baseSats - 1
	}
	remaining := baseSats - loss
	if remaining < 5 {
		remaining = 5
	}
	d.GpsStats.GpsSats = remaining
	// Simulate heater (is_heating alert) when snow severity moderate
	if d.Alerts == nil {
		d.Alerts = &dev.DishAlerts{}
	}
	if sev > 0.2 {
		d.Alerts.IsHeating = true
	}
	// At heavy accumulation raise lower signal alert & possibly invalidate GPS
	if sev > 0.7 {
		d.Alerts.LowerSignalThanPredicted = true
	}
	if sev > 0.85 {
		d.GpsStats.GpsValid = false
	}
}

func (s *State) respondJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func serveIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(indexHTML))
}

const indexHTML = `<!doctype html><html><head><title>Naquadah Admin</title><style>
:root{--bg:#121416;--panel:#1f2428;--panel-alt:#252c31;--border:#30383f;--text:#e2e8ec;--accent:#1976d2;--accent-alt:#64b5f6;--bad:#d32f2f;--good:#2e7d32;font-size:15px}
body{font-family:system-ui;margin:0;background:var(--bg);color:var(--text);-webkit-font-smoothing:antialiased}
header{padding:1rem 1.2rem;background:#0d1114;border-bottom:1px solid var(--border);display:flex;align-items:center;gap:1rem}
h1{margin:0;font-size:1.25rem;letter-spacing:.5px}
main{padding:1.1rem;margin:0}
section{margin-bottom:1.6rem;background:var(--panel);padding:1rem 1rem .9rem;border:1px solid var(--border);border-radius:10px;box-shadow:0 2px 4px -2px #0008}
section h2{margin:.1rem 0 .75rem;font-size:1rem;font-weight:600;letter-spacing:.5px;color:var(--accent-alt)}
button,select,input{font:inherit}
button{margin:2px;padding:6px 12px;border:1px solid var(--border);background:var(--panel-alt);cursor:pointer;border-radius:6px;font-size:.75rem;color:var(--text);transition:.15s ease;background-image:linear-gradient(#2c343a,#232a2f)}
button:hover{filter:brightness(1.15)}
button:active{transform:translateY(1px)}
button.toggle{padding:4px 9px}
button.toggle.active{background:var(--bad);border-color:#a31919;color:#fff}
fieldset{border:1px solid var(--border);padding:8px;border-radius:6px}
#gridWrapper{display:inline-block;position:relative;margin-top:8px;padding-left:18px;padding-top:18px}
#gridXLabels{display:flex;gap:3px;position:absolute;top:0;left:18px}
#gridXLabels span{width:24px;text-align:center;font-size:10px;color:#ccc}
#gridYLabels{position:absolute;left:0;top:18px;display:flex;flex-direction:column;gap:3px}
#gridYLabels span{height:24px;display:flex;align-items:center;justify-content:center;font-size:10px;color:#ccc;width:18px}
#grid{display:grid;grid-template-columns:repeat(8,24px);gap:3px}
#grid div{width:24px;height:24px;background:var(--good);cursor:pointer;border-radius:4px;box-shadow:inset 0 0 0 1px #0006}
#grid div.hole{background:#111}
canvas#weatherCanvas{image-rendering:pixelated;border:1px solid var(--border);margin-top:6px;background:#000}
code{background:#1e2529;padding:2px 4px;border-radius:4px;color:#9ecbff}
select,input{margin:2px;background:var(--panel-alt);border:1px solid var(--border);color:var(--text);padding:4px 6px;border-radius:6px}
input[type=range]{width:130px}
details{margin-top:.5rem}
details summary{cursor:pointer;font-weight:600;color:var(--accent)}
.kv{font-size:.7rem;display:grid;grid-template-columns:140px 1fr;gap:2px 8px;margin-top:.4rem}
.kv div.label{opacity:.7}
.statusline{font-size:.75rem;margin-top:.35rem}
.flex-row{display:flex;flex-wrap:wrap;align-items:center;gap:.75rem}
hr{border:none;border-top:1px solid var(--border);margin:8px 0}
a{text-decoration:none;color:var(--accent-alt)}
.hb-status{position:fixed;bottom:8px;right:12px;font-size:.65rem;padding:4px 8px;border-radius:6px;background:#22333a;color:#9ecbff;opacity:.85;z-index:999;transition:.25s}
.hb-status.bad{background:#471d1d;color:#ffb4b4}
.hb-status.warn{background:#463d22;color:#ffe6a1}
</style></head><body><header><h1>Naquadah Admin</h1><span style="font-size:.75rem;opacity:.6">Weather & Overrides</span>
<div style="margin-left:auto;display:flex;align-items:center;gap:.5rem;font-size:.75rem">
	<button id="manualRefresh" style="padding:4px 8px">Refresh Now</button>
	<label style="display:flex;align-items:center;gap:4px">Auto
		<input type="checkbox" id="autoToggle" checked/>
	</label>
	<label>Interval(s)<input id="refreshInterval" type="number" min="1" value="60" style="width:50px"/></label>
</div>
</header><main>
<section>
	<h2>Alarms</h2>
	<p style="margin-top:0">Click to toggle individual dish alert flags. <button onclick="clearAlarms()">Clear All</button></p>
	<div id="alarmButtons"></div>
</section>
<section>
	<h2>Field Overrides</h2>
	<p style="margin-top:0">Select a field and value to force; clear removes override.</p>
	<select id="fieldSelect"></select>
	<input id="fieldVal" placeholder="value" size=10 />
	<button onclick="setField()">Set</button>
	<button onclick="clearField()">Clear</button>
	<div style="margin-top:6px" class="flex-row">
		<input id="customFieldName" placeholder="custom.path (e.g. dish.device_state.uptime_s)" size=52 />
		<input id="customFieldVal" placeholder="value/raw" size=12 />
		<button onclick="setCustomField()">Set Custom</button>
		<button onclick="clearCustomField()">Clear Custom</button>
	</div>
	<div style="margin-top:6px"><strong>Active:</strong> <span id="fields"></span></div>
</section>
<section>
	<h2>Combined Grid / Storm Path</h2>
	<div style="display:flex;gap:1rem;flex-wrap:wrap;align-items:flex-start">
		<div style="position:relative;padding-left:22px;padding-top:18px;display:inline-block">
			<div id="weatherXLabels" style="position:absolute;top:0;left:22px;display:flex;gap:0"></div>
			<div id="weatherYLabels" style="position:absolute;left:0;top:18px;display:flex;flex-direction:column;gap:0"></div>
			<canvas id="weatherCanvas" width="160" height="160" title="8x8 Combined Grid"></canvas>
			<div style="font-size:12px;margin-top:4px;max-width:260px">Legend: black=obstructed (weather or manual), green=clear. Yellow outline = manual-only override. Click toggles manual cell. Randomize seeds manual holes. Clear Manual removes all overrides.</div>
		</div>
		<div style="min-width:230px">
			<div><strong>Rain Path</strong></div>
			<label>SX<input id="pathSX" size=2 value="0"/></label>
			<label>SY<input id="pathSY" size=2 value="0"/></label>
			<label>EX<input id="pathEX" size=2 value="7"/></label>
			<label>EY<input id="pathEY" size=2 value="7"/></label>
			<button onclick="updatePath()">Update</button>
			<hr/>
			<div><strong>Extra Rain Cells</strong></div>
			<div id="extraCells" style="font-size:12px"></div>
			<label>X<input id="cellX" size=2/></label>
			<label>Y<input id="cellY" size=2/></label>
			<button onclick="addCell()">Add</button>
			<button onclick="clearManual()">Clear Manual Override</button>
			<button onclick="randomize()">Randomize</button>
		</div>
	</div>
</section>
<section>
	<h2>Error Injection (next request only)</h2>
	<select id="errCode">
		<option value="14">UNAVAILABLE (14)</option>
		<option value="2">UNKNOWN (2)</option>
		<option value="3">INVALID_ARGUMENT (3)</option>
		<option value="4">DEADLINE_EXCEEDED (4)</option>
		<option value="5">NOT_FOUND (5)</option>
		<option value="7">PERMISSION_DENIED (7)</option>
		<option value="8">RESOURCE_EXHAUSTED (8)</option>
		<option value="9">FAILED_PRECONDITION (9)</option>
		<option value="10">ABORTED (10)</option>
		<option value="11">OUT_OF_RANGE (11)</option>
		<option value="12">UNIMPLEMENTED (12)</option>
		<option value="13">INTERNAL (13)</option>
		<option value="15">DATA_LOSS (15)</option>
		<option value="16">UNAUTHENTICATED (16)</option>
	</select>
	<input id="errMsg" placeholder="message" size=20 />
	<button onclick="injectError()">Inject</button>
	<button onclick="disableError()">Disable</button>
</section>
<section>
	<h2>Rain Fade</h2>
	<div class="flex-row">
		<label>Intensity (log 0-10)
			<input type="range" min=0 max=10 step=0.1 id="rainIntensity" value="5" oninput="updateRainDisplay()"/>
			<span id="rainIntVal">5 (≈0.32)</span>
		</label>
		<label>Duration s<input id="rainDuration" size=6 value="30" /></label>
		<label>Iterations<input id="rainIterations" size=4 value="1" /></label>
		<label>Delay s<input id="rainDelay" size=6 value="5" /></label>
		<button onclick="startRain()">Start</button>
		<button onclick="stopRain()">Stop</button>
	</div>
	<div class="statusline" id="rainStatus"></div>
	<details id="rainImp"><summary>Impacted Fields</summary>
		<div class="kv" id="rainFields"></div>
	</details>
</section>
<section>
	<h2>Snow Accumulation</h2>
	<div class="flex-row">
		<label>Intensity (0-10)<input type="range" min=0 max=10 step=0.1 id="snowIntensity" value="6" oninput="snowIntVal.textContent=this.value"/><span id="snowIntVal">6</span></label>
		<label>Duration s<input id="snowDuration" size=6 value="60" /></label>
		<label>Iterations<input id="snowIterations" size=4 value="1" /></label>
		<label>Delay s<input id="snowDelay" size=6 value="10" /></label>
		<button onclick="startSnow()">Start</button>
		<button onclick="stopSnow()">Stop</button>
	</div>
	<div class="statusline" id="snowStatus"></div>
	<details id="snowImp"><summary>Impacted Fields</summary>
		<div class="kv" id="snowFields"></div>
	</details>
</section>
<script>
const ALARMS=[ 'motors_stuck','thermal_throttle','thermal_shutdown','mast_not_near_vertical','unexpected_location','slow_ethernet_speeds','roaming','install_pending','is_heating','power_supply_thermal_throttle','is_power_save_idle','moving_while_not_mobile','moving_too_fast_for_policy','dbf_telem_stale','low_motor_current','lower_signal_than_predicted'];
const FIELDS=[ {group:'Throughput',items:[ {key:'dish.downlink_throughput_bps',label:'Downlink (bps)'}, {key:'dish.uplink_throughput_bps',label:'Uplink (bps)'}]}, {group:'Latency / Loss',items:[ {key:'dish.pop_ping_latency_ms',label:'POP Ping Latency (ms)'}, {key:'dish.pop_ping_drop_rate',label:'POP Ping Drop Rate'}]}, {group:'Radio Geometry',items:[ {key:'dish.boresight_azimuth_deg',label:'Boresight Azimuth (deg)'}, {key:'dish.boresight_elevation_deg',label:'Boresight Elevation (deg)'}]}, {group:'Environment',items:[ {key:'dish.obstruction_fraction',label:'Obstruction Fraction'}]}, {group:'Software Update',items:[ {key:'dish.software_update_progress_pct',label:'Update Progress (%)'}]}, {group:'Device',items:[ {key:'dish.eth_speed_mbps',label:'Ethernet Speed (Mbps)'}, {key:'dish.uptime_s',label:'Uptime (s)'}]},];
let refreshTimer=null;
function scheduleRefresh(){ if(refreshTimer){clearTimeout(refreshTimer);refreshTimer=null;} if(!document.getElementById('autoToggle').checked) return; let sec=parseFloat(document.getElementById('refreshInterval').value); if(isNaN(sec)||sec<=0) sec=60; if(sec<0.25) sec=0.25; refreshTimer=setTimeout(()=>{refresh();}, sec*1000); }
function init(){ const fs=document.getElementById('fieldSelect'); FIELDS.forEach(g=>{let og=document.createElement('optgroup');og.label=g.group;g.items.forEach(f=>{let o=document.createElement('option');o.value=f.key;o.textContent=f.label;og.appendChild(o)});fs.appendChild(og)}); const ab=document.getElementById('alarmButtons'); ALARMS.forEach(a=>{let b=document.getElementById('alarm-'+a);if(!b){b=document.createElement('button');b.className='toggle';b.id='alarm-'+a;b.textContent=a;b.onclick=()=>toggleAlarm(a);ab.appendChild(b)}});
 // Build obstruction axis labels (X top 0-7, Y left bottom-origin 0 at bottom shown ascending upward)
 const gx=document.getElementById('gridXLabels'); if(gx){gx.innerHTML=''; for(let x=0;x<8;x++){let s=document.createElement('span');s.textContent=x;gx.appendChild(s);} }
 const gy=document.getElementById('gridYLabels'); if(gy){gy.innerHTML=''; // bottom-origin: show 0 at bottom outside grid
 for(let y=7;y>=0;y--){let s=document.createElement('span');s.textContent=(7-y);gy.appendChild(s);} }
 // Weather axis labels
 const wx=document.getElementById('weatherXLabels'); if(wx){wx.innerHTML=''; for(let x=0;x<8;x++){let s=document.createElement('div');s.style.width='20px';s.style.textAlign='center';s.style.fontSize='10px';s.style.color='#ccc';s.textContent=x;wx.appendChild(s);} }
 const wy=document.getElementById('weatherYLabels'); if(wy){wy.innerHTML=''; for(let y=0;y<8;y++){let s=document.createElement('div');s.style.height='20px';s.style.display='flex';s.style.alignItems='center';s.style.justifyContent='center';s.style.fontSize='10px';s.style.color='#ccc';s.style.width='20px';s.textContent=y;wy.appendChild(s);} }
 document.getElementById('manualRefresh').onclick=()=>refresh(true); document.getElementById('autoToggle').onchange=()=>scheduleRefresh(); document.getElementById('refreshInterval').onchange=()=>scheduleRefresh(); updateRainDisplay(); refresh(true); }
async function refresh(manual){let s=await fetch('/api/alarms').then(r=>r.json()); window.__snapshotDishAlerts = s.dish_alerts || {}; renderAlarms(s.alarms||{}); let merged={};Object.assign(merged,s.fields||{});Object.assign(merged,s.raw_fields||{}); document.getElementById('fields').textContent=JSON.stringify(merged);
 // Prefer explicit manual obstruction grid if present (length 64). Otherwise use weather/effective grid.
 let gridRef=[]; if(s.obstruction && s.obstruction.length===64){ gridRef=s.obstruction; } else if(s.effective_grid && s.effective_grid.length===64){ gridRef=s.effective_grid; } else if(s.weather_grid && s.weather_grid.length===64){ gridRef=s.weather_grid; }
 renderGrid(gridRef);
 if(s.rain){document.getElementById('rainStatus').textContent='Rain: '+(s.rain.active?'ACTIVE':'idle')+' iter '+s.rain.iter+'/'+(s.rain.iterations||'∞')+' intensity '+(s.rain.intensity).toFixed(3);} if(s.snow){document.getElementById('snowStatus').textContent='Snow: '+(s.snow.active?'ACTIVE':'idle')+' iter '+s.snow.iter+'/'+(s.snow.iterations||'∞')+' intensity '+(s.snow.intensity*10).toFixed(1);} if(s.last_dish){updateImpacts(s.last_dish);} fetchWeather(); if(!manual){} scheduleRefresh(); }
function updateImpacts(d){let fields=[ ['Downlink (bps)',d.downlink_bps], ['Uplink (bps)',d.uplink_bps], ['POP Latency (ms)',d.pop_ping_latency_ms], ['POP Drop Rate',d.pop_ping_drop_rate], ['Obstruction Frac',d.obstruction_fraction], ['GPS Sats',d.gps_sats], ['GPS Valid',d.gps_valid] ];let rainF=document.getElementById('rainFields');let snowF=document.getElementById('snowFields');rainF.innerHTML='';snowF.innerHTML='';fields.forEach(f=>{let l=document.createElement('div');l.className='label';l.textContent=f[0];let v=document.createElement('div');v.textContent=f[1];rainF.appendChild(l.cloneNode(true));rainF.appendChild(v.cloneNode(true));snowF.appendChild(l);snowF.appendChild(v);});}
function renderAlarms(overrides){const dyn=window.__snapshotDishAlerts||{};const merged={...dyn,...overrides};ALARMS.forEach(a=>{let b=document.getElementById('alarm-'+a);if(!b)return; if(merged[a]) b.classList.add('active'); else b.classList.remove('active');});}
function toggleAlarm(name){const btn=document.getElementById('alarm-'+name);const willEnable=!btn.classList.contains('active');fetch('/api/alarms',{method:'POST',body:JSON.stringify({name:name,value:willEnable})});setTimeout(refresh,200)}
function clearAlarms(){fetch('/api/alarms',{method:'POST',body:JSON.stringify({name:'__clear_all__'})});setTimeout(refresh,200)}
function setField(){let key=document.getElementById('fieldSelect').value;let v=parseFloat(document.getElementById('fieldVal').value);if(isNaN(v))return;fetch('/api/fields',{method:'POST',body:JSON.stringify({name:key,value:v})});setTimeout(refresh,200)}
function clearField(){let key=document.getElementById('fieldSelect').value;fetch('/api/fields',{method:'POST',body:JSON.stringify({name:key})});setTimeout(refresh,200)}
function setCustomField(){let key=document.getElementById('customFieldName').value.trim();let raw=document.getElementById('customFieldVal').value.trim();if(!key||!raw)return;let num=parseFloat(raw);if(!isNaN(num)&&raw.match(/^[-+]?\d+(\.\d+)?$/)){fetch('/api/fields',{method:'POST',body:JSON.stringify({name:key,value:num})});}else{fetch('/api/fields',{method:'POST',body:JSON.stringify({name:key,raw:raw})});}setTimeout(refresh,200)}
function clearCustomField(){let key=document.getElementById('customFieldName').value.trim();if(!key)return;fetch('/api/fields',{method:'POST',body:JSON.stringify({name:key})});setTimeout(refresh,200)}
function injectError(){let c=parseInt(document.getElementById('errCode').value)||14;let m=document.getElementById('errMsg').value||'INJECTED_ERROR';fetch('/api/error',{method:'POST',body:JSON.stringify({enable:true,code:c,msg:m})});}
function disableError(){fetch('/api/error',{method:'POST',body:JSON.stringify({enable:false,code:0,msg:''})});}
function logMap(v){ // v slider 0-10 -> intensity 0-1 (log scale, higher slider -> higher intensity)
	// Use formula: intensity = 10^{(v-10)/10}. v=0 -> 10^-1=0.1, v=10 ->1.0. Reserve near 0 for very low.
	return Math.pow(10,(v-10)/10);
}
function updateRainDisplay(){let v=parseFloat(rainIntensity.value)||0;let mapped=logMap(v);document.getElementById('rainIntVal').textContent=v.toFixed(1)+' (≈'+mapped.toFixed(2)+')';}
function startRain(){let raw=parseFloat(rainIntensity.value)||5;let I=logMap(raw);let D=parseFloat(rainDuration.value)||30;let N=parseInt(rainIterations.value)||1;let L=parseFloat(rainDelay.value)||0;fetch('/api/rainfade',{method:'POST',body:JSON.stringify({action:'start',intensity:I,duration_s:D,iterations:N,delay_s:L})});setTimeout(refresh,500)}
function stopRain(){fetch('/api/rainfade',{method:'POST',body:JSON.stringify({action:'stop'})});setTimeout(refresh,500)}
function startSnow(){let I=parseFloat(snowIntensity.value)||6;I=I/10;let D=parseFloat(snowDuration.value)||60;let N=parseInt(snowIterations.value)||1;let L=parseFloat(snowDelay.value)||0;fetch('/api/snow',{method:'POST',body:JSON.stringify({action:'start',intensity:I,duration_s:D,iterations:N,delay_s:L})});setTimeout(refresh,500)}
function stopSnow(){fetch('/api/snow',{method:'POST',body:JSON.stringify({action:'stop'})});setTimeout(refresh,500)}
function renderGrid(arr){let g=document.getElementById('grid'); if(!g) return; g.innerHTML='';for(let i=0;i<64;i++){let v=arr[i];let d=document.createElement('div');if(v===0)d.classList.add('hole');d.onclick=()=>{let x=i%8,y=Math.floor(i/8);let uiY=7 - y;let isHole=v===0;let newVal=isHole?1:0;fetch('/api/obstruction',{method:'POST',body:JSON.stringify({x:x,y:uiY,value:newVal})}).then(()=>setTimeout(refresh,150));};g.appendChild(d)}}
function randomize(){fetch('/api/obstruction',{method:'POST',body:JSON.stringify({randomize:true})}).then(r=>r.json()).then(s=>{ if(s && s.obstruction){ renderGrid(s.obstruction);} setTimeout(refresh,300); })}
async function fetchWeather(){let w=await fetch('/api/weather').then(r=>r.json()); if(w.rain&&w.rain.path){pathSX.value=w.rain.path.start_x;pathSY.value=w.rain.path.start_y;pathEX.value=w.rain.path.end_x;pathEY.value=w.rain.path.end_y;} if(w.rain&&w.rain.extra_cells){renderExtraCells(w.rain.extra_cells);} let grid = (w.effective_grid && w.effective_grid.length===64)?w.effective_grid:(w.weather_grid||[]); let manual = w.obstruction||[]; drawWeather(grid, manual, w.rain); }
function drawWeather(grid, manual, rainState){
 let c=document.getElementById('weatherCanvas'); if(!c)return; let ctx=c.getContext('2d'); let sz=20; ctx.clearRect(0,0,c.width,c.height);
 for(let y=0;y<8;y++){
	for(let x=0;x<8;x++){
		let idx=y*8+x; let v=grid[idx]; let m = (manual&&manual.length===64)?manual[idx]:1; // manual hole makes it obstructed regardless of dynamic
		let obstructed = (v===0)||(m===0);
		ctx.fillStyle = obstructed ? '#000' : '#2e7d32';
		ctx.fillRect(x*sz,y*sz,sz,sz);
		// highlight manual override hole (outline) if dynamic also obstructed? Add border accent
		if(m===0 && v!==0){ ctx.strokeStyle='#ffcc00'; ctx.lineWidth=2; ctx.strokeRect(x*sz+2,y*sz+2,sz-4,sz-4); }
	}
 }
 ctx.strokeStyle='#333'; ctx.lineWidth=1; for(let i=0;i<=8;i++){ ctx.beginPath();ctx.moveTo(0,i*sz);ctx.lineTo(8*sz,i*sz);ctx.stroke(); ctx.beginPath();ctx.moveTo(i*sz,0);ctx.lineTo(i*sz,8*sz);ctx.stroke(); }
 // Click interaction toggle manual
 c.onclick=(ev)=>{ let rect=c.getBoundingClientRect(); let x=Math.floor((ev.clientX-rect.left)/sz); let y=Math.floor((ev.clientY-rect.top)/sz); if(x<0||y<0||x>7||y>7)return; let uiY=7 - y; let idx=y*8+x; let manualHas = (manual && manual.length===64); let currentManual = manualHas?manual[idx]:1; // manual value 0 means obstructed override
 let newVal = currentManual===0 ? 1 : 0; fetch('/api/obstruction',{method:'POST',body:JSON.stringify({x:x,y:uiY,value:newVal})}).then(()=>setTimeout(refresh,150)); };
}
function updatePath(){
	let sx=parseInt(pathSX.value)||0;let sy=parseInt(pathSY.value)||0;let ex=parseInt(pathEX.value)||7;let ey=parseInt(pathEY.value)||7;
	// UI uses bottom-left origin; backend expects top-left (assumed). Convert Y.
	let tsY = 7 - sy; let teY = 7 - ey;
	fetch('/api/weather',{method:'POST',body:JSON.stringify({rain_path:{start_x:sx,start_y:tsY,end_x:ex,end_y:teY}})});
	setTimeout(refresh,400)
}
let currentCells=[];function addCell(){let x=parseInt(cellX.value),y=parseInt(cellY.value);if(isNaN(x)||isNaN(y))return;let convY=7 - y;currentCells.push({X:x,Y:convY});saveCells();cellX.value='';cellY.value='';}
function renderExtraCells(cells){currentCells=cells.slice();let div=document.getElementById('extraCells');div.innerHTML='';cells.forEach((c,i)=>{let displayY=7 - c.Y;let span=document.createElement('div');span.textContent='('+c.X+','+displayY+') ';let rm=document.createElement('button');rm.textContent='x';rm.style.fontSize='10px';rm.onclick=()=>{currentCells.splice(i,1);saveCells();};span.appendChild(rm);div.appendChild(span);});}
function saveCells(){fetch('/api/weather',{method:'POST',body:JSON.stringify({extra_rain_cells:currentCells})});setTimeout(refresh,300)}
function clearManual(){fetch('/api/weather',{method:'POST',body:JSON.stringify({clear_manual:true})});setTimeout(refresh,300)}
init();
// Heartbeat / keep-alive monitor
let hbEl=document.createElement('div');hbEl.className='hb-status';hbEl.textContent='connecting…';document.body.appendChild(hbEl);
let lastOK=Date.now();let hbFailures=0;let hbInterval=4000;async function heartbeat(){
	try{let r=await fetch('/api/health',{cache:'no-store'}); if(!r.ok) throw new Error('bad status'); let j=await r.json(); lastOK=Date.now(); hbFailures=0; hbEl.textContent='healthy '+new Date(j.ts*1000).toLocaleTimeString(); hbEl.className='hb-status'; }
	catch(e){ hbFailures++; let ago=(Date.now()-lastOK)/1000; hbEl.textContent='unresponsive ('+ago.toFixed(0)+'s)'; hbEl.className='hb-status '+(ago>30?'bad':(ago>10?'warn':'')); }
	// exponential-ish backoff after multiple failures
	let next = hbFailures===0?4000: Math.min(15000, 4000 * (1+hbFailures));
	setTimeout(heartbeat,next);
}
heartbeat();
 </script></body></html>`

// applyReflectOverrideNumeric tries to set a numeric path (dot-separated using json tag names)
func applyReflectOverrideNumeric(d *dev.DishGetStatusResponse, path string, val float64) bool {
	if d == nil || path == "" {
		return false
	}
	path = strings.TrimPrefix(path, "dish.")
	segs := strings.Split(path, ".")
	rv := reflect.ValueOf(d).Elem()
	for i, s := range segs {
		if !rv.IsValid() {
			return false
		}
		if rv.Kind() == reflect.Struct {
			rt := rv.Type()
			found := false
			for f := 0; f < rt.NumField(); f++ {
				sf := rt.Field(f)
				if !sf.IsExported() {
					continue
				}
				tag := sf.Tag.Get("json")
				name := tag
				if idx := strings.Index(name, ","); idx >= 0 {
					name = name[:idx]
				}
				if name == s || strings.EqualFold(name, s) {
					fv := rv.Field(f)
					if fv.Kind() == reflect.Ptr {
						if fv.IsNil() {
							fv.Set(reflect.New(fv.Type().Elem()))
						}
						fv = fv.Elem()
					}
					if i == len(segs)-1 {
						return setNumericValue(fv, val)
					}
					rv = fv
					found = true
					break
				}
			}
			if !found {
				return false
			}
			continue
		}
		return false
	}
	return false
}

// applyReflectOverrideRaw sets arbitrary field supporting string, bool, enum(int) or numeric conversion.
func applyReflectOverrideRaw(d *dev.DishGetStatusResponse, path string, raw string) bool {
	if d == nil || path == "" {
		return false
	}
	path = strings.TrimPrefix(path, "dish.")
	segs := strings.Split(path, ".")
	rv := reflect.ValueOf(d).Elem()
	for i, s := range segs {
		if !rv.IsValid() {
			return false
		}
		if rv.Kind() == reflect.Struct {
			rt := rv.Type()
			found := false
			for f := 0; f < rt.NumField(); f++ {
				sf := rt.Field(f)
				if !sf.IsExported() {
					continue
				}
				tag := sf.Tag.Get("json")
				name := tag
				if idx := strings.Index(name, ","); idx >= 0 {
					name = name[:idx]
				}
				if name == s || strings.EqualFold(name, s) {
					fv := rv.Field(f)
					if fv.Kind() == reflect.Ptr {
						if fv.IsNil() {
							fv.Set(reflect.New(fv.Type().Elem()))
						}
						fv = fv.Elem()
					}
					if i == len(segs)-1 {
						return setRawValue(fv, raw)
					}
					rv = fv
					found = true
					break
				}
			}
			if !found {
				return false
			}
			continue
		}
		return false
	}
	return false
}

func setNumericValue(v reflect.Value, val float64) bool {
	if !v.CanSet() {
		return false
	}
	switch v.Kind() {
	case reflect.Float32, reflect.Float64:
		v.SetFloat(val)
		return true
	case reflect.Int, reflect.Int32, reflect.Int64:
		v.SetInt(int64(val))
		return true
	case reflect.Uint, reflect.Uint32, reflect.Uint64:
		if val < 0 {
			val = 0
		}
		v.SetUint(uint64(val))
		return true
	case reflect.Bool:
		v.SetBool(val != 0)
		return true
	default:
		return false
	}
}

func setRawValue(v reflect.Value, raw string) bool {
	if !v.CanSet() {
		return false
	}
	switch v.Kind() {
	case reflect.String:
		v.SetString(raw)
		return true
	case reflect.Bool:
		low := strings.ToLower(raw)
		if low == "true" || low == "1" || low == "yes" {
			v.SetBool(true)
		} else {
			v.SetBool(false)
		}
		return true
	case reflect.Float32, reflect.Float64:
		if f, err := strconv.ParseFloat(raw, 64); err == nil {
			v.SetFloat(f)
			return true
		}
	case reflect.Int, reflect.Int32, reflect.Int64:
		if i, err := strconv.ParseInt(raw, 10, 64); err == nil {
			v.SetInt(i)
			return true
		}
		// enum attempt via name match (iterate constants not trivial here)
	case reflect.Uint, reflect.Uint32, reflect.Uint64:
		if u, err := strconv.ParseUint(raw, 10, 64); err == nil {
			v.SetUint(u)
			return true
		}
	}
	return false
}
