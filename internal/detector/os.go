package detector

import (
	"os"
	"runtime"
)

// OSInfo holds information about the current operating system.
type OSInfo struct {
	OS       string
	Arch     string
	Hostname string
}

// DetectOS returns information about the current operating system and architecture.
func DetectOS() OSInfo {
	hostname, _ := os.Hostname()
	return OSInfo{
		OS:       runtime.GOOS,
		Arch:     runtime.GOARCH,
		Hostname: hostname,
	}
}
