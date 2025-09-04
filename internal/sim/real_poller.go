package sim

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"time"

	dev "github.com/b0ch3nski/go-starlink/model/api-protoc/device"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

// RealPoller polls an actual Starlink device for a subset of telemetry and appends Samples.
// This is best-effort; failures are logged and skipped.
type RealPoller struct {
	addr     string
	interval time.Duration
	timeout  time.Duration
	token    string
	path     string // optional JSON capture path; if empty, discard
	stopCh   chan struct{}
	conn     *grpc.ClientConn
	client   dev.DeviceClient
}

func NewRealPoller(addr string, interval, timeout time.Duration, token, path string) (*RealPoller, error) {
	conn, err := grpc.Dial(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}
	return &RealPoller{addr: addr, interval: interval, timeout: timeout, token: token, path: path, stopCh: make(chan struct{}), conn: conn, client: dev.NewDeviceClient(conn)}, nil
}

func (p *RealPoller) Start() { go p.loop() }
func (p *RealPoller) Stop()  { close(p.stopCh); _ = p.conn.Close() }

func (p *RealPoller) loop() {
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			p.captureOnce()
		case <-p.stopCh:
			return
		}
	}
}

func (p *RealPoller) ctx() (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(context.Background(), p.timeout)
	if p.token != "" {
		ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+p.token)
	}
	return ctx, cancel
}

func (p *RealPoller) captureOnce() {
	// Dish status
	var dish *dev.DishGetStatusResponse
	var wifi *dev.WifiGetStatusResponse
	var clients *dev.WifiGetClientsResponse
	var speed *dev.SpeedTestResponse
	var pingAll *dev.GetPingResponse

	call := func(r *dev.Request) *dev.Response {
		ctx, cancel := p.ctx()
		defer cancel()
		resp, err := p.client.Handle(ctx, r)
		if err != nil {
			log.Printf("real-poller: handle error %v", err)
			return nil
		}
		return resp
	}

	// Dish GetStatus
	if resp := call(&dev.Request{Id: 1, Request: &dev.Request_GetStatus{GetStatus: &dev.GetStatusRequest{}}, TargetId: "dish"}); resp != nil {
		if ds, ok := resp.Response.(*dev.Response_DishGetStatus); ok {
			dish = ds.DishGetStatus
		}
	}
	// Wifi GetStatus
	if resp := call(&dev.Request{Id: 2, Request: &dev.Request_GetStatus{GetStatus: &dev.GetStatusRequest{}}, TargetId: "wifi"}); resp != nil {
		if ws, ok := resp.Response.(*dev.Response_WifiGetStatus); ok {
			wifi = ws.WifiGetStatus
		}
	}
	// Wifi clients
	if resp := call(&dev.Request{Id: 3, Request: &dev.Request_WifiGetClients{WifiGetClients: &dev.WifiGetClientsRequest{}}}); resp != nil {
		if wc, ok := resp.Response.(*dev.Response_WifiGetClients); ok {
			clients = wc.WifiGetClients
		}
	}
	// Speedtest snapshot (non-running) - use SpeedTest request for aggregated metrics if available
	if resp := call(&dev.Request{Id: 4, Request: &dev.Request_SpeedTest{SpeedTest: &dev.SpeedTestRequest{}}}); resp != nil {
		if st, ok := resp.Response.(*dev.Response_SpeedTest); ok {
			speed = st.SpeedTest
		}
	}
	// Ping all
	if resp := call(&dev.Request{Id: 5, Request: &dev.Request_GetPing{GetPing: &dev.GetPingRequest{}}}); resp != nil {
		if pg, ok := resp.Response.(*dev.Response_GetPing); ok {
			pingAll = pg.GetPing
		}
	}

	sample := &Sample{Ts: time.Now().UTC(), DishStatus: dish, WifiStatus: wifi, WifiClients: clients, Speedtest: speed, PingAll: pingAll}
	if p.path != "" {
		p.append(sample)
	}
}

func (p *RealPoller) append(s *Sample) {
	existing, _ := LoadSamples(p.path)
	existing = append(existing, s)
	b, _ := json.MarshalIndent(existing, "", "  ")
	tmp := p.path + ".tmp"
	_ = os.WriteFile(tmp, b, 0o644)
	_ = os.Rename(tmp, p.path)
}
