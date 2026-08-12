package system_service

import (
	"context"
	"testing"
	"time"
)

type fakeInspector struct {
	info   SystemInfo
	report DoctorReport
}

func (f fakeInspector) Info(context.Context) (SystemInfo, error)     { return f.info, nil }
func (f fakeInspector) Doctor(context.Context) (DoctorReport, error) { return f.report, nil }

func TestServiceAddsDaemonRuntimeInformation(t *testing.T) {
	started := time.Now().Add(-2 * time.Second)
	service := New(fakeInspector{info: SystemInfo{OS: "linux"}}, started)
	info, err := service.SystemInfo(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.OS != "linux" || info.DaemonPID <= 0 || info.DaemonStartedAt == "" || info.DaemonUptimeSeconds < 1 {
		t.Fatalf("system info = %#v", info)
	}
	version := service.Version(context.Background())
	if version.APIVersion == "" || version.StateVersion == "" || version.RuntimeVersion == "" {
		t.Fatalf("version = %#v", version)
	}
}

func TestServicePreservesDoctorReport(t *testing.T) {
	expected := DoctorReport{Healthy: false, Checks: []Check{{Code: "BROKEN", Status: StatusFail}}}
	actual, err := New(fakeInspector{report: expected}, time.Now()).Doctor(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if actual.Healthy || len(actual.Checks) != 1 || actual.Checks[0].Code != "BROKEN" {
		t.Fatalf("report = %#v", actual)
	}
}
