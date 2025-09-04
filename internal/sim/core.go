package sim

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	gmath "math/rand"
	"strings"
	"sync"
	"time"

	dev "github.com/b0ch3nski/go-starlink/model/api-protoc/device"
	unlock "github.com/b0ch3nski/go-starlink/model/api-protoc/device/services/unlock"
	statuspb "github.com/b0ch3nski/go-starlink/model/api-protoc/status"
)

type CoreOptions struct {
	Noisy      bool
	EmitEvents bool
}

// Core is the simulation core; it owns state and pluggable handlers.
type Core struct {
	mu       sync.RWMutex
	opts     CoreOptions
	profile  Profile
	provider DataProvider
	// hooks allows per-request overrides for testing faults, etc.
	hooks map[string]Handler
}

// Handler builds a Response for a given Request.
type Handler func(ctx context.Context, c *Core, req *dev.Request) (*dev.Response, error)

func NewCore(opts CoreOptions) *Core {
	c := &Core{
		opts:     opts,
		profile:  DefaultProfile(),
		provider: nil,
		hooks:    map[string]Handler{},
	}
	return c
}

// NewCoreWithProfile allows constructing the simulator with a specific profile.
func NewCoreWithProfile(opts CoreOptions, p Profile) *Core {
	c := &Core{
		opts:     opts,
		profile:  p,
		provider: nil,
		hooks:    map[string]Handler{},
	}
	return c
}

// With sets a custom handler for a request case key (e.g., "get_status").
func (c *Core) With(key string, h Handler) { c.hooks[key] = h }

// SetDataProvider sets the active data provider (playback/baseline). Passing nil restores random synthesis.
func (c *Core) SetDataProvider(p DataProvider) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.provider = p
}

// getSample returns the next provider sample if a provider is set.
func (c *Core) getSample() *Sample {
	c.mu.RLock()
	p := c.provider
	c.mu.RUnlock()
	if p == nil {
		return nil
	}
	return p.Next(time.Now())
}

// HandleDeviceRequest routes Request.oneof to generator handlers and returns a Response with a default OK status and api_version.
func (c *Core) HandleDeviceRequest(ctx context.Context, req *dev.Request) (*dev.Response, error) {
	if c.opts.Noisy {
		// Keep logs minimal to avoid noise in tests.
		fmt.Printf("device.Handle: id=%d oneof=%T\n", req.GetId(), req.GetRequest())
	}
	resp := &dev.Response{
		Id:         req.GetId(),
		ApiVersion: 1,
		Status:     &statuspb.Status{Code: 0, Message: "OK"},
	}

	// Dispatch by concrete request type. Only set oneof field and return.
	switch r := req.GetRequest().(type) {
	case *dev.Request_GetNextId:
		resp.Response = &dev.Response_GetNextId{GetNextId: &dev.GetNextIdResponse{Id: req.GetId() + 1}}
	case *dev.Request_EnableDebugTelem:
		resp.Response = &dev.Response_EnableDebugTelem{EnableDebugTelem: &dev.EnableDebugTelemResponse{}}
	case *dev.Request_FactoryReset:
		resp.Response = &dev.Response_FactoryReset{FactoryReset: &dev.FactoryResetResponse{}}
	case *dev.Request_GetStatus:
		// Route to dish or wifi based on target_id (simple heuristic).
		if tid := strings.ToLower(req.GetTargetId()); strings.Contains(tid, "wifi") || strings.Contains(tid, "router") {
			if !c.profile.EnableRouter || !c.profile.EnableWifi {
				resp.Response = &dev.Response_WifiGetStatus{WifiGetStatus: &dev.WifiGetStatusResponse{}}
				break
			}
			sample := c.getSample()
			if sample != nil && sample.WifiStatus != nil {
				resp.Response = &dev.Response_WifiGetStatus{WifiGetStatus: sample.WifiStatus}
			} else {
				resp.Response = &dev.Response_WifiGetStatus{WifiGetStatus: c.randWifiStatus()}
			}
		} else {
			sample := c.getSample()
			if sample != nil && sample.DishStatus != nil {
				resp.Response = &dev.Response_DishGetStatus{DishGetStatus: sample.DishStatus}
			} else {
				resp.Response = &dev.Response_DishGetStatus{DishGetStatus: c.randDishStatus()}
			}
		}
	case *dev.Request_DishGetContext:
		resp.Response = &dev.Response_DishGetContext{DishGetContext: c.randDishContext()}
	case *dev.Request_DishGetConfig:
		resp.Response = &dev.Response_DishGetConfig{DishGetConfig: c.randDishConfig()}
	case *dev.Request_DishGetObstructionMap:
		resp.Response = &dev.Response_DishGetObstructionMap{DishGetObstructionMap: c.randDishObstructions()}
	case *dev.Request_DishGetData:
		resp.Response = &dev.Response_DishGetHistory{DishGetHistory: c.randDishHistory()}
	case *dev.Request_DishSetEmc:
		resp.Response = &dev.Response_DishSetEmc{DishSetEmc: &dev.DishSetEmcResponse{}}
	case *dev.Request_DishGetEmc:
		resp.Response = &dev.Response_DishGetEmc{DishGetEmc: &dev.DishGetEmcResponse{}}
	case *dev.Request_DishPowerSave:
		resp.Response = &dev.Response_DishInhibitGps{DishInhibitGps: &dev.DishInhibitGpsResponse{}}
	case *dev.Request_DishInhibitGps:
		resp.Response = &dev.Response_DishInhibitGps{DishInhibitGps: &dev.DishInhibitGpsResponse{}}
	case *dev.Request_DishClearObstructionMap:
		resp.Response = &dev.Response_DishClearObstructionMap{DishClearObstructionMap: &dev.DishClearObstructionMapResponse{}}
	case *dev.Request_StartDishSelfTest:
		resp.Response = &dev.Response_StartDishSelfTest{StartDishSelfTest: &dev.StartDishSelfTestResponse{}}
	case *dev.Request_WifiGetClients:
		if !c.profile.EnableRouter || !c.profile.EnableWifi {
			resp.Response = &dev.Response_WifiGetClients{WifiGetClients: &dev.WifiGetClientsResponse{}}
		} else if sample := c.getSample(); sample != nil && sample.WifiClients != nil {
			resp.Response = &dev.Response_WifiGetClients{WifiGetClients: sample.WifiClients}
		} else {
			resp.Response = &dev.Response_WifiGetClients{WifiGetClients: c.randWifiClients()}
		}
	case *dev.Request_WifiGetConfig:
		if !c.profile.EnableRouter || !c.profile.EnableWifi {
			resp.Response = &dev.Response_WifiGetConfig{WifiGetConfig: &dev.WifiGetConfigResponse{WifiConfig: &dev.WifiConfig{}}}
		} else {
			resp.Response = &dev.Response_WifiGetConfig{WifiGetConfig: c.randWifiConfig()}
		}
	case *dev.Request_WifiGetFirewall:
		if !c.profile.EnableRouter || !c.profile.EnableWifi {
			resp.Response = &dev.Response_WifiGetFirewall{WifiGetFirewall: &dev.WifiGetFirewallResponse{}}
		} else {
			resp.Response = &dev.Response_WifiGetFirewall{WifiGetFirewall: c.randWifiFirewall()}
		}
	case *dev.Request_WifiGetPingMetrics:
		if !c.profile.EnableRouter || !c.profile.EnableWifi {
			resp.Response = &dev.Response_WifiGetPingMetrics{WifiGetPingMetrics: &dev.WifiGetPingMetricsResponse{}}
		} else {
			resp.Response = &dev.Response_WifiGetPingMetrics{WifiGetPingMetrics: &dev.WifiGetPingMetricsResponse{}}
		}
	case *dev.Request_WifiSetup:
		resp.Response = &dev.Response_WifiSetup{WifiSetup: &dev.WifiSetupResponse{}}
	case *dev.Request_WifiSetMeshDeviceTrust:
		resp.Response = &dev.Response_WifiSetMeshDeviceTrust{WifiSetMeshDeviceTrust: &dev.WifiSetMeshDeviceTrustResponse{}}
	case *dev.Request_WifiSetMeshConfig:
		resp.Response = &dev.Response_WifiSetMeshConfig{WifiSetMeshConfig: &dev.WifiSetMeshConfigResponse{}}
	case *dev.Request_WifiGetClientHistory:
		resp.Response = &dev.Response_WifiGetClientHistory{WifiGetClientHistory: &dev.WifiGetClientHistoryResponse{}}
	case *dev.Request_WifiSetAviationConformed:
		resp.Response = &dev.Response_WifiSelfTest{WifiSelfTest: &dev.WifiSelfTestResponse{}}
	case *dev.Request_WifiSelfTest:
		resp.Response = &dev.Response_WifiSelfTest{WifiSelfTest: &dev.WifiSelfTestResponse{}}
	// Some request variants don't have dedicated response types in this schema; return OK status only.
	case *dev.Request_WifiGuestInfo:
		resp.Response = &dev.Response_WifiGuestInfo{WifiGuestInfo: &dev.WifiGuestInfoResponse{}}
	case *dev.Request_WifiRfTest:
		resp.Response = &dev.Response_WifiRfTest{WifiRfTest: &dev.WifiRfTestResponse{}}
	case *dev.Request_WifiFactoryTestCommand:
		resp.Response = &dev.Response_WifiFactoryTestCommand{WifiFactoryTestCommand: &dev.WifiFactoryTestCommandResponse{}}
	case *dev.Request_GetPing:
		resp.Response = &dev.Response_GetPing{GetPing: c.randPingAll()}
	case *dev.Request_PingHost:
		resp.Response = &dev.Response_PingHost{PingHost: c.randPingHost(r.PingHost.GetAddress())}
	case *dev.Request_Time:
		resp.Response = &dev.Response_Time{Time: &dev.GetTimeResponse{UnixNano: time.Now().UnixNano()}}
	case *dev.Request_Reboot:
		resp.Response = &dev.Response_Reboot{Reboot: &dev.RebootResponse{}}
	case *dev.Request_SpeedTest:
		resp.Response = &dev.Response_SpeedTest{SpeedTest: c.randSpeedtest()}
	case *dev.Request_StartSpeedtest:
		resp.Response = &dev.Response_StartSpeedtest{StartSpeedtest: &dev.StartSpeedtestResponse{}} // no-op
	case *dev.Request_GetSpeedtestStatus:
		resp.Response = &dev.Response_GetSpeedtestStatus{GetSpeedtestStatus: &dev.GetSpeedtestStatusResponse{Status: &dev.SpeedtestStatus{Running: gmath.Intn(2) == 0, Id: 1}}}
	case *dev.Request_GetDeviceInfo:
		resp.Response = &dev.Response_GetDeviceInfo{GetDeviceInfo: c.randDeviceInfo()}
	case *dev.Request_GetNetworkInterfaces:
		resp.Response = &dev.Response_GetNetworkInterfaces{GetNetworkInterfaces: c.randInterfaces()}
	case *dev.Request_DishStow:
		resp.Response = &dev.Response_DishStow{DishStow: &dev.DishStowResponse{}}
	case *dev.Request_DishSetConfig:
		resp.Response = &dev.Response_DishSetConfig{DishSetConfig: &dev.DishSetConfigResponse{}}
	case *dev.Request_WifiSetConfig:
		resp.Response = &dev.Response_WifiSetConfig{WifiSetConfig: &dev.WifiSetConfigResponse{}}
	case *dev.Request_GetDiagnostics:
		resp.Response = &dev.Response_DishGetDiagnostics{DishGetDiagnostics: c.randDishDiagnostics()}
	case *dev.Request_SetSku:
		resp.Response = &dev.Response_SetSku{SetSku: &dev.SetSkuResponse{}}
	case *dev.Request_SetTrustedKeys:
		resp.Response = &dev.Response_SetTrustedKeys{SetTrustedKeys: &dev.SetTrustedKeysResponse{}}
	case *dev.Request_Update:
		resp.Response = &dev.Response_Update{Update: &dev.UpdateResponse{}}
	case *dev.Request_GetLocation:
		resp.Response = &dev.Response_GetLocation{GetLocation: &dev.GetLocationResponse{Lla: &dev.LLAPosition{Lat: float64(c.profile.Lat), Lon: float64(c.profile.Lon), Alt: 0}}}
	case *dev.Request_GetHeapDump:
		resp.Response = &dev.Response_GetHeapDump{GetHeapDump: &dev.GetHeapDumpResponse{}}
	case *dev.Request_RestartControl:
		resp.Response = &dev.Response_RestartControl{RestartControl: &dev.RestartControlResponse{}}
	case *dev.Request_Fuse:
		resp.Response = &dev.Response_Fuse{Fuse: &dev.FuseResponse{}}
	case *dev.Request_GetPersistentStats:
		resp.Response = &dev.Response_WifiGetPersistentStats{WifiGetPersistentStats: &dev.WifiGetPersistentStatsResponse{}}
	case *dev.Request_GetConnections:
		resp.Response = &dev.Response_GetConnections{GetConnections: &dev.GetConnectionsResponse{}}
	case *dev.Request_ReportClientSpeedtest:
		resp.Response = &dev.Response_ReportClientSpeedtest{ReportClientSpeedtest: &dev.ReportClientSpeedtestResponse{}}
	case *dev.Request_InitiateRemoteSsh:
		resp.Response = &dev.Response_InitiateRemoteSsh{InitiateRemoteSsh: &dev.InitiateRemoteSshResponse{}}
	case *dev.Request_SelfTest:
		resp.Response = &dev.Response_SelfTest{SelfTest: &dev.SelfTestResponse{}}
	case *dev.Request_SetTestMode:
		resp.Response = &dev.Response_SetTestMode{SetTestMode: &dev.SetTestModeResponse{}}
	case *dev.Request_SoftwareUpdate:
		resp.Response = &dev.Response_SoftwareUpdate{SoftwareUpdate: &dev.SoftwareUpdateResponse{}}
	// case *dev.Request_IqCapture: // no explicit response type in this schema
	case *dev.Request_GetRadioStats:
		resp.Response = &dev.Response_GetRadioStats{GetRadioStats: &dev.GetRadioStatsResponse{}}
	case *dev.Request_RunIperfServer:
		resp.Response = &dev.Response_RunIperfServer{RunIperfServer: &dev.RunIperfServerResponse{}}
	case *dev.Request_TransceiverIfLoopbackTest:
		resp.Response = &dev.Response_TransceiverIfLoopbackTest{TransceiverIfLoopbackTest: &dev.TransceiverIFLoopbackTestResponse{}}
	case *dev.Request_TransceiverGetStatus:
		resp.Response = &dev.Response_TransceiverGetStatus{TransceiverGetStatus: &dev.TransceiverGetStatusResponse{}}
	case *dev.Request_TransceiverGetTelemetry:
		resp.Response = &dev.Response_TransceiverGetTelemetry{TransceiverGetTelemetry: &dev.TransceiverGetTelemetryResponse{}}
	case *dev.Request_StartUnlock:
		if out, _ := c.StartUnlock(ctx, r.StartUnlock); out != nil {
			resp.Response = &dev.Response_StartUnlock{StartUnlock: out}
		}
	case *dev.Request_FinishUnlock:
		if out, _ := c.FinishUnlock(ctx, r.FinishUnlock); out != nil {
			resp.Response = &dev.Response_FinishUnlock{FinishUnlock: out}
		}
	default:
		// For unhandled methods, return a generic OK with no payload.
		// This keeps the simulator permissive while still implementing the APIs.
	}
	// Defensive: ensure a concrete oneof so clients not checking for nil payloads don't panic.
	if resp.Response == nil {
		resp.Response = &dev.Response_GetDeviceInfo{GetDeviceInfo: c.randDeviceInfo()}
	}
	return resp, nil
}

// DeviceStream echoes heartbeats and can emit random events.
func (c *Core) DeviceStream(stream dev.Device_StreamServer) error {
	ctx := stream.Context()
	// Kick off event ticker if enabled.
	var ticker *time.Ticker
	if c.opts.EmitEvents {
		ticker = time.NewTicker(3 * time.Second)
		defer ticker.Stop()
	}
	for {
		// Non-blocking event send
		if ticker != nil {
			select {
			case <-ticker.C:
				_ = stream.Send(&dev.FromDevice{Message: &dev.FromDevice_Event{Event: c.randEvent()}})
			default:
			}
		}
		// Handle client messages
		if err := stream.Context().Err(); err != nil {
			return err
		}
		// Use Recv with short deadline
		_ = stream.Send(&dev.FromDevice{Message: &dev.FromDevice_HealthCheck{HealthCheck: &dev.HealthCheck{}}})
		// Read any incoming message (request or health check)
		stream.SetTrailer(nil)
		_ = ctx
		to, err := stream.Recv()
		if err != nil {
			// Stream closed by client
			return nil
		}
		switch m := to.GetMessage().(type) {
		case *dev.ToDevice_Request:
			resp, _ := c.HandleDeviceRequest(ctx, m.Request)
			_ = stream.Send(&dev.FromDevice{Message: &dev.FromDevice_Response{Response: resp}})
		case *dev.ToDevice_HealthCheck:
			_ = stream.Send(&dev.FromDevice{Message: &dev.FromDevice_HealthCheck{HealthCheck: &dev.HealthCheck{}}})
		default:
		}
	}
}

// MeshStream: echo simple status and allow clients to send updates.
func (c *Core) MeshStream(stream dev.Mesh_MeshStreamServer) error {
	// Periodically send a mesh status snapshot.
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			_ = stream.Send(&dev.FromController{Message: &dev.FromController_Status{Status: c.randMeshStatus()}})
		default:
		}
		to, err := stream.Recv()
		if err != nil {
			return nil
		}
		_ = to // ignore for now
	}
}

// Unlock service methods
func (c *Core) StartUnlock(ctx context.Context, r *unlock.StartUnlockRequest) (*unlock.StartUnlockResponse, error) {
	nonce := make([]byte, 16)
	_, _ = rand.Read(nonce)
	spki := make([]byte, 32)
	_, _ = rand.Read(spki)
	return &unlock.StartUnlockResponse{
		DeviceId: c.profile.DeviceID,
		Nonce:    nonce,
		SignSpki: spki,
	}, nil
}

func (c *Core) FinishUnlock(ctx context.Context, r *unlock.FinishUnlockRequest) (*unlock.FinishUnlockResponse, error) {
	// Always succeed in simulator
	return &unlock.FinishUnlockResponse{Status: 0}, nil
}

// Random data builders below. Only a subset is needed for a believable dataset.

func (c *Core) randDishStatus() *dev.DishGetStatusResponse {
	// Expanded synthetic status approximating real field richness.
	uptime := uint64(3600 + gmath.Intn(24*3600))
	alerts := &dev.DishAlerts{
		MotorsStuck:                false,
		ThermalThrottle:            gmath.Intn(20) == 0,
		ThermalShutdown:            false,
		MastNotNearVertical:        false,
		UnexpectedLocation:         false,
		SlowEthernetSpeeds:         gmath.Intn(50) == 0,
		Roaming:                    false,
		InstallPending:             gmath.Intn(40) == 0,
		IsHeating:                  gmath.Intn(30) == 0,
		PowerSupplyThermalThrottle: gmath.Intn(25) == 0,
		IsPowerSaveIdle:            gmath.Intn(15) == 0,
		MovingWhileNotMobile:       false,
		MovingTooFastForPolicy:     false,
		DbfTelemStale:              gmath.Intn(100) == 0,
		LowMotorCurrent:            false,
		LowerSignalThanPredicted:   gmath.Intn(60) == 0,
	}
	gps := &dev.DishGpsStats{GpsValid: true, GpsSats: uint32(30 + gmath.Intn(10))}
	ob := &dev.DishObstructionStats{
		FractionObstructed:               gmath.Float32() * 0.05,
		TimeObstructed:                   gmath.Float32() * 100,
		ValidS:                           3600,
		PatchesValid:                     64,
		AvgProlongedObstructionIntervalS: gmath.Float32()*300 + 100,
		AvgProlongedObstructionDurationS: gmath.Float32()*30 + 5,
	}
	ready := &dev.DishReadyStates{Cady: true, Scp: true, L1L2: true, Xphy: true, Aap: true, Rf: true}
	align := &dev.AlignmentStats{
		TiltAngleDeg:                 gmath.Float32()*5 - 2.5,
		BoresightAzimuthDeg:          gmath.Float32() * 360,
		BoresightElevationDeg:        20 + gmath.Float32()*50,
		DesiredBoresightAzimuthDeg:   gmath.Float32() * 360,
		DesiredBoresightElevationDeg: 20 + gmath.Float32()*50,
		AttitudeEstimationState:      2,
		AttitudeUncertaintyDeg:       gmath.Float32()*2 + 0.2,
	}
	updStats := &dev.SoftwareUpdateStats{SoftwareUpdateState: dev.SoftwareUpdateState_IDLE, SoftwareUpdateProgress: float32(gmath.Intn(100))}
	initDur := &dev.InitializationDurationSeconds{
		AttitudeInitialization: int32(30 + gmath.Intn(40)),
		BurstDetected:          int32(5 + gmath.Intn(10)),
		EkfConverged:           int32(20 + gmath.Intn(30)),
		FirstCplane:            int32(15 + gmath.Intn(20)),
		FirstPopPing:           int32(25 + gmath.Intn(15)),
		GpsValid:               int32(10 + gmath.Intn(20)),
		InitialNetworkEntry:    int32(60 + gmath.Intn(60)),
		NetworkSchedule:        int32(20 + gmath.Intn(40)),
		RfReady:                int32(40 + gmath.Intn(30)),
		StableConnection:       int32(120 + gmath.Intn(120)),
	}
	config := &dev.DishConfig{ApplySnowMeltMode: gmath.Intn(10) == 0}
	return &dev.DishGetStatusResponse{
		DeviceInfo:                    c.randDeviceInfo().GetDeviceInfo(),
		DeviceState:                   &dev.DeviceState{UptimeS: uptime},
		Alerts:                        alerts,
		GpsStats:                      gps,
		ObstructionStats:              ob,
		PopPingDropRate:               gmath.Float32() * 0.02,
		PopPingLatencyMs:              20 + gmath.Float32()*40,
		DownlinkThroughputBps:         50_000_000 + gmath.Float32()*100_000_000,
		UplinkThroughputBps:           5_000_000 + gmath.Float32()*25_000_000,
		BoresightAzimuthDeg:           align.BoresightAzimuthDeg,
		BoresightElevationDeg:         align.BoresightElevationDeg,
		EthSpeedMbps:                  1000,
		MobilityClass:                 dev.UserMobilityClass_STATIONARY,
		IsSnrAboveNoiseFloor:          true,
		ReadyStates:                   ready,
		ClassOfService:                dev.UserClassOfService_BUSINESS,
		SoftwareUpdateState:           dev.SoftwareUpdateState_IDLE,
		SoftwareUpdateStats:           updStats,
		AlignmentStats:                align,
		DisablementCode:               0,
		HasSignedCals:                 true,
		Config:                        config,
		InitializationDurationSeconds: initDur,
	}
}

func (c *Core) randDishContext() *dev.DishGetContextResponse {
	return &dev.DishGetContextResponse{
		DeviceInfo: c.randDeviceInfo().GetDeviceInfo(),
	}
}

func (c *Core) randDishConfig() *dev.DishGetConfigResponse {
	return &dev.DishGetConfigResponse{DishConfig: &dev.DishConfig{}}
}

func (c *Core) randDishObstructions() *dev.DishGetObstructionMapResponse {
	// Provide a tiny synthetic map
	rows, cols := 8, 8
	snr := make([]float32, rows*cols)
	for i := range snr {
		snr[i] = 5 + gmath.Float32()*20
	}
	return &dev.DishGetObstructionMapResponse{
		NumRows:         uint32(rows),
		NumCols:         uint32(cols),
		Snr:             snr,
		MinElevationDeg: 5,
		MaxThetaDeg:     90,
	}
}

func (c *Core) randDishHistory() *dev.DishGetHistoryResponse {
	// Build a short history window with a few samples
	n := 10
	mk := func(base float32) []float32 {
		a := make([]float32, n)
		for i := 0; i < n; i++ {
			a[i] = base * (0.8 + gmath.Float32()*0.4)
		}
		return a
	}
	return &dev.DishGetHistoryResponse{
		Current:               uint64(time.Now().Unix()),
		PopPingDropRate:       mk(0.01),
		PopPingLatencyMs:      mk(35),
		DownlinkThroughputBps: mk(100_000_000),
		UplinkThroughputBps:   mk(15_000_000),
		Outages:               []*dev.DishOutage{},
	}
}

func (c *Core) randWifiStatus() *dev.WifiGetStatusResponse {
	return &dev.WifiGetStatusResponse{
		DeviceInfo:       &dev.DeviceInfo{Id: c.profile.DeviceID + "-router", HardwareVersion: c.profile.RouterHW, SoftwareVersion: c.profile.RouterSW, CountryCode: c.profile.Country},
		DeviceState:      &dev.DeviceState{UptimeS: uint64(1000 + gmath.Intn(5000))},
		PopPingLatencyMs: 20 + gmath.Float32()*30,
		PopPingDropRate:  gmath.Float32() * 0.02,
		Ipv4WanAddress:   "100.64.0.2",
		DishId:           c.profile.DeviceID,
		UtcNs:            time.Now().UnixNano(),
	}
}

func (c *Core) randWifiClients() *dev.WifiGetClientsResponse {
	n := 1 + gmath.Intn(4)
	clients := make([]*dev.WifiClient, 0, n)
	for i := 0; i < n; i++ {
		clients = append(clients, &dev.WifiClient{
			MacAddress:     fmt.Sprintf("%s", c.randMAC()),
			GivenName:      fmt.Sprintf("client-%d", i+1),
			IpAddress:      fmt.Sprintf("192.168.1.%d", 100+i),
			SignalStrength: -30 - gmath.Float32()*40,
		})
	}
	return &dev.WifiGetClientsResponse{Clients: clients}
}

func (c *Core) randWifiConfig() *dev.WifiGetConfigResponse {
	// Build minimal wifi config with a single BSS
	wc := &dev.WifiConfig{
		CountryCode: c.profile.Country,
		Networks: []*dev.WifiConfig_Network{
			{
				BasicServiceSets: []*dev.WifiConfig_BasicServiceSet{
					{
						Bssid: "02:00:00:00:00:01",
						Ssid:  c.profile.SSID,
					},
				},
			},
		},
	}
	return &dev.WifiGetConfigResponse{WifiConfig: wc}
}

func (c *Core) randWifiFirewall() *dev.WifiGetFirewallResponse {
	// Provide placeholder iptables rules
	return &dev.WifiGetFirewallResponse{Iptables: "*filter\nCOMMIT\n", Iptables_6: "*filter\nCOMMIT\n"}
}

func (c *Core) randPingAll() *dev.GetPingResponse {
	m := map[string]*dev.PingResult{
		"cloud":  c.randPing(),
		"router": c.randPing(),
		"space":  c.randPing(),
	}
	return &dev.GetPingResponse{Results: m}
}

func (c *Core) randPingHost(host string) *dev.PingHostResponse {
	return &dev.PingHostResponse{Result: c.randPing()}
}

func (c *Core) randPing() *dev.PingResult {
	return &dev.PingResult{
		DropRate:  gmath.Float32() * 0.02,
		LatencyMs: 20 + gmath.Float32()*60,
	}
}

func (c *Core) randSpeedtest() *dev.SpeedTestResponse {
	mk := func(streams uint32) (down, up float32) {
		baseDown := 80 + gmath.Float32()*120
		baseUp := 10 + gmath.Float32()*40
		return baseDown * (1 + float32(streams)/32), baseUp * (1 + float32(streams)/32)
	}
	d1, u1 := mk(1)
	d4, u4 := mk(4)
	d16, u16 := mk(16)
	d64, u64 := mk(64)
	return &dev.SpeedTestResponse{
		DownloadMbps_1TcpConn:  d1,
		UploadMbps_1TcpConn:    u1,
		DownloadMbps_4TcpConn:  d4,
		UploadMbps_4TcpConn:    u4,
		DownloadMbps_16TcpConn: d16,
		UploadMbps_16TcpConn:   u16,
		DownloadMbps_64TcpConn: d64,
		UploadMbps_64TcpConn:   u64,
		RouterSpeedtest: &dev.SpeedTestStats{
			UploadStartTime:   time.Now().Add(-5 * time.Second).Unix(),
			DownloadStartTime: time.Now().Unix(),
			UploadMbps:        u16,
			DownloadMbps:      d16,
			Target:            dev.SpeedTestStats_CLOUDFLARE,
			TcpStreams:        16,
		},
	}
}

func (c *Core) randDeviceInfo() *dev.GetDeviceInfoResponse {
	return &dev.GetDeviceInfoResponse{DeviceInfo: &dev.DeviceInfo{
		Id:              c.profile.DeviceID,
		HardwareVersion: c.profile.DishHW,
		SoftwareVersion: c.profile.DishSW,
		CountryCode:     c.profile.Country,
		// Note: DeviceInfo here has no SKU/Position in this schema; omit.
	}}
}

func (c *Core) randInterfaces() *dev.GetNetworkInterfacesResponse {
	return &dev.GetNetworkInterfacesResponse{NetworkInterfaces: []*dev.NetworkInterface{
		{Name: "eth0", MacAddress: c.randMAC(), Ipv4Addresses: []string{"192.168.1.1"}},
		{Name: "wlan0", MacAddress: c.randMAC(), Ipv4Addresses: []string{"192.168.1.2"}},
	}}
}

func (c *Core) randDishDiagnostics() *dev.DishGetDiagnosticsResponse {
	return &dev.DishGetDiagnosticsResponse{
		DisablementCode: dev.DishGetDiagnosticsResponse_OKAY,
	}
}

func (c *Core) randEvent() *dev.Event {
	return &dev.Event{Event: &dev.Event_WifiCloudStatus{WifiCloudStatus: &dev.WifiCloudStatusEvent{
		ApiVersion:       1,
		DirectLinkToDish: true,
		HardwareVersion:  c.profile.RouterHW,
		IsBypassed:       false,
	}}}
}

func (c *Core) randMeshStatus() *dev.WifiGlobalMeshStatus {
	return &dev.WifiGlobalMeshStatus{SoftwareVersion: c.profile.RouterSW, HardwareVersion: c.profile.RouterHW}
}

func (c *Core) randMAC() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	// Unicast, locally administered
	b[0] = (b[0] & 0xfe) | 0x02
	return fmt.Sprintf("%s:%s:%s:%s:%s:%s",
		hex.EncodeToString(b[0:1]),
		hex.EncodeToString(b[1:2]),
		hex.EncodeToString(b[2:3]),
		hex.EncodeToString(b[3:4]),
		hex.EncodeToString(b[4:5]),
		hex.EncodeToString(b[5:6]))
}
