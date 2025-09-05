package admin

import (
	"encoding/json"
	"math/rand"
	"net/http"
	"strings"
	"sync"

	dev "github.com/b0ch3nski/go-starlink/model/api-protoc/device"
)

// State holds mutable simulation overrides controlled via the admin UI.
type State struct {
	mu sync.RWMutex

	// Alarm overrides map dish alert field name -> bool
	alarms map[string]bool

	// Field overrides applied after synthetic generation; key path -> float64
	fields map[string]float64

	// Error injection: if set, next matching request returns gRPC status code/message (handled in server layer)
	ErrorNext struct {
		Enable bool
		Code   int32
		Msg    string
	}

	// Obstruction grid override (8x8). nil => use generated.
	obstruction []float32
}

func NewState() *State { return &State{alarms: map[string]bool{}, fields: map[string]float64{}} }

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
		case "dish.boresight_elevation_deg":
			d.BoresightElevationDeg = float32(val)
		case "dish.software_update_progress_pct":
			if d.SoftwareUpdateStats == nil {
				d.SoftwareUpdateStats = &dev.SoftwareUpdateStats{}
			}
			d.SoftwareUpdateStats.SoftwareUpdateProgress = float32(val)
		case "dish.uptime_s":
			if d.DeviceState == nil {
				d.DeviceState = &dev.DeviceState{}
			}
			d.DeviceState.UptimeS = uint64(val)
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
func (s *State) ClearField(name string) { s.mu.Lock(); delete(s.fields, name); s.mu.Unlock() }

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
	s.obstruction[y*8+x] = 0
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
	return map[string]interface{}{
		"alarms":      s.alarms,
		"fields":      s.fields,
		"error_next":  s.ErrorNext,
		"obstruction": s.obstruction,
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
func (s *State) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/alarms", s.handleAlarms)
	mux.HandleFunc("/api/fields", s.handleFields)
	mux.HandleFunc("/api/error", s.handleError)
	mux.HandleFunc("/api/obstruction", s.handleObstruction)
	mux.HandleFunc("/", serveIndex)
	return mux
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
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		if body.Value == nil {
			s.ClearField(body.Name)
		} else {
			s.SetField(body.Name, *body.Value)
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
			Randomize bool `json:"randomize"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		if body.Randomize {
			s.RandomizeObstruction()
		} else if body.X != nil && body.Y != nil {
			s.SetObstructionHole(*body.X, *body.Y)
		}
		s.respondJSON(w, s.Snapshot())
	case http.MethodGet:
		s.respondJSON(w, s.Snapshot())
	default:
		w.WriteHeader(405)
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
body{font-family:system-ui;margin:1rem}section{margin-bottom:1.5rem}
button.toggle{margin:2px;padding:4px 8px;border:1px solid #999;background:#eee;cursor:pointer;border-radius:4px;font-size:12px}
button.toggle.active{background:#d32f2f;color:#fff;border-color:#b71c1c}
fieldset{border:1px solid #ccc;padding:8px;border-radius:6px}
#grid{display:grid;grid-template-columns:repeat(8,24px);gap:2px;margin-top:8px}
#grid div{width:24px;height:24px;background:#4caf50;cursor:pointer}
#grid div.hole{background:#222}
code{background:#f5f5f5;padding:2px 4px;border-radius:4px}
select,input{margin:2px}
</style></head><body><h1>Naquadah Admin</h1>
<section>
	<h2>Alarms</h2>
	<p>Click to toggle individual dish alert flags. <button onclick="clearAlarms()">Clear All</button></p>
	<div id="alarmButtons"></div>
</section>
<section>
	<h2>Field Overrides</h2>
	<p>Select a field and value to force; clear removes override.</p>
	<select id="fieldSelect"></select>
	<input id="fieldVal" placeholder="value" size=10 />
	<button onclick="setField()">Set</button>
	<button onclick="clearField()">Clear</button>
	<div style="margin-top:6px"><strong>Active:</strong> <span id="fields"></span></div>
</section>
<section>
	<h2>Error Injection (next request only)</h2>
	<input id="errCode" placeholder="code" size=5 />
	<input id="errMsg" placeholder="message" size=20 />
	<button onclick="injectError()">Inject</button>
	<button onclick="disableError()">Disable</button>
</section>
<section>
	<h2>Obstruction Map</h2>
	<div id="grid"></div>
	<button onclick="randomize()">Randomize</button>
</section>
<script>
const ALARMS=[
 'motors_stuck','thermal_throttle','thermal_shutdown','mast_not_near_vertical','unexpected_location',
 'slow_ethernet_speeds','roaming','install_pending','is_heating','power_supply_thermal_throttle',
 'is_power_save_idle','moving_while_not_mobile','moving_too_fast_for_policy','dbf_telem_stale','low_motor_current','lower_signal_than_predicted'];
const FIELDS=[
 {group:'Throughput',items:[
	 {key:'dish.downlink_throughput_bps',label:'Downlink (bps)'},
	 {key:'dish.uplink_throughput_bps',label:'Uplink (bps)'}]},
 {group:'Latency / Loss',items:[
	 {key:'dish.pop_ping_latency_ms',label:'POP Ping Latency (ms)'},
	 {key:'dish.pop_ping_drop_rate',label:'POP Ping Drop Rate'}]},
 {group:'Radio Geometry',items:[
	 {key:'dish.boresight_azimuth_deg',label:'Boresight Azimuth (deg)'},
	 {key:'dish.boresight_elevation_deg',label:'Boresight Elevation (deg)'}]},
 {group:'Environment',items:[
	 {key:'dish.obstruction_fraction',label:'Obstruction Fraction'}]},
 {group:'Software Update',items:[
	 {key:'dish.software_update_progress_pct',label:'Update Progress (%)'}]},
 {group:'Device',items:[
	 {key:'dish.eth_speed_mbps',label:'Ethernet Speed (Mbps)'},
	 {key:'dish.uptime_s',label:'Uptime (s)'}]},
];
function init(){
	const fs=document.getElementById('fieldSelect');
		FIELDS.forEach(g=>{let og=document.createElement('optgroup');og.label=g.group;g.items.forEach(f=>{let o=document.createElement('option');o.value=f.key;o.textContent=f.label;og.appendChild(o)});fs.appendChild(og)});
	const ab=document.getElementById('alarmButtons');
	ALARMS.forEach(a=>{let b=document.createElement('button');b.className='toggle';b.id='alarm-'+a;b.textContent=a;b.onclick=()=>toggleAlarm(a);ab.appendChild(b)});
	refresh();
	setInterval(refresh,1500);
}
async function refresh(){let s=await fetch('/api/alarms').then(r=>r.json());
	renderAlarms(s.alarms||{});
	document.getElementById('fields').textContent=JSON.stringify(s.fields||{});
	renderGrid(s.obstruction||[]);
}
function renderAlarms(active){ALARMS.forEach(a=>{let b=document.getElementById('alarm-'+a);if(!b)return; if(active[a]) b.classList.add('active'); else b.classList.remove('active');});}
function toggleAlarm(name){const btn=document.getElementById('alarm-'+name);const willEnable=!btn.classList.contains('active');fetch('/api/alarms',{method:'POST',body:JSON.stringify({name:name,value:willEnable})});setTimeout(refresh,200)}
function clearAlarms(){fetch('/api/alarms',{method:'POST',body:JSON.stringify({name:'__clear_all__'})});setTimeout(refresh,200)}
function setField(){let key=document.getElementById('fieldSelect').value;let v=parseFloat(document.getElementById('fieldVal').value);if(isNaN(v))return;fetch('/api/fields',{method:'POST',body:JSON.stringify({name:key,value:v})});setTimeout(refresh,200)}
function clearField(){let key=document.getElementById('fieldSelect').value;fetch('/api/fields',{method:'POST',body:JSON.stringify({name:key})});setTimeout(refresh,200)}
function injectError(){let c=parseInt(document.getElementById('errCode').value)||14;let m=document.getElementById('errMsg').value||'INJECTED_ERROR';fetch('/api/error',{method:'POST',body:JSON.stringify({enable:true,code:c,msg:m})});}
function disableError(){fetch('/api/error',{method:'POST',body:JSON.stringify({enable:false,code:0,msg:''})});}
function renderGrid(arr){let g=document.getElementById('grid');g.innerHTML='';for(let i=0;i<64;i++){let v=arr[i];let d=document.createElement('div');if(v===0)d.classList.add('hole');d.onclick=()=>{let x=i%8,y=Math.floor(i/8);fetch('/api/obstruction',{method:'POST',body:JSON.stringify({x:x,y:y})});setTimeout(refresh,200)};g.appendChild(d)}}
function randomize(){fetch('/api/obstruction',{method:'POST',body:JSON.stringify({randomize:true})});setTimeout(refresh,200)}
init();
</script></body></html>`
