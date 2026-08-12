package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"text/template"
	"time"

	"boyler/cmd/boyler/cmd/ui"
	pb "boyler/internal/daemon/infrastructure/inbound/api/grpc/gen"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func commandError(err error) error {
	if status.Code(err) == codes.Unavailable {
		socket := os.Getenv("UNIX_SOCKET")
		if socket == "" {
			return fmt.Errorf("cannot connect to Boyler daemon\n\n  Configuration: UNIX_SOCKET is not set\n  Hint:          provide it in the environment or Boyler .env file")
		}
		return fmt.Errorf("cannot connect to Boyler daemon\n\n  Socket: unix://%s\n  Hint:   start it with 'boyler init'", socket)
	}
	if _, ok := status.FromError(err); !ok {
		return err
	}
	return fmt.Errorf("daemon: %s", status.Convert(err).Message())
}

func printActionResult(output io.Writer, action, id string) {
	theme := ui.NewTheme(output, colorMode.value)
	if !theme.Terminal() {
		fmt.Fprintln(output, id)
		return
	}
	fmt.Fprintf(output, "%s %s  %s\n", theme.Success(theme.Symbol("✓", "+")), theme.Success(action), theme.Muted(id))
}

func printSuccess(output io.Writer, message string) {
	theme := ui.NewTheme(output, colorMode.value)
	if !theme.Terminal() {
		fmt.Fprintln(output, message)
		return
	}
	fmt.Fprintf(output, "%s %s\n", theme.Success(theme.Symbol("✓", "+")), theme.Success(message))
}

func printWarning(output io.Writer, message string) {
	theme := ui.NewTheme(output, colorMode.value)
	fmt.Fprintf(output, "%s %s\n", theme.Warning(theme.Symbol("!", "Warning:")), message)
}

func printFailure(output io.Writer, err error) {
	theme := ui.NewTheme(output, colorMode.value)
	fmt.Fprintf(output, "%s %s\n", theme.Error(theme.Symbol("✗", "Error:")), err)
}

func shortContainerID(id string) string {
	if len(id) <= 12 {
		return id
	}
	return id[:12]
}

func relativeTime(value string, now time.Time) string {
	created, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return value
	}

	d := now.Sub(created)
	if d < 0 {
		d = 0
	}
	seconds := int64(d / time.Second)
	switch {
	case seconds < 1:
		return "Less than a second ago"
	case seconds < 60:
		return pluralAge(seconds, "second")
	case seconds < 3600:
		return pluralAge(seconds/60, "minute")
	case seconds < 86400:
		return pluralAge(seconds/3600, "hour")
	default:
		return pluralAge(seconds/86400, "day")
	}
}

func pluralAge(value int64, unit string) string {
	if value != 1 {
		unit += "s"
	}
	return fmt.Sprintf("%d %s ago", value, unit)
}

func displayStatus(value string) string {
	switch strings.ToLower(value) {
	case "running":
		return "Up"
	case "pause", "paused", "freeze", "frozen":
		return "Up (Paused)"
	case "stopped", "deleted", "exited":
		return "Exited"
	default:
		return value
	}
}

type inspectOutput struct {
	Id         string            `json:"Id"`
	Created    string            `json:"Created"`
	State      inspectState      `json:"State"`
	Image      string            `json:"Image"`
	Config     inspectConfig     `json:"Config"`
	HostConfig inspectHostConfig `json:"HostConfig"`
}

type inspectState struct {
	Status    string `json:"Status"`
	Running   bool   `json:"Running"`
	Paused    bool   `json:"Paused"`
	Pid       int32  `json:"Pid"`
	StartedAt string `json:"StartedAt"`
}

type inspectConfig struct {
	Hostname string   `json:"Hostname"`
	Env      []string `json:"Env"`
	Cmd      []string `json:"Cmd"`
}

type inspectHostConfig struct {
	Memory     int64  `json:"Memory"`
	CPUShares  uint64 `json:"CpuShares"`
	CPUQuota   int64  `json:"CpuQuota"`
	CPUPeriod  uint64 `json:"CpuPeriod"`
	CpusetCPUs string `json:"CpusetCpus"`
	CpusetMems string `json:"CpusetMems"`
}

func printInspect(w io.Writer, response *pb.InspectResponse) error {
	output := inspectView(response)
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "    ")
	return encoder.Encode([]inspectOutput{output})
}

func printInspectTemplate(w io.Writer, response *pb.InspectResponse, format string) error {
	output := inspectView(response)
	if format == "json" {
		return json.NewEncoder(w).Encode(output)
	}

	tmpl, err := newOutputTemplate("inspect", format)
	if err != nil {
		return fmt.Errorf("invalid inspect format: %w", err)
	}
	if err := tmpl.Execute(w, output); err != nil {
		return fmt.Errorf("execute inspect format: %w", err)
	}
	_, err = fmt.Fprintln(w)
	return err
}

func newOutputTemplate(name, format string) (*template.Template, error) {
	return template.New(name).Funcs(template.FuncMap{
		"json": func(value any) (string, error) {
			data, err := json.Marshal(value)
			return string(data), err
		},
		"join":  strings.Join,
		"lower": strings.ToLower,
		"upper": strings.ToUpper,
	}).Parse(format)
}

func inspectView(response *pb.InspectResponse) inspectOutput {
	statusValue := strings.ToLower(response.GetStatus())
	resources := response.GetResources()
	var memory int64
	var cpuShares uint64
	var cpuQuota int64
	var cpuPeriod uint64
	var cpusetCPUs string
	var cpusetMems string
	if resources != nil {
		if resources.Memory != nil {
			memory = resources.Memory.Max
		}
		if resources.Cpu != nil {
			cpuShares = resources.Cpu.Weight
			cpuQuota = resources.Cpu.Quota
			cpuPeriod = resources.Cpu.Period
			cpusetCPUs = resources.Cpu.Cpus
			cpusetMems = resources.Cpu.Mems
		}
	}

	return inspectOutput{
		Id:      response.GetContainerId(),
		Created: response.GetCreatedAt(),
		State: inspectState{
			Status:    statusValue,
			Running:   statusValue == "running",
			Paused:    statusValue == "pause" || statusValue == "paused" || statusValue == "freeze",
			Pid:       response.GetPid(),
			StartedAt: response.GetStartedAt(),
		},
		Image: response.GetImageId(),
		Config: inspectConfig{
			Hostname: response.GetHostname(),
			Env:      response.GetEnv(),
			Cmd:      response.GetArgs(),
		},
		HostConfig: inspectHostConfig{
			Memory:     memory,
			CPUShares:  cpuShares,
			CPUQuota:   cpuQuota,
			CPUPeriod:  cpuPeriod,
			CpusetCPUs: cpusetCPUs,
			CpusetMems: cpusetMems,
		},
	}
}
