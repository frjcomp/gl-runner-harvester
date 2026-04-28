//go:build !windows

package monitor

import "fmt"

func listWindowsProcessJobs() ([]processJob, error) {
	return nil, fmt.Errorf("windows process listing not available on this platform")
}
