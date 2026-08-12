package system_service

import (
	"context"
	"os"
	"runtime"
	"time"

	buildversion "boyler/internal/version"
)

type Status string

const (
	StatusPass    Status = "pass"
	StatusWarning Status = "warning"
	StatusFail    Status = "fail"
)

type Check struct {
	Code, Component, Name, Detail, Hint string
	Status                              Status
}

type SystemInfo struct {
	OS, Architecture, Kernel, Hostname, GoVersion string
	CgroupVersion, RuntimePath                    string
	CgroupControllers                             []string
	ImagesPath, ContainersPath, DaemonStartedAt   string
	OverlayFS, Root, IPv4Forwarding               bool
	DaemonUptimeSeconds                           int64
	DaemonPID                                     int32
}

type DoctorReport struct {
	Healthy bool
	Checks  []Check
}

type VersionInfo struct {
	Version, Commit, BuildDate, GoVersion string
	OS, Architecture                      string
	APIVersion, StateVersion              string
	RuntimeVersion                        string
}

type HostInspector interface {
	Info(context.Context) (SystemInfo, error)
	Doctor(context.Context) (DoctorReport, error)
}

type Service interface {
	SystemInfo(context.Context) (SystemInfo, error)
	Doctor(context.Context) (DoctorReport, error)
	Version(context.Context) VersionInfo
}

type service struct {
	inspector HostInspector
	startedAt time.Time
}

func New(inspector HostInspector, startedAt time.Time) Service {
	return &service{inspector: inspector, startedAt: startedAt}
}

func (s *service) SystemInfo(ctx context.Context) (SystemInfo, error) {
	info, err := s.inspector.Info(ctx)
	if err != nil {
		return SystemInfo{}, err
	}
	info.DaemonStartedAt = s.startedAt.UTC().Format(time.RFC3339)
	info.DaemonUptimeSeconds = max(0, int64(time.Since(s.startedAt)/time.Second))
	info.DaemonPID = int32(os.Getpid())
	return info, nil
}

func (s *service) Doctor(ctx context.Context) (DoctorReport, error) {
	return s.inspector.Doctor(ctx)
}

func (*service) Version(context.Context) VersionInfo {
	info := buildversion.Current(runtime.Version(), runtime.GOOS, runtime.GOARCH)
	return VersionInfo{
		Version: info.Version, Commit: info.Commit, BuildDate: info.BuildDate,
		GoVersion: info.GoVersion, OS: info.OS, Architecture: info.Arch,
		APIVersion: buildversion.APIVersion, StateVersion: buildversion.StateVersion, RuntimeVersion: buildversion.RuntimeVersion,
	}
}
