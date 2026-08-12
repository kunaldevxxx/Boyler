package cmd

import (
	"bytes"
	"strings"
	"testing"
	"time"

	pb "boyler/internal/daemon/infrastructure/inbound/api/grpc/gen"
)

func TestPrintContainersDockerStyle(t *testing.T) {
	now := time.Date(2026, time.August, 9, 15, 0, 0, 0, time.UTC)
	response := &pb.PsResponse{Containers: []*pb.ContainerListItem{{
		ContainerId: "8e6ff240a1a54642a58e1a53b3222ee0",
		Image:       "alpine_latest",
		Command:     "/bin/sh",
		Created:     now.Add(-12 * time.Minute).Format(time.RFC3339),
		Status:      "running",
		Name:        "eager_turing",
	}}}

	var output bytes.Buffer
	printContainers(&output, response, now)

	for _, expected := range []string{
		"CONTAINER ID",
		"8e6ff240a1a5",
		`"/bin/sh"`,
		"12 minutes ago",
		"Up",
		"eager_turing",
	} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("output %q does not contain %q", output.String(), expected)
		}
	}
}

func TestPrintInspectDockerStyle(t *testing.T) {
	response := inspectTestResponse()

	var output bytes.Buffer
	if err := printInspect(&output, response); err != nil {
		t.Fatalf("printInspect returned an error: %v", err)
	}

	for _, expected := range []string{
		`"Id": "container-id"`,
		`"State": {`,
		`"Running": true`,
		`"Config": {`,
		`"HostConfig": {`,
		`"Memory": 536870912`,
	} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("output %q does not contain %q", output.String(), expected)
		}
	}
}

func TestPrintInspectTemplate(t *testing.T) {
	var output bytes.Buffer
	format := `{{.Id}} {{.State.Status}} {{.State.Pid}} {{join .Config.Env ","}}`
	if err := printInspectTemplate(&output, inspectTestResponse(), format); err != nil {
		t.Fatalf("printInspectTemplate returned an error: %v", err)
	}
	want := "container-id running 42 APP_ENV=production\n"
	if output.String() != want {
		t.Fatalf("template output = %q, want %q", output.String(), want)
	}
}

func inspectTestResponse() *pb.InspectResponse {
	return &pb.InspectResponse{
		ContainerId: "container-id",
		Status:      "running",
		Pid:         42,
		ImageId:     "alpine_latest",
		CreatedAt:   "2026-08-09T14:20:10Z",
		StartedAt:   "2026-08-09T14:20:11Z",
		Hostname:    "boyler",
		Env:         []string{"APP_ENV=production"},
		Args:        []string{"/bin/sh"},
		Resources: &pb.ResourceLimits{
			Memory: &pb.MemoryRestriction{Max: 536870912, Exist: true},
			Cpu:    &pb.CPURestriction{Weight: 1024, Period: 100000},
		},
	}
}

func TestPrintContainersQuietAndNoTrunc(t *testing.T) {
	response := psTestResponse()
	var output bytes.Buffer
	if err := printContainersWithOptions(&output, response, time.Now(), psOptions{quiet: true, noTrunc: true}); err != nil {
		t.Fatalf("printContainersWithOptions returned an error: %v", err)
	}
	if output.String() != "8e6ff240a1a54642a58e1a53b3222ee0\nf39a2c177d0249afb412233445566778\n" {
		t.Fatalf("unexpected quiet output: %q", output.String())
	}
}

func TestPrintContainersTemplate(t *testing.T) {
	var output bytes.Buffer
	err := printContainersWithOptions(&output, psTestResponse(), time.Now(), psOptions{
		filters: []string{"status=running"},
		format:  `{{.ID}} {{.Names}}`,
	})
	if err != nil {
		t.Fatalf("printContainersWithOptions returned an error: %v", err)
	}
	if output.String() != "8e6ff240a1a5 eager_turing\n" {
		t.Fatalf("unexpected template output: %q", output.String())
	}
}

func TestFilterContainersUsesAndAcrossKeysAndOrWithinKey(t *testing.T) {
	containers, err := filterContainers(psTestResponse().GetContainers(), []string{
		"status=running",
		"status=paused",
		"image=alpine",
	})
	if err != nil {
		t.Fatalf("filterContainers returned an error: %v", err)
	}
	if len(containers) != 1 || containers[0].GetName() != "eager_turing" {
		t.Fatalf("unexpected filtered containers: %#v", containers)
	}
}

func TestFilterContainersRejectsUnknownFilter(t *testing.T) {
	_, err := filterContainers(psTestResponse().GetContainers(), []string{"label=app"})
	if err == nil {
		t.Fatal("expected unsupported filter error")
	}
}

func psTestResponse() *pb.PsResponse {
	return &pb.PsResponse{Containers: []*pb.ContainerListItem{
		{
			ContainerId: "8e6ff240a1a54642a58e1a53b3222ee0",
			Image:       "alpine_latest",
			Command:     "/bin/sh",
			Created:     "2026-08-09T14:48:00Z",
			Status:      "running",
			Name:        "eager_turing",
		},
		{
			ContainerId: "f39a2c177d0249afb412233445566778",
			Image:       "busybox_latest",
			Command:     "/bin/sleep 1000",
			Created:     "2026-08-09T13:00:00Z",
			Status:      "stopped",
			Name:        "busy_bardeen",
		},
	}}
}

func TestPullReference(t *testing.T) {
	repository, tag, canonical := pullReference("alpine")
	if repository != "library/alpine" || tag != "latest" || canonical != "library/alpine:latest" {
		t.Fatalf("unexpected reference: %q %q %q", repository, tag, canonical)
	}

	repository, tag, canonical = pullReference("docker.io/acme/api:v2")
	if repository != "acme/api" || tag != "v2" || canonical != "acme/api:v2" {
		t.Fatalf("unexpected tagged reference: %q %q %q", repository, tag, canonical)
	}
}
