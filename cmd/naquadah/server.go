package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/b0ch3nski/go-starlink/model/internal/auth"

	dev "github.com/b0ch3nski/go-starlink/model/api-protoc/device"
	unlock "github.com/b0ch3nski/go-starlink/model/api-protoc/device/services/unlock"
	"github.com/b0ch3nski/go-starlink/model/internal/metrics"
	"github.com/b0ch3nski/go-starlink/model/internal/rules"
	"github.com/b0ch3nski/go-starlink/model/internal/sim"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/keepalive"

	"github.com/b0ch3nski/go-starlink/model/internal/admin"
	"github.com/b0ch3nski/go-starlink/model/internal/tlsutil"
	"github.com/grandcat/zeroconf"
)

func run() {
	var (
		port            = flag.Int("port", 9200, "gRPC port to listen on")
		seed            = flag.Int64("seed", time.Now().UnixNano(), "random seed")
		noisy           = flag.Bool("noisy", false, "log every request")
		events          = flag.Bool("events", true, "emit periodic events on streams")
		cfgPath         = flag.String("config", "naquadah.yaml", "path to YAML device config")
		genCfg          = flag.Bool("gen-config", false, "generate a default YAML config at -config and exit")
		rulesPath       = flag.String("rules", "", "rules YAML path (optional)")
		genRules        = flag.Bool("gen-rules", false, "write example rules file if missing then exit")
		recordJSON      = flag.String("record-json", "", "record samples to JSON file (implies random source unless -real-target)")
		recordInterval  = flag.Duration("record-interval", 60*time.Second, "recording interval")
		playbackJSON    = flag.String("playback-json", "", "playback samples from JSON file")
		playbackLoop    = flag.Bool("playback-loop", true, "loop playback when end reached")
		baselineJSON    = flag.String("baseline-json", "", "baseline hybrid samples JSON (adds jitter)")
		playbackScale   = flag.Float64("playback-scale", 1.0, "playback advancement scale (>1 faster, <1 slower)")
		metricsAddr     = flag.String("metrics", "", "prometheus metrics listen address (e.g. :9090)")
		adminAddr       = flag.String("admin", "", "admin http listen address (e.g. :8080) for runtime overrides")
		restAddr        = flag.String("rest", "", "standalone REST API listen address (no embedded UI)")
		adminNoUI       = flag.Bool("admin-no-ui", false, "disable embedded UI (API only) on -admin listener")
		realRecordJSON  = flag.String("real-record-json", "", "record ONLY real dish samples to JSON file (used with -real-target)")
		useTLS          = flag.Bool("tls", false, "enable self-signed TLS for gRPC (experimental)")
		mdns            = flag.Bool("mdns", false, "announce _starlink._tcp via mDNS (best-effort)")
		certFile        = flag.String("tls-cert", "", "optional TLS cert file (overrides self-signed)")
		keyFile         = flag.String("tls-key", "", "optional TLS key file")
		realTarget      = flag.String("real-target", "", "real dish host:port to poll (enables real poller)")
		realToken       = flag.String("real-token", "", "auth token for real dish (optional)")
		realTimeout     = flag.Duration("real-timeout", 5*time.Second, "timeout per real dish request")
		rainIntensity   = flag.Float64("rain-fade-intensity", 0, "if >0 start rain fade simulation with given intensity (0-1)")
		rainDuration    = flag.Duration("rain-fade-duration", 30*time.Second, "rain fade iteration active duration")
		rainIterations  = flag.Int("rain-fade-iterations", 1, "rain fade iterations (0=infinite)")
		rainDelay       = flag.Duration("rain-fade-delay", 5*time.Second, "delay between rain fade iterations")
		shutdownTimeout = flag.Duration("shutdown-timeout", 8*time.Second, "max duration for graceful stop before force")
		oauthEnable     = flag.Bool("auth", false, "enable OAuth2 (JWT bearer) auth on admin/REST APIs")
		authIssuer      = flag.String("auth-issuer", "", "expected JWT issuer")
		authAudience    = flag.String("auth-audience", "", "expected JWT audience")
		authHS256       = flag.String("auth-hs256-secret", "", "HS256 shared secret (development only)")
		authJWKS        = flag.String("auth-jwks", "", "JWKS URL for asymmetric key validation")
		authJWKSRefresh = flag.Duration("auth-jwks-refresh", 5*time.Minute, "JWKS cache refresh interval")
	)
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "%s\n\n", AppDescription)
		fmt.Fprintf(flag.CommandLine.Output(), "Usage: %s [options]\n\nOptions:\n", os.Args[0])
		flag.PrintDefaults()
		fmt.Fprintf(flag.CommandLine.Output(), "\nExamples:\n")
		fmt.Fprintf(flag.CommandLine.Output(), "  # Run with admin UI and metrics\n  %s -admin :8080 -metrics :9090\n", os.Args[0])
		fmt.Fprintf(flag.CommandLine.Output(), "  # Start with rain fade 50%% intensity for 3 iterations\n  %s -rain-fade-intensity 0.5 -rain-fade-iterations 3 -admin :8080\n", os.Args[0])
		fmt.Fprintf(flag.CommandLine.Output(), "  # Playback previously recorded samples faster (2x)\n  %s -playback-json samples.json -playback-scale 2\n", os.Args[0])
		fmt.Fprintf(flag.CommandLine.Output(), "  # Record synthetic samples every 30s\n  %s -record-json out.json -record-interval 30s\n", os.Args[0])
		fmt.Fprintf(flag.CommandLine.Output(), "  # Real dish polling (experimental)\n  %s -real-target host:9200 -real-token TOKEN\n", os.Args[0])
		fmt.Fprintf(flag.CommandLine.Output(), "  # Standalone REST API (no UI)\n  %s -rest :8082\n", os.Args[0])
		fmt.Fprintf(flag.CommandLine.Output(), "  # Admin listener API only (suppress UI)\n  %s -admin :8080 -admin-no-ui\n", os.Args[0])
		fmt.Fprintf(flag.CommandLine.Output(), "\nVersion: %s\n%s\n%s\n", AppVersion, AppAuthor, AppHomepage)
	}
	flag.Parse()

	if *showVersion {
		fmt.Printf("naquadah %s\n%s\n%s\n", AppVersion, AppAuthor, AppHomepage)
		return
	}

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

	var serverOpts []grpc.ServerOption
	serverOpts = append(serverOpts, grpc.KeepaliveParams(keepalive.ServerParameters{Time: 2 * time.Minute}))
	if *useTLS {
		var certPair credentials.TransportCredentials
		if *certFile != "" && *keyFile != "" {
			creds, err := credentials.NewServerTLSFromFile(*certFile, *keyFile)
			if err != nil {
				log.Fatalf("load tls cert: %v", err)
			}
			certPair = creds
		} else {
			c, err := tlsutil.SelfSigned([]string{"localhost"})
			if err != nil {
				log.Fatalf("self-signed: %v", err)
			}
			certPair = credentials.NewServerTLSFromCert(&c)
			log.Printf("generated ephemeral self-signed TLS cert (24h) for host localhost")
		}
		serverOpts = append(serverOpts, grpc.Creds(certPair))
	}
	s := grpc.NewServer(serverOpts...)

	opts := sim.CoreOptions{Noisy: *noisy, EmitEvents: *events}
	profile := sim.DefaultProfile()
	if p, err := sim.LoadConfig(*cfgPath); err == nil {
		profile = p
		log.Printf("Loaded config from %s\n", *cfgPath)
	}
	core := sim.NewCoreWithProfile(opts, profile)

	// Optional admin / REST state & HTTP servers
	var adminState *admin.State
	if *adminAddr != "" || *restAddr != "" {
		adminState = admin.NewState()
	}

	startHTTP := func(addr string, includeUI bool, label string) {
		if addr == "" {
			return
		}
		go func() {
			log.Printf("%s listening on %s (ui=%v)", label, addr, includeUI)
			var h http.Handler
			if *oauthEnable {
				h = adminState.HandlerWithAuthOptions(auth.Config{Enabled: true, Issuer: *authIssuer, Audience: *authAudience, HS256Secret: *authHS256, JWKSURL: *authJWKS, JWKSRefresh: *authJWKSRefresh}, includeUI)
			} else {
				h = adminState.HandlerAPI(includeUI)
			}
			if err := http.ListenAndServe(addr, h); err != nil {
				log.Printf("%s server error: %v", label, err)
			}
		}()
	}

	// Start admin combined listener (UI optional)
	if *adminAddr != "" {
		startHTTP(*adminAddr, !*adminNoUI, "admin")
	}
	// Separate REST-only listener
	if *restAddr != "" {
		startHTTP(*restAddr, false, "rest")
	}

	if adminState != nil && *rainIntensity > 0 {
		adminState.StartRainFade(*rainIntensity, *rainDuration, *rainIterations, *rainDelay)
		log.Printf("rain fade started intensity=%.2f duration=%s iterations=%d delay=%s", *rainIntensity, rainDuration.String(), *rainIterations, rainDelay.String())
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

	// Recorder (local synthetic capture). Skip if dedicated real-only recording is requested.
	if *recordJSON != "" && *realRecordJSON == "" {
		rec := sim.NewRecorder(core, *recordJSON, *recordInterval)
		rec.Start()
		log.Printf("Recording synthetic samples every %s to %s", recordInterval.String(), *recordJSON)
		// No handle to stop; process lifetime.
	}

	// Real dish polling (parallel). If -real-record-json provided, use that exclusively (never mix with sim file).
	if *realTarget != "" {
		pollPath := *realRecordJSON
		if pollPath == "" { // fallback to prior behavior only if dedicated real path not set
			if *recordJSON != "" {
				pollPath = *recordJSON // legacy mixed capture
			} else {
				pollPath = "real_capture.json"
			}
		}
		poller, err := sim.NewRealPoller(*realTarget, *recordInterval, *realTimeout, *realToken, pollPath)
		if err != nil {
			log.Fatalf("real poller: %v", err)
		}
		poller.Start()
		mode := "mixed"
		if *realRecordJSON != "" {
			mode = "real-only"
		}
		log.Printf("Real poller active target=%s interval=%s output=%s mode=%s", *realTarget, recordInterval.String(), pollPath, mode)
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

	log.Printf("naquadah listening on :%d (seed=%d tls=%v version=%s)\n", *port, *seed, *useTLS, AppVersion)
	if *mdns {
		go announceMDNS(*port)
	}
	// Graceful shutdown & signal handling
	go func() {
		if err := s.Serve(lis); err != nil {
			log.Printf("serve ended: %v", err)
		}
	}()

	sigCh := make(chan os.Signal, 2)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	first := <-sigCh
	log.Printf("shutdown signal received (%v); initiating graceful shutdown", first)

	// Start graceful gRPC stop in background so we can time out
	grpcStopped := make(chan struct{})
	go func() { s.GracefulStop(); close(grpcStopped) }()

	// If second signal arrives, force stop immediately
	go func() {
		second := <-sigCh
		log.Printf("second signal (%v) forcing immediate shutdown", second)
		s.Stop()
	}()

	// Wait for graceful or timeout
	select {
	case <-grpcStopped:
		log.Printf("gRPC server stopped gracefully")
	case <-time.After(*shutdownTimeout):
		log.Printf("graceful shutdown timed out after %s; forcing stop", shutdownTimeout.String())
		s.Stop()
	}

	// Allow a brief moment for logs to flush
	time.Sleep(200 * time.Millisecond)
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

// announceMDNS performs a best-effort mDNS service announcement so local discovery tools
// (and potentially the official app) can see the simulator. Implementation kept minimal
// to avoid extra dependencies; if enhancement needed, integrate a proper mDNS library.
func announceMDNS(port int) {
	host := fmt.Sprintf("naquadah-%d", port)
	txt := []string{
		"txtvers=1",
		"app=naquadah",
		fmt.Sprintf("ver=%s", AppVersion),
		"proto=grpc",
	}
	server, err := zeroconf.Register(host, "_starlink._tcp", "local.", port, txt, nil)
	if err != nil {
		log.Printf("mDNS register failed: %v", err)
		return
	}
	log.Printf("mDNS announced service %s._starlink._tcp.local on port %d (TXT: %v)", host, port, txt)
	// Block until process exit; closed by graceful shutdown path.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	server.Shutdown()
}
