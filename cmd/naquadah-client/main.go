package main

import (
	context "context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	dev "github.com/b0ch3nski/go-starlink/model/api-protoc/device"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// snapshot holds a merged view of several request responses.
type snapshot struct {
	Timestamp   time.Time                   `json:"ts"`
	DishStatus  *dev.DishGetStatusResponse  `json:"dish_status,omitempty"`
	WifiStatus  *dev.WifiGetStatusResponse  `json:"wifi_status,omitempty"`
	WifiClients *dev.WifiGetClientsResponse `json:"wifi_clients,omitempty"`
	Speedtest   *dev.SpeedTestResponse      `json:"speedtest,omitempty"`
	PingAll     *dev.GetPingResponse        `json:"ping_all,omitempty"`
	Errors      map[string]string           `json:"errors,omitempty"`
}

func main() {
	addr := flag.String("addr", "127.0.0.1:9200", "naquadah gRPC address")
	out := flag.String("out", "", "if set, append JSON snapshot/stream lines to this file")
	fmtFlag := flag.String("format", "pretty", "output format: pretty|compact|line")
	timeout := flag.Duration("timeout", 5*time.Second, "per-request timeout")
	interval := flag.Duration("interval", 0, "if >0, poll repeatedly at this interval (snapshot mode)")
	stream := flag.Bool("stream", false, "enable bidirectional streaming mode")
	streamInterval := flag.Duration("stream-interval", 10*time.Second, "interval between request bursts in stream mode")
	streamSpeedtest := flag.Bool("stream-speedtest", false, "include speedtest request in each stream interval burst")
	flag.Parse()

	if *stream {
		if err := streamMode(*addr, *timeout, *streamInterval, *fmtFlag, *out, *streamSpeedtest); err != nil {
			log.Fatalf("stream: %v", err)
		}
		return
	}

	for {
		ss, err := collect(*addr, *timeout)
		if err != nil {
			log.Fatalf("collect: %v", err)
		}
		emit(ss, *fmtFlag, *out)
		if *interval <= 0 {
			break
		}
		time.Sleep(*interval)
	}
}

func collect(addr string, timeout time.Duration) (*snapshot, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	conn, err := grpc.DialContext(ctx, addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	client := dev.NewDeviceClient(conn)
	errMap := map[string]string{}
	ss := &snapshot{Timestamp: time.Now().UTC(), Errors: errMap}

	// helper to invoke Handle with retries only if context allows
	do := func(name string, req *dev.Request) *dev.Response {
		c2, cancel2 := context.WithTimeout(ctx, timeout)
		defer cancel2()
		resp, err := client.Handle(c2, req)
		if err != nil {
			errMap[name] = err.Error()
			return nil
		}
		return resp
	}

	// Dish GetStatus
	if r := do("dish_get_status", &dev.Request{Id: 1, TargetId: "dish", Request: &dev.Request_GetStatus{GetStatus: &dev.GetStatusRequest{}}}); r != nil {
		if x, ok := r.Response.(*dev.Response_DishGetStatus); ok {
			ss.DishStatus = x.DishGetStatus
		}
	}
	// Wifi GetStatus
	if r := do("wifi_get_status", &dev.Request{Id: 2, TargetId: "wifi", Request: &dev.Request_GetStatus{GetStatus: &dev.GetStatusRequest{}}}); r != nil {
		if x, ok := r.Response.(*dev.Response_WifiGetStatus); ok {
			ss.WifiStatus = x.WifiGetStatus
		}
	}
	// Wifi Clients
	if r := do("wifi_get_clients", &dev.Request{Id: 3, Request: &dev.Request_WifiGetClients{WifiGetClients: &dev.WifiGetClientsRequest{}}}); r != nil {
		if x, ok := r.Response.(*dev.Response_WifiGetClients); ok {
			ss.WifiClients = x.WifiGetClients
		}
	}
	// Speedtest snapshot
	if r := do("speedtest", &dev.Request{Id: 4, Request: &dev.Request_SpeedTest{SpeedTest: &dev.SpeedTestRequest{}}}); r != nil {
		if x, ok := r.Response.(*dev.Response_SpeedTest); ok {
			ss.Speedtest = x.SpeedTest
		}
	}
	// Ping metrics
	if r := do("ping_all", &dev.Request{Id: 5, Request: &dev.Request_GetPing{GetPing: &dev.GetPingRequest{}}}); r != nil {
		if x, ok := r.Response.(*dev.Response_GetPing); ok {
			ss.PingAll = x.GetPing
		}
	}

	if len(ss.Errors) == 0 {
		ss.Errors = nil
	}
	return ss, nil
}

func emit(s *snapshot, format, outPath string) {
	var b []byte
	var err error
	switch format {
	case "compact":
		b, err = json.Marshal(s)
	case "line":
		b, err = json.Marshal(s)
		if err == nil {
			b = append(b, '\n')
		}
	default:
		b, err = json.MarshalIndent(s, "", "  ")
	}
	if err != nil {
		log.Printf("marshal: %v", err)
		return
	}
	if outPath != "" {
		f, err := os.OpenFile(outPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			log.Printf("open out: %v", err)
			return
		}
		defer f.Close()
		if _, err := f.Write(b); err != nil {
			log.Printf("write out: %v", err)
		}
		if format != "line" { // ensure newline for non-line formats too
			f.Write([]byte("\n"))
		}
	} else {
		os.Stdout.Write(b)
		if format != "line" {
			fmt.Println()
		}
	}
}

// --- Streaming Mode ---

type streamRecord struct {
	Ts          time.Time                   `json:"ts"`
	Kind        string                      `json:"kind"` // response|event|health|error
	Oneof       string                      `json:"oneof,omitempty"`
	DishStatus  *dev.DishGetStatusResponse  `json:"dish_status,omitempty"`
	WifiStatus  *dev.WifiGetStatusResponse  `json:"wifi_status,omitempty"`
	WifiClients *dev.WifiGetClientsResponse `json:"wifi_clients,omitempty"`
	Speedtest   *dev.SpeedTestResponse      `json:"speedtest,omitempty"`
	PingAll     *dev.GetPingResponse        `json:"ping_all,omitempty"`
	Event       interface{}                 `json:"event,omitempty"`
	Health      *dev.HealthCheck            `json:"health,omitempty"`
	Error       string                      `json:"error,omitempty"`
}

func streamMode(addr string, timeout, interval time.Duration, format, outPath string, includeSpeedtest bool) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Handle Ctrl+C
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
	}()

	conn, err := grpc.DialContext(ctx, addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return err
	}
	defer conn.Close()
	client := dev.NewDeviceClient(conn)
	stream, err := client.Stream(ctx)
	if err != nil {
		return err
	}

	// Sender goroutine: periodic request burst.
	go func() {
		id := uint64(1)
		burst := func() {
			// Dish status
			_ = stream.Send(&dev.ToDevice{Message: &dev.ToDevice_Request{Request: &dev.Request{Id: id, TargetId: "dish", Request: &dev.Request_GetStatus{GetStatus: &dev.GetStatusRequest{}}}}})
			id++
			// Wifi status
			_ = stream.Send(&dev.ToDevice{Message: &dev.ToDevice_Request{Request: &dev.Request{Id: id, TargetId: "wifi", Request: &dev.Request_GetStatus{GetStatus: &dev.GetStatusRequest{}}}}})
			id++
			// Wifi clients
			_ = stream.Send(&dev.ToDevice{Message: &dev.ToDevice_Request{Request: &dev.Request{Id: id, Request: &dev.Request_WifiGetClients{WifiGetClients: &dev.WifiGetClientsRequest{}}}}})
			id++
			// Ping all
			_ = stream.Send(&dev.ToDevice{Message: &dev.ToDevice_Request{Request: &dev.Request{Id: id, Request: &dev.Request_GetPing{GetPing: &dev.GetPingRequest{}}}}})
			id++
			if includeSpeedtest {
				_ = stream.Send(&dev.ToDevice{Message: &dev.ToDevice_Request{Request: &dev.Request{Id: id, Request: &dev.Request_SpeedTest{SpeedTest: &dev.SpeedTestRequest{}}}}})
				id++
			}
		}
		burst()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				burst()
			}
		}
	}()

	for {
		msg, err := stream.Recv()
		if err != nil {
			rec := &streamRecord{Ts: time.Now().UTC(), Kind: "error", Error: err.Error()}
			emitStreamRecord(rec, format, outPath)
			return nil
		}
		rec := &streamRecord{Ts: time.Now().UTC()}
		switch m := msg.GetMessage().(type) {
		case *dev.FromDevice_Response:
			rec.Kind = "response"
			if m.Response != nil {
				switch r := m.Response.Response.(type) {
				case *dev.Response_DishGetStatus:
					rec.Oneof = "DishGetStatus"
					rec.DishStatus = r.DishGetStatus
				case *dev.Response_WifiGetStatus:
					rec.Oneof = "WifiGetStatus"
					rec.WifiStatus = r.WifiGetStatus
				case *dev.Response_WifiGetClients:
					rec.Oneof = "WifiGetClients"
					rec.WifiClients = r.WifiGetClients
				case *dev.Response_SpeedTest:
					rec.Oneof = "SpeedTest"
					rec.Speedtest = r.SpeedTest
				case *dev.Response_GetPing:
					rec.Oneof = "GetPing"
					rec.PingAll = r.GetPing
				default:
					rec.Oneof = fmt.Sprintf("%T", r)
				}
			}
		case *dev.FromDevice_Event:
			rec.Kind = "event"
			rec.Event = m.Event
		case *dev.FromDevice_HealthCheck:
			rec.Kind = "health"
			rec.Health = m.HealthCheck
		default:
			rec.Kind = "unknown"
		}
		emitStreamRecord(rec, format, outPath)
		if ctx.Err() != nil {
			return nil
		}
	}
}

func emitStreamRecord(rec *streamRecord, format, outPath string) {
	var b []byte
	var err error
	switch format {
	case "compact", "line":
		b, err = json.Marshal(rec)
		if err == nil && format == "line" {
			b = append(b, '\n')
		}
	default:
		b, err = json.MarshalIndent(rec, "", "  ")
		if err == nil {
			b = append(b, '\n')
		}
	}
	if err != nil {
		log.Printf("marshal stream: %v", err)
		return
	}
	if outPath != "" {
		f, err := os.OpenFile(outPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			log.Printf("open out: %v", err)
			return
		}
		defer f.Close()
		if _, err := f.Write(b); err != nil {
			log.Printf("write out: %v", err)
		}
	} else {
		os.Stdout.Write(b)
		if format != "line" && format != "compact" {
			// already appended newline for pretty; no-op
		}
	}
}
