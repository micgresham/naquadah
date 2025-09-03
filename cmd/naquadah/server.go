package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"net"
	"time"

	dev "github.com/b0ch3nski/go-starlink/model/api-protoc/device"
	unlock "github.com/b0ch3nski/go-starlink/model/api-protoc/device/services/unlock"
	"github.com/b0ch3nski/go-starlink/model/internal/sim"
	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"
)

func run() {
	var (
		port    = flag.Int("port", 9200, "gRPC port to listen on")
		seed    = flag.Int64("seed", time.Now().UnixNano(), "random seed")
		noisy   = flag.Bool("noisy", false, "log every request")
		events  = flag.Bool("events", true, "emit periodic events on streams")
		cfgPath = flag.String("config", "naquadah.yaml", "path to YAML device config")
		genCfg  = flag.Bool("gen-config", false, "generate a default YAML config at -config and exit")
	)
	flag.Parse()

	rand.Seed(*seed)

	if *genCfg {
		if err := sim.WriteTemplateConfig(*cfgPath, 0o644, false); err != nil {
			log.Fatalf("write config: %v", err)
		}
		log.Printf("Wrote template config to %s\n", *cfgPath)
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

	dev.RegisterDeviceServer(s, &deviceServer{core: core})
	dev.RegisterMeshServer(s, &meshServer{core: core})
	unlock.RegisterUnlockServiceServer(s, &unlockServer{core: core})

	log.Printf("naquadah listening on :%d (seed=%d)\n", *port, *seed)
	if err := s.Serve(lis); err != nil {
		log.Fatalf("serve: %v", err)
	}
}

type deviceServer struct {
	dev.UnimplementedDeviceServer
	core *sim.Core
}

func (s *deviceServer) Stream(stream dev.Device_StreamServer) error {
	return s.core.DeviceStream(stream)
}

func (s *deviceServer) Handle(ctx context.Context, req *dev.Request) (*dev.Response, error) {
	return s.core.HandleDeviceRequest(ctx, req)
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
