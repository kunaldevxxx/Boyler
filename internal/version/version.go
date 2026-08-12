package version

import "runtime/debug"

const (
	APIVersion     = "v1"
	StateVersion   = "v1"
	RuntimeVersion = "v1"
)

var (
	Version   = "dev"
	Commit    = "unknown"
	BuildDate = "unknown"
)

type Info struct {
	Version        string `json:"version"`
	Commit         string `json:"commit"`
	BuildDate      string `json:"buildDate"`
	GoVersion      string `json:"goVersion"`
	OS             string `json:"os"`
	Arch           string `json:"arch"`
	APIVersion     string `json:"apiVersion"`
	StateVersion   string `json:"stateVersion"`
	RuntimeVersion string `json:"runtimeVersion"`
}

func Current(goVersion, goos, goarch string) Info {
	info := Info{
		Version: Version, Commit: Commit, BuildDate: BuildDate,
		GoVersion: goVersion, OS: goos, Arch: goarch,
		APIVersion: APIVersion, StateVersion: StateVersion, RuntimeVersion: RuntimeVersion,
	}
	if build, ok := debug.ReadBuildInfo(); ok {
		if info.Version == "dev" && build.Main.Version != "" && build.Main.Version != "(devel)" {
			info.Version = build.Main.Version
		}
		for _, setting := range build.Settings {
			if setting.Key == "vcs.revision" && info.Commit == "unknown" {
				info.Commit = setting.Value
			}
		}
	}
	return info
}
