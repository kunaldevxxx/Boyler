package system

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	systemservice "boyler/internal/daemon/application/system_service"
	"golang.org/x/sys/unix"
)

type Config struct {
	RuntimePath, ImagesPath, ContainersPath, ShimPath, StatePath string
}

type Inspector struct{ config Config }

func NewInspector(config Config) *Inspector { return &Inspector{config: config} }

func (i *Inspector) Info(ctx context.Context) (systemservice.SystemInfo, error) {
	if err := ctx.Err(); err != nil {
		return systemservice.SystemInfo{}, err
	}
	hostname, _ := os.Hostname()
	return systemservice.SystemInfo{
		OS: runtime.GOOS, Architecture: runtime.GOARCH,
		Kernel: readFile("/proc/sys/kernel/osrelease"), Hostname: hostname, GoVersion: runtime.Version(),
		CgroupVersion: cgroupVersion(), CgroupControllers: cgroupControllers(),
		OverlayFS: hasFilesystem("overlay"), Root: os.Geteuid() == 0,
		IPv4Forwarding: readFile("/proc/sys/net/ipv4/ip_forward") == "1",
		RuntimePath:    i.config.RuntimePath, ImagesPath: i.config.ImagesPath, ContainersPath: i.config.ContainersPath,
	}, nil
}

func (i *Inspector) Doctor(ctx context.Context) (systemservice.DoctorReport, error) {
	if err := ctx.Err(); err != nil {
		return systemservice.DoctorReport{}, err
	}
	checks := []systemservice.Check{
		check("HOST_NOT_LINUX", "kernel", "Linux host", runtime.GOOS == "linux", runtime.GOOS, "Boyler supports Linux only"),
		check("DAEMON_NOT_ROOT", "daemon", "Root privileges", os.Geteuid() == 0, fmt.Sprintf("effective UID %d", os.Geteuid()), "run boylerd as root"),
		check("CGROUP_V2_UNAVAILABLE", "cgroups", "Cgroups v2", cgroupVersion() == "v2", cgroupVersion(), "enable the unified cgroup v2 hierarchy"),
		check("CGROUP_CONTROLLERS_MISSING", "cgroups", "CPU and memory controllers", containsAll(cgroupControllers(), "cpu", "memory"), strings.Join(cgroupControllers(), ","), "enable cpu and memory cgroup controllers"),
		check("OVERLAYFS_UNAVAILABLE", "filesystem", "OverlayFS", hasFilesystem("overlay"), "kernel filesystem support", "enable the overlay kernel module"),
		check("IP_FORWARDING_DISABLED", "network", "IPv4 forwarding", readFile("/proc/sys/net/ipv4/ip_forward") == "1", readFile("/proc/sys/net/ipv4/ip_forward"), "set net.ipv4.ip_forward=1"),
		fileCheck("RUNTIME_NOT_EXECUTABLE", "runtime", "Runtime binary", i.config.RuntimePath, true),
		directoryCheck("IMAGE_STORAGE_UNAVAILABLE", "storage", "Images storage", i.config.ImagesPath),
		directoryCheck("CONTAINER_STORAGE_UNAVAILABLE", "storage", "Containers storage", i.config.ContainersPath),
	}
	if i.config.ShimPath == "" {
		checks = append(checks, systemservice.Check{Code: "SHIM_NOT_CONFIGURED", Component: "shim", Name: "Shim", Status: systemservice.StatusWarning, Detail: "not configured", Hint: "configure boyler-shim after its lifecycle integration"})
	} else {
		checks = append(checks, fileCheck("SHIM_UNHEALTHY", "shim", "Shim binary", i.config.ShimPath, true))
	}
	if i.config.StatePath == "" {
		checks = append(checks, systemservice.Check{Code: "PERSISTENT_STATE_DISABLED", Component: "storage", Name: "Persistent container state", Status: systemservice.StatusWarning, Detail: "in-memory repository", Hint: "configure persistent state before production use"})
	} else {
		checks = append(checks, directoryCheck("STATE_STORAGE_UNAVAILABLE", "storage", "Persistent state", i.config.StatePath))
	}
	healthy := true
	for _, item := range checks {
		if item.Status == systemservice.StatusFail {
			healthy = false
		}
	}
	return systemservice.DoctorReport{Healthy: healthy, Checks: checks}, nil
}

func check(code, component, name string, ok bool, detail, hint string) systemservice.Check {
	status := systemservice.StatusPass
	if !ok {
		status = systemservice.StatusFail
	} else {
		hint = ""
	}
	if detail == "" {
		detail = "unavailable"
	}
	return systemservice.Check{Code: code, Component: component, Name: name, Status: status, Detail: detail, Hint: hint}
}

func fileCheck(code, component, name, path string, executable bool) systemservice.Check {
	info, err := os.Lstat(path)
	ok := err == nil && info.Mode()&os.ModeSymlink == 0 && info.Mode().IsRegular() &&
		(!executable || info.Mode().Perm()&0111 != 0) && info.Mode().Perm()&0022 == 0
	detail := path
	if path == "" {
		detail = "not configured"
	} else if err != nil {
		detail = err.Error()
	}
	return check(code, component, name, ok, detail, "use a regular executable not writable by group/others; symbolic links are rejected")
}

func directoryCheck(code, component, name, path string) systemservice.Check {
	info, err := os.Lstat(path)
	ok := err == nil && info.Mode()&os.ModeSymlink == 0 && info.IsDir() && writableDirectory(path)
	detail := path
	if path == "" {
		detail = "not configured"
	} else if err != nil {
		detail = err.Error()
	}
	return check(code, component, name, ok, detail, "create the directory and grant boylerd write access")
}

func writableDirectory(path string) bool {
	return unix.Access(path, unix.W_OK|unix.X_OK) == nil
}

func cgroupVersion() string {
	if _, err := os.Stat("/sys/fs/cgroup/cgroup.controllers"); err == nil {
		return "v2"
	}
	if _, err := os.Stat("/sys/fs/cgroup"); err == nil {
		return "v1"
	}
	return "unavailable"
}

func cgroupControllers() []string {
	items := strings.Fields(readFile("/sys/fs/cgroup/cgroup.controllers"))
	sort.Strings(items)
	return items
}

func hasFilesystem(name string) bool {
	for _, field := range strings.Fields(readFile("/proc/filesystems")) {
		if field == name {
			return true
		}
	}
	return false
}

func readFile(path string) string {
	value, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(value))
}

func containsAll(values []string, expected ...string) bool {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		set[value] = struct{}{}
	}
	for _, value := range expected {
		if _, ok := set[value]; !ok {
			return false
		}
	}
	return true
}
