package grpc

import (
	"context"

	systemservice "boyler/internal/daemon/application/system_service"
	pb "boyler/internal/daemon/infrastructure/inbound/api/grpc/gen"
)

type InspectionHandler struct {
	pb.UnimplementedDaemonInspectionServiceServer
	service systemservice.Service
}

func NewInspectionHandler(service systemservice.Service) *InspectionHandler {
	return &InspectionHandler{service: service}
}

func (h *InspectionHandler) SystemInfo(ctx context.Context, _ *pb.SystemInfoRequest) (*pb.SystemInfoResponse, error) {
	info, err := h.service.SystemInfo(ctx)
	if err != nil {
		return nil, err
	}
	return &pb.SystemInfoResponse{
		Os: info.OS, Architecture: info.Architecture, Kernel: info.Kernel, Hostname: info.Hostname,
		GoVersion: info.GoVersion, CgroupVersion: info.CgroupVersion, CgroupControllers: info.CgroupControllers,
		OverlayFs: info.OverlayFS, Root: info.Root, Ipv4Forwarding: info.IPv4Forwarding,
		RuntimePath: info.RuntimePath, ImagesPath: info.ImagesPath, ContainersPath: info.ContainersPath,
		DaemonStartedAt: info.DaemonStartedAt, DaemonUptimeSeconds: info.DaemonUptimeSeconds,
		DaemonPid: info.DaemonPID,
	}, nil
}

func (h *InspectionHandler) Doctor(ctx context.Context, _ *pb.DoctorRequest) (*pb.DoctorResponse, error) {
	report, err := h.service.Doctor(ctx)
	if err != nil {
		return nil, err
	}
	checks := make([]*pb.DiagnosticCheck, 0, len(report.Checks))
	for _, item := range report.Checks {
		checks = append(checks, &pb.DiagnosticCheck{
			Code: item.Code, Component: item.Component, Name: item.Name,
			Status: diagnosticStatus(item.Status), Detail: item.Detail, Hint: item.Hint,
		})
	}
	return &pb.DoctorResponse{Healthy: report.Healthy, Checks: checks}, nil
}

func (h *InspectionHandler) Version(ctx context.Context, _ *pb.VersionRequest) (*pb.VersionResponse, error) {
	info := h.service.Version(ctx)
	return &pb.VersionResponse{
		Version: info.Version, Commit: info.Commit, BuildDate: info.BuildDate, GoVersion: info.GoVersion,
		Os: info.OS, Architecture: info.Architecture, ApiVersion: info.APIVersion,
		StateVersion: info.StateVersion, RuntimeVersion: info.RuntimeVersion,
	}, nil
}

func diagnosticStatus(status systemservice.Status) pb.DiagnosticStatus {
	switch status {
	case systemservice.StatusPass:
		return pb.DiagnosticStatus_DIAGNOSTIC_STATUS_PASS
	case systemservice.StatusWarning:
		return pb.DiagnosticStatus_DIAGNOSTIC_STATUS_WARNING
	case systemservice.StatusFail:
		return pb.DiagnosticStatus_DIAGNOSTIC_STATUS_FAIL
	default:
		return pb.DiagnosticStatus_DIAGNOSTIC_STATUS_UNSPECIFIED
	}
}
