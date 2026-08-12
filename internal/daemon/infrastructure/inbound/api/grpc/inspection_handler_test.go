package grpc

import (
	"context"
	"testing"

	systemservice "boyler/internal/daemon/application/system_service"
	pb "boyler/internal/daemon/infrastructure/inbound/api/grpc/gen"
)

type inspectionServiceStub struct{}

func (inspectionServiceStub) SystemInfo(context.Context) (systemservice.SystemInfo, error) {
	return systemservice.SystemInfo{OS: "linux", DaemonPID: 42}, nil
}
func (inspectionServiceStub) Doctor(context.Context) (systemservice.DoctorReport, error) {
	return systemservice.DoctorReport{Healthy: false, Checks: []systemservice.Check{{Code: "TEST", Status: systemservice.StatusFail}}}, nil
}
func (inspectionServiceStub) Version(context.Context) systemservice.VersionInfo {
	return systemservice.VersionInfo{Version: "v1.0.0", APIVersion: "v1"}
}

func TestInspectionHandlerMapsResponses(t *testing.T) {
	handler := NewInspectionHandler(inspectionServiceStub{})
	info, err := handler.SystemInfo(context.Background(), &pb.SystemInfoRequest{})
	if err != nil || info.Os != "linux" || info.DaemonPid != 42 {
		t.Fatalf("system info = %#v, %v", info, err)
	}
	report, err := handler.Doctor(context.Background(), &pb.DoctorRequest{})
	if err != nil || report.Healthy || len(report.Checks) != 1 || report.Checks[0].Status != pb.DiagnosticStatus_DIAGNOSTIC_STATUS_FAIL {
		t.Fatalf("doctor = %#v, %v", report, err)
	}
	version, err := handler.Version(context.Background(), &pb.VersionRequest{})
	if err != nil || version.Version != "v1.0.0" || version.ApiVersion != "v1" {
		t.Fatalf("version = %#v, %v", version, err)
	}
}
