package server

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	systemservice "boyler/internal/daemon/application/system_service"
	grpchandler "boyler/internal/daemon/infrastructure/inbound/api/grpc"
	pb "boyler/internal/daemon/infrastructure/inbound/api/grpc/gen"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health/grpc_health_v1"
)

type serverInspectionStub struct{}

func (serverInspectionStub) SystemInfo(context.Context) (systemservice.SystemInfo, error) {
	return systemservice.SystemInfo{OS: "linux", DaemonPID: 42}, nil
}
func (serverInspectionStub) Doctor(context.Context) (systemservice.DoctorReport, error) {
	return systemservice.DoctorReport{Healthy: true}, nil
}
func (serverInspectionStub) Version(context.Context) systemservice.VersionInfo {
	return systemservice.VersionInfo{Version: "test", APIVersion: "v1"}
}

func TestGrpcServerHealthAndInspectionOverUnixSocket(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "boyler.sock")
	server := NewGrpcServer(socket, grpchandler.NewDaemonHandler(nil, nil), grpchandler.NewInspectionHandler(serverInspectionStub{}))
	result := make(chan error, 1)
	go func() { result <- server.Start() }()
	t.Cleanup(server.Stop)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var connection *grpc.ClientConn
	var err error
	for ctx.Err() == nil {
		connection, err = grpc.NewClient("unix://"+socket, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err == nil {
			_, err = grpc_health_v1.NewHealthClient(connection).Check(ctx, &grpc_health_v1.HealthCheckRequest{Service: pb.DaemonInspectionService_ServiceDesc.ServiceName})
			if err == nil {
				break
			}
			_ = connection.Close()
		}
		select {
		case startErr := <-result:
			if errors.Is(startErr, os.ErrPermission) || strings.Contains(startErr.Error(), "operation not permitted") {
				t.Skipf("Unix sockets are unavailable in this sandbox: %v", startErr)
			}
			t.Fatalf("server failed: %v", startErr)
		case <-time.After(10 * time.Millisecond):
		}
	}
	if err != nil {
		t.Fatalf("health check: %v", err)
	}
	defer connection.Close()
	version, err := pb.NewDaemonInspectionServiceClient(connection).Version(ctx, &pb.VersionRequest{})
	if err != nil || version.Version != "test" {
		t.Fatalf("version = %#v, %v", version, err)
	}
	info, err := os.Lstat(socket)
	if err != nil || info.Mode().Perm() != 0660 {
		t.Fatalf("socket mode = %v, %v", info, err)
	}
}

func TestGrpcServerRefusesNonSocketAndSymlinkParents(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "not-a-socket")
	if err := os.WriteFile(file, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	server := NewGrpcServer(file, grpchandler.NewDaemonHandler(nil, nil), grpchandler.NewInspectionHandler(serverInspectionStub{}))
	if err := server.Start(); err == nil || !strings.Contains(err.Error(), "non-socket") {
		t.Fatalf("error = %v", err)
	}
	outside := t.TempDir()
	link := filepath.Join(root, "linked")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	server = NewGrpcServer(filepath.Join(link, "boyler.sock"), grpchandler.NewDaemonHandler(nil, nil), grpchandler.NewInspectionHandler(serverInspectionStub{}))
	if err := server.Start(); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("error = %v", err)
	}
}
