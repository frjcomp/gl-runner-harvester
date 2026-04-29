package harvester

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/shirou/gopsutil/v4/disk"
)

func diskUsedPercent(path string) (float64, error) {
	targetPath := nearestExistingPath(path)
	usage, err := disk.Usage(targetPath)
	if err != nil {
		return 0, fmt.Errorf("get disk usage for %q: %w", targetPath, err)
	}
	return usage.UsedPercent, nil
}

func nearestExistingPath(path string) string {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return "."
	}

	current := filepath.Clean(trimmed)
	for {
		if _, err := os.Stat(current); err == nil {
			return current
		}

		parent := filepath.Dir(current)
		if parent == current {
			return "."
		}
		current = parent
	}
}
