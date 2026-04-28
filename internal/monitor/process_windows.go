package monitor

import (
	"strings"

	"github.com/shirou/gopsutil/v4/process"
)

func listWindowsProcessJobs() ([]processJob, error) {
	pids, err := process.Pids()
	if err != nil {
		return nil, err
	}

	jobs := make([]processJob, 0)
	for _, pid := range pids {
		proc, err := process.NewProcess(pid)
		if err != nil {
			continue
		}

		envList, err := proc.Environ()
		if err != nil || len(envList) == 0 {
			continue
		}
		env := envListToMap(envList)

		jobID := strings.TrimSpace(ciLookup(env, "CI_JOB_ID"))
		if jobID == "" {
			continue
		}

		cmdline, _ := proc.Cmdline()
		jobs = append(jobs, processJob{
			PID:     int(pid),
			JobID:   jobID,
			Cmdline: cmdline,
			Env:     env,
		})
	}

	return jobs, nil
}
