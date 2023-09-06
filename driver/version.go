package driver

import (
	"encoding/json"
	"fmt"
	"runtime"

	"github.com/exoscale/exoscale-csi-driver/cmd/exoscale-csi-driver/buildinfo"
)

// VersionInfo represents the current running version
type VersionInfo struct {
	DriverVersion string `json:"driverVersion"`
	GitCommit     string `json:"gitCommit"`
	BuildDate     string `json:"buildDate"`
	GoVersion     string `json:"goVersion"`
	Compiler      string `json:"compiler"`
	Platform      string `json:"platform"`
}

// GetVersion returns the current running version
func GetVersion() VersionInfo {
	return VersionInfo{
		DriverVersion: buildinfo.Version,
		GitCommit:     buildinfo.GitCommit,
		BuildDate:     buildinfo.BuildDate,
		GoVersion:     runtime.Version(),
		Compiler:      runtime.Compiler,
		Platform:      fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH),
	}
}

// GetVersionJSON returns the current running version in JSON
func GetVersionJSON() (string, error) {
	info := GetVersion()
	marshalled, err := json.MarshalIndent(&info, "", "  ")
	if err != nil {
		return "", err
	}
	return string(marshalled), nil
}
