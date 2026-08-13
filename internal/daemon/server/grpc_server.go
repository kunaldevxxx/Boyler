package server

import (
	grpchandler "boyler/internal/daemon/infrastructure/inbound/api/grpc"
	pb "boyler/internal/daemon/infrastructure/inbound/api/grpc/gen"
	inter "boyler/internal/daemon/infrastructure/inbound/api/grpc/interceptor"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	grpc_health_v1 "google.golang.org/grpc/health/grpc_health_v1"
)

type Server struct {
	grpcServer   *grpc.Server
	healthServer *health.Server
	socketPath   string
	socketGroup  string
}

const (
	unaryRequestTimeout = 20 * time.Second
	imagePruneTimeout   = 2 * time.Minute
)

type ServerOption func(*Server)

func WithSocketGroup(group string) ServerOption {
	return func(server *Server) { server.socketGroup = group }
}

func NewGrpcServer(socketPath string, daemonHandler *grpchandler.DaemonHandler, inspectionHandler *grpchandler.InspectionHandler, options ...ServerOption) *Server {
	grpcServer := grpc.NewServer(
		grpc.UnaryInterceptor(inter.ContextInterceptor(unaryRequestTimeout, map[string]time.Duration{
			pb.ImageService_PruneImages_FullMethodName: imagePruneTimeout,
		})),
		grpc.MaxRecvMsgSize(4<<20), grpc.MaxSendMsgSize(4<<20),
	)
	pb.RegisterContainerServiceServer(grpcServer, daemonHandler)
	pb.RegisterImageServiceServer(grpcServer, daemonHandler)
	pb.RegisterDaemonInspectionServiceServer(grpcServer, inspectionHandler)
	healthServer := health.NewServer()
	healthServer.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)
	healthServer.SetServingStatus(pb.DaemonInspectionService_ServiceDesc.ServiceName, grpc_health_v1.HealthCheckResponse_SERVING)
	grpc_health_v1.RegisterHealthServer(grpcServer, healthServer)
	server := &Server{
		grpcServer:   grpcServer,
		healthServer: healthServer,
		socketPath:   socketPath,
	}
	for _, option := range options {
		option(server)
	}
	return server
}

func (s *Server) Start() error {
	if s.socketPath == "" || !filepath.IsAbs(s.socketPath) {
		return fmt.Errorf("unix socket path must be absolute")
	}
	if err := rejectSymlinkParents(filepath.Dir(s.socketPath)); err != nil {
		return err
	}
	if err := os.MkdirAll(parentDir(s.socketPath), 0750); err != nil {
		return fmt.Errorf("Failed mkdir: %v", err)
	}
	if info, err := os.Lstat(s.socketPath); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || info.Mode()&os.ModeSocket == 0 {
			return fmt.Errorf("refuse to replace non-socket path %s", s.socketPath)
		}
		if err := os.Remove(s.socketPath); err != nil {
			return fmt.Errorf("remove stale unix socket: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("inspect unix socket: %w", err)
	}
	lis, err := net.Listen("unix", s.socketPath)
	if err != nil {
		return fmt.Errorf("Failed to listen unix-socket: %v", err)
	}
	if err := os.Chmod(s.socketPath, 0660); err != nil {
		_ = lis.Close()
		return fmt.Errorf("secure unix socket: %w", err)
	}
	if s.socketGroup != "" {
		group, err := user.LookupGroup(s.socketGroup)
		if err != nil {
			_ = lis.Close()
			return fmt.Errorf("resolve socket group %q: %w", s.socketGroup, err)
		}
		gid, err := strconv.Atoi(group.Gid)
		if err != nil {
			_ = lis.Close()
			return fmt.Errorf("parse socket group %q: %w", s.socketGroup, err)
		}
		if err := os.Chown(s.socketPath, -1, gid); err != nil {
			_ = lis.Close()
			return fmt.Errorf("set socket group %q: %w", s.socketGroup, err)
		}
	}
	return s.grpcServer.Serve(peerCredentialListener{Listener: lis})
}

func (s *Server) Stop() {
	if s.healthServer != nil {
		s.healthServer.Shutdown()
	}
	if s.grpcServer != nil {
		s.grpcServer.GracefulStop()
	}
	if err := os.Remove(s.socketPath); err != nil && !os.IsNotExist(err) {
		slog.Error("Failed to delete unix-socket during shutdown", slog.String("error", err.Error()))
	}
}

func parentDir(socketPath string) string {
	return filepath.Dir(socketPath)
}

func rejectSymlinkParents(path string) error {
	current := filepath.Clean(path)
	for {
		info, err := os.Lstat(current)
		if err == nil && info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("socket parent must not contain symlinks: %s", current)
		}
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("inspect socket parent %s: %w", current, err)
		}
		parent := filepath.Dir(current)
		if parent == current {
			return nil
		}
		current = parent
	}
}
