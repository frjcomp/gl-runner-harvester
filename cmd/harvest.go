package cmd

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/frjcomp/gl-runner-harvester/internal/detector"
	"github.com/frjcomp/gl-runner-harvester/internal/harvester"
	"github.com/frjcomp/gl-runner-harvester/internal/monitor"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

var (
	outputDir      string
	collectionPath string
	runnerConfig   string
	exectuor       string
	interval       int
	scanSecrets    bool
	gitlabURL      string
	noHarvestFiles bool
)

var harvestCmd = &cobra.Command{
	Use:   "harvest",
	Short: "Start harvesting CI/CD jobs from GitLab runners",
	Long: `harvest detects the runner environment, monitors for active CI/CD jobs,
copies source code when enabled, and optionally scans for secrets.`,
	RunE: runHarvest,
}

func init() {
	harvestCmd.Flags().StringVar(&collectionPath, "collection-path", "/tmp/gl-harvest", "Directory to store harvested data")
	harvestCmd.Flags().StringVar(&outputDir, "output-dir", "", "Deprecated alias for --collection-path")
	harvestCmd.Flags().StringVar(&runnerConfig, "runner-config", "", "Path to GitLab runner config.toml (auto-detected if not specified)")
	harvestCmd.Flags().StringVar(&exectuor, "exectuor", "", "Manually set executor type (shell, ssh, docker, kubernetes)")
	harvestCmd.Flags().IntVar(&interval, "interval", 5, "Polling interval in seconds")
	harvestCmd.Flags().BoolVar(&scanSecrets, "scan", true, "Enable secret scanning on harvested data")
	harvestCmd.Flags().BoolVar(&noHarvestFiles, "no-harvest-files", false, "Do not copy or write harvested files; scan source/env in place and only emit logs")
	harvestCmd.Flags().StringVar(&gitlabURL, "gitlab-url", "https://gitlab.com", "GitLab base URL used to verify GitLab PAT findings")
}

func runHarvest(cmd *cobra.Command, args []string) error {
	// 1. Detect OS info
	osInfo := detector.DetectOS()
	log.Info().
		Str("os", osInfo.OS).
		Str("arch", osInfo.Arch).
		Str("hostname", osInfo.Hostname).
		Msg("Detected OS info")

	// 2. Detect executor type
	execType, execMeta := detector.DetectExecutor(runnerConfig)
	if exectuor != "" {
		manualExecType, err := parseManualExecutor(exectuor)
		if err != nil {
			return err
		}
		execType = manualExecType
		execMeta = map[string]string{
			"source":         "manual_flag",
			"executor_value": string(manualExecType),
		}
	}
	log.Info().
		Str("executor", string(execType)).
		Interface("metadata", execMeta).
		Msg("Detected executor type")

	// 3. Check permissions
	permInfo := detector.CheckPermissions(osInfo.OS)
	log.Info().
		Bool("is_privileged", permInfo.IsPrivileged).
		Str("username", permInfo.Username).
		Bool("runner_writable", permInfo.RunnerBinaryWritable).
		Str("runner_path", permInfo.RunnerBinaryPath).
		Msg("Permission check")

	// 4. Print detection summary
	printDetectionSummary(osInfo, execType, permInfo)

	normalizedGitLabURL, err := normalizeGitLabURL(gitlabURL)
	if err != nil {
		return err
	}

	collectPath := strings.TrimSpace(collectionPath)
	if legacy := strings.TrimSpace(outputDir); legacy != "" {
		collectPath = legacy
	}

	// 5. Create harvester
	h := harvester.New(collectPath, scanSecrets, normalizedGitLabURL, !noHarvestFiles)

	// 6. Start monitoring loop
	m := monitor.New(osInfo, execType, interval, h)
	log.Info().Int("interval_seconds", interval).Str("collection_path", collectPath).Bool("harvest_files", !noHarvestFiles).Msg("Starting monitor")
	return m.Start()
}

func normalizeGitLabURL(raw string) (string, error) {
	v := strings.TrimSpace(raw)
	if v == "" {
		return "", fmt.Errorf("--gitlab-url cannot be empty")
	}
	if !strings.Contains(v, "://") {
		v = "https://" + v
	}

	u, err := url.Parse(v)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("invalid --gitlab-url value %q", raw)
	}

	u.RawQuery = ""
	u.Fragment = ""
	u.Path = strings.TrimRight(u.Path, "/")

	return strings.TrimRight(u.String(), "/"), nil
}

func printDetectionSummary(osInfo detector.OSInfo, execType detector.ExecutorType, permInfo detector.PermissionInfo) {
	log.Info().Msg("=== Detection Summary ===")

	switch execType {
	case detector.Shell, detector.SSH:
		if osInfo.OS == "windows" {
			log.Info().Msg("Windows shell executor: checking binary writability and custom users")
		} else {
			log.Info().Msg("Linux/macOS shell executor: checking root or custom user context")
		}
	case detector.Docker:
		log.Info().Msg("Docker executor: will monitor build tmp directories")
	case detector.Kubernetes:
		log.Info().Msg("Kubernetes executor: will monitor build directories and env vars")
	default:
		log.Warn().Msg("Unknown executor type; attempting generic monitoring")
	}

	if permInfo.IsPrivileged {
		log.Warn().Msg("Running with elevated privileges — full system access possible")
	}

	if osInfo.OS == "windows" && permInfo.RunnerBinaryWritable {
		log.Warn().
			Str("runner_path", permInfo.RunnerBinaryPath).
			Msg("Writable gitlab-runner service binary detected; this can be abused for privilege escalation and lateral movement (https://docs.gitlab.com/runner/install/windows/)")
	}
}

func parseManualExecutor(v string) (detector.ExecutorType, error) {
	normalized := strings.ToLower(strings.TrimSpace(v))
	switch normalized {
	case string(detector.Shell):
		return detector.Shell, nil
	case string(detector.SSH):
		return detector.SSH, nil
	case string(detector.Docker):
		return detector.Docker, nil
	case string(detector.Kubernetes):
		return detector.Kubernetes, nil
	default:
		return detector.Unknown, fmt.Errorf("invalid --exectuor value %q (supported: shell, ssh, docker, kubernetes)", v)
	}
}
