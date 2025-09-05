package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"net"
	"net/http"
	"time"

	dev "github.com/b0ch3nski/go-starlink/model/api-protoc/device"
	unlock "github.com/b0ch3nski/go-starlink/model/api-protoc/device/services/unlock"
	"github.com/b0ch3nski/go-starlink/model/internal/metrics"
	"github.com/b0ch3nski/go-starlink/model/internal/rules"
	"github.com/b0ch3nski/go-starlink/model/internal/sim"
	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"

	"github.com/b0ch3nski/go-starlink/model/internal/admin"
)

func run() {
	var (
		port           = flag.Int("port", 9200, "gRPC port to listen on")
		seed           = flag.Int64("seed", time.Now().UnixNano(), "random seed")
		noisy          = flag.Bool("noisy", false, "log every request")
		events         = flag.Bool("events", true, "emit periodic events on streams")
		cfgPath        = flag.String("config", "naquadah.yaml", "path to YAML device config")
		genCfg         = flag.Bool("gen-config", false, "generate a default YAML config at -config and exit")
		rulesPath      = flag.String("rules", "", "rules YAML path (optional)")
		genRules       = flag.Bool("gen-rules", false, "write example rules file if missing then exit")
		recordJSON     = flag.String("record-json", "", "record samples to JSON file (implies random source unless -real-target)")
		recordInterval = flag.Duration("record-interval", 60*time.Second, "recording interval")
		playbackJSON   = flag.String("playback-json", "", "playback samples from JSON file")
		playbackLoop   = flag.Bool("playback-loop", true, "loop playback when end reached")
		baselineJSON   = flag.String("baseline-json", "", "baseline hybrid samples JSON (adds jitter)")
		playbackScale  = flag.Float64("playback-scale", 1.0, "playback advancement scale (>1 faster, <1 slower)")
		metricsAddr    = flag.String("metrics", "", "prometheus metrics listen address (e.g. :9090)")
		adminAddr      = flag.String("admin", "", "admin http listen address (e.g. :8080) for runtime overrides")
		realTarget     = flag.String("real-target", "", "real dish host:port to poll (enables real poller)")
		realToken      = flag.String("real-token", "", "auth token for real dish (optional)")
		realTimeout    = flag.Duration("real-timeout", 5*time.Second, "timeout per real dish request")
	)
	flag.Parse()

	rand.Seed(*seed)

	if *metricsAddr != "" {
		metrics.Init(*metricsAddr)
	}

	if *genCfg {
		if err := sim.WriteTemplateConfig(*cfgPath, 0o644, false); err != nil {
			log.Fatalf("write config: %v", err)
		}
		log.Printf("Wrote template config to %s\n", *cfgPath)
		return
	}
	if *genRules {
		if *rulesPath == "" {
			log.Fatalf("-rules path required with -gen-rules")
		}
		if err := rules.WriteTemplate(*rulesPath, 0o644); err != nil {
			log.Fatalf("write rules: %v", err)
		}
		log.Printf("Wrote rules template to %s", *rulesPath)
		return
	}

	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", *port))
	if err != nil {
		log.Fatalf("listen: %v", err)
	}

	s := grpc.NewServer(
		grpc.KeepaliveParams(keepalive.ServerParameters{Time: 2 * time.Minute}),
	)

	opts := sim.CoreOptions{Noisy: *noisy, EmitEvents: *events}
	profile := sim.DefaultProfile()
	if p, err := sim.LoadConfig(*cfgPath); err == nil {
		profile = p
		log.Printf("Loaded config from %s\n", *cfgPath)
	}
	core := sim.NewCoreWithProfile(opts, profile)

	// Optional admin state & HTTP server
	var adminState *admin.State
	if *adminAddr != "" {
		adminState = admin.NewState()
		go func() {
			log.Printf("admin listening on %s", *adminAddr)
			if err := http.ListenAndServe(*adminAddr, adminState.Handler()); err != nil {
				log.Printf("admin server error: %v", err)
			}
		}()
	}

	// Data provider selection
	if *playbackJSON != "" || *baselineJSON != "" {
		prov, err := sim.BuildProvider(*playbackJSON, *playbackLoop, *playbackScale, *baselineJSON, *seed)
		if err != nil {
			log.Fatalf("provider: %v", err)
		}
		if prov != nil {
			core.SetDataProvider(prov)
			log.Printf("Data provider active (%s)", func() string {
				if *playbackJSON != "" {
					return "playback"
				}
				return "baseline"
			}())
		}
	}

	// Recorder (local synthetic capture). Future real target polling could be added here.
	if *recordJSON != "" {
		rec := sim.NewRecorder(core, *recordJSON, *recordInterval)
		rec.Start()
		log.Printf("Recording samples every %s to %s", recordInterval.String(), *recordJSON)
		// No handle to stop; process lifetime.
	}

	// Real dish polling (parallel to recorder, appends to same file if provided)
	if *realTarget != "" {
		pollPath := *recordJSON
		if pollPath == "" {
			pollPath = "real_capture.json"
		}
		poller, err := sim.NewRealPoller(*realTarget, *recordInterval, *realTimeout, *realToken, pollPath)
		if err != nil {
			log.Fatalf("real poller: %v", err)
		}
		poller.Start()
		log.Printf("Real poller active target=%s interval=%s output=%s", *realTarget, recordInterval.String(), pollPath)
	}

	var ruleEngine *rules.Engine
	if *rulesPath != "" {
		eng, err := rules.Load(*rulesPath)
		if err != nil {
			log.Fatalf("load rules: %v", err)
		}
		ruleEngine = eng
		log.Printf("Loaded rules from %s", *rulesPath)
	}

	dev.RegisterDeviceServer(s, &deviceServer{core: core, rules: ruleEngine, admin: adminState})
	dev.RegisterMeshServer(s, &meshServer{core: core})
	unlock.RegisterUnlockServiceServer(s, &unlockServer{core: core})

	log.Printf("naquadah listening on :%d (seed=%d)\n", *port, *seed)
	if err := s.Serve(lis); err != nil {
		log.Fatalf("serve: %v", err)
	}
}

type deviceServer struct {
	dev.UnimplementedDeviceServer
	core  *sim.Core
	rules *rules.Engine
	admin *admin.State
}

func (s *deviceServer) Stream(stream dev.Device_StreamServer) error {
	return s.core.DeviceStream(stream)
}

func (s *deviceServer) Handle(ctx context.Context, req *dev.Request) (*dev.Response, error) {
	start := time.Now()
	if s.rules != nil {
		if err := s.rules.ApplyPre(ctx, req); err != nil {
			return nil, err
		}
	}
	resp, err := s.core.HandleDeviceRequest(ctx, req)
	if err != nil {
		return nil, err
	}
	// Admin one-shot injected error (highest priority)
	if s.admin != nil {
		if ok, code, msg := s.admin.ConsumeError(); ok {
			return nil, fmt.Errorf("injected error code=%d msg=%s", code, msg)
		}
	}
	if s.rules != nil {
		pr := s.rules.ApplyPost(resp, req)
		if pr.Drop {
			return nil, nil
		}
		if pr.Err != nil {
			return nil, pr.Err
		}
	}
	// Apply admin overrides (dish status fields & alarms, etc.)
	if s.admin != nil {
		if dr, ok := resp.Response.(*dev.Response_DishGetStatus); ok {
			s.admin.ApplyDish(dr.DishGetStatus)
		}
	}
	// metrics
	key := fmt.Sprintf("%T", req.GetRequest())
	metrics.IncRequest(key)
	metrics.ObserveLatency(key, time.Since(start).Seconds())
	return resp, nil
}

type meshServer struct {
	dev.UnimplementedMeshServer
	core *sim.Core
}

func (s *meshServer) MeshStream(stream dev.Mesh_MeshStreamServer) error {
	return s.core.MeshStream(stream)
}

type unlockServer struct {
	unlock.UnimplementedUnlockServiceServer
	core *sim.Core
}

func (s *unlockServer) StartUnlock(ctx context.Context, r *unlock.StartUnlockRequest) (*unlock.StartUnlockResponse, error) {
	return s.core.StartUnlock(ctx, r)
}

func (s *unlockServer) FinishUnlock(ctx context.Context, r *unlock.FinishUnlockRequest) (*unlock.FinishUnlockResponse, error) {
	return s.core.FinishUnlock(ctx, r)
}
