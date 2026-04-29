package cmd

import (
	"os"
	"strings"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

var (
	logLevel string
	version  = "dev"

	getArgs      = func() []string { return os.Args[1:] }
	executeRoot  = func() error { return rootCmd.Execute() }
	exitWithCode = os.Exit
)

var rootCmd = &cobra.Command{
	Use:   "gl-runner-harvester",
	Short: "GitLab runner reconnaissance and secret harvesting tool",
	Long: `gl-runner-harvester detects GitLab CI runner configuration,
monitors CI/CD jobs, harvests source code when enabled,
and scans for secrets using pattern matching and titus.`,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		return configureLogging(logLevel)
	},
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the version",
	Run: func(cmd *cobra.Command, args []string) {
		log.Info().Str("version", version).Msg("gl-runner-harvester")
	},
}

func configureLogging(level string) error {
	writer := zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: "15:04:05"}
	log.Logger = zerolog.New(writer).With().Timestamp().Logger()

	normalizedLevel := strings.ToLower(level)
	switch normalizedLevel {
	case "trace":
		zerolog.SetGlobalLevel(zerolog.TraceLevel)
	case "debug":
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
	case "info":
		zerolog.SetGlobalLevel(zerolog.InfoLevel)
	case "warn", "warning":
		zerolog.SetGlobalLevel(zerolog.WarnLevel)
	case "error":
		zerolog.SetGlobalLevel(zerolog.ErrorLevel)
	default:
		zerolog.SetGlobalLevel(zerolog.InfoLevel)
		normalizedLevel = "info"
	}

	log.Info().Str("log_level", normalizedLevel).Msg("Log level configured")
	return nil
}

func Execute() {
	args := getArgs()
	if shouldDefaultToHarvest(args) {
		rootCmd.SetArgs(append([]string{"harvest"}, args...))
	}

	if err := executeRoot(); err != nil {
		exitWithCode(1)
	}
}

func shouldDefaultToHarvest(args []string) bool {
	if len(args) == 0 {
		return true
	}

	first := args[0]
	if first == "harvest" || first == "version" || first == "help" {
		return false
	}

	return strings.HasPrefix(first, "-")
}

func init() {
	rootCmd.PersistentFlags().StringVar(&logLevel, "log-level", "info", "Log level (trace, debug, info, warn, error)")
	rootCmd.AddCommand(versionCmd)
	rootCmd.AddCommand(harvestCmd)
}
