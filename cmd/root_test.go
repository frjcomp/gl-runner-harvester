package cmd

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rs/zerolog"
)

func TestConfigureLoggingLevels(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  zerolog.Level
	}{
		{name: "trace", input: "trace", want: zerolog.TraceLevel},
		{name: "debug", input: "DEBUG", want: zerolog.DebugLevel},
		{name: "info", input: "info", want: zerolog.InfoLevel},
		{name: "warn alias", input: "warning", want: zerolog.WarnLevel},
		{name: "error", input: "error", want: zerolog.ErrorLevel},
		{name: "unknown defaults info", input: "nope", want: zerolog.InfoLevel},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := configureLogging(tc.input, ""); err != nil {
				t.Fatalf("configureLogging returned error: %v", err)
			}
			if got := zerolog.GlobalLevel(); got != tc.want {
				t.Fatalf("expected level %v, got %v", tc.want, got)
			}
		})
	}
}

func TestConfigureLoggingWritesPlainTextFile(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "harvester.log")

	if err := configureLogging("info", logPath); err != nil {
		t.Fatalf("configureLogging returned error: %v", err)
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, "Log level configured") {
		t.Fatalf("expected configuration log entry in file, got %q", content)
	}
	if strings.Contains(content, "\x1b[") {
		t.Fatalf("expected file logs without ANSI color codes, got %q", content)
	}
}

func TestRootPersistentFlagShorthandsPresent(t *testing.T) {
	tests := map[string]string{
		"log-level": "L",
		"log":       "l",
	}

	for name, want := range tests {
		flag := rootCmd.PersistentFlags().Lookup(name)
		if flag == nil {
			t.Fatalf("expected persistent flag %q to exist", name)
		}
		if flag.Shorthand != want {
			t.Fatalf("expected shorthand %q for flag %q, got %q", want, name, flag.Shorthand)
		}
	}
}

func TestShouldDefaultToHarvest(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want bool
	}{
		{name: "no args", args: nil, want: true},
		{name: "harvest command", args: []string{"harvest"}, want: false},
		{name: "version command", args: []string{"version"}, want: false},
		{name: "help command", args: []string{"help"}, want: false},
		{name: "flag only", args: []string{"--log-level", "debug"}, want: true},
		{name: "unknown command", args: []string{"other"}, want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldDefaultToHarvest(tc.args); got != tc.want {
				t.Fatalf("expected %v, got %v", tc.want, got)
			}
		})
	}
}

func TestExecuteSuccess(t *testing.T) {
	oldGetArgs, oldExecuteRoot, oldExit := getArgs, executeRoot, exitWithCode
	defer func() {
		getArgs = oldGetArgs
		executeRoot = oldExecuteRoot
		exitWithCode = oldExit
	}()

	called := false
	getArgs = func() []string { return []string{"--log-level", "debug"} }
	executeRoot = func() error {
		called = true
		return nil
	}
	exitWithCode = func(code int) {
		t.Fatalf("did not expect exit, got code %d", code)
	}

	Execute()

	if !called {
		t.Fatalf("expected executeRoot to be called")
	}
}

func TestExecuteFailureExits(t *testing.T) {
	oldGetArgs, oldExecuteRoot, oldExit := getArgs, executeRoot, exitWithCode
	defer func() {
		getArgs = oldGetArgs
		executeRoot = oldExecuteRoot
		exitWithCode = oldExit
	}()

	getArgs = func() []string { return []string{"version"} }
	executeRoot = func() error { return errors.New("boom") }

	gotCode := -1
	exitWithCode = func(code int) { gotCode = code }

	Execute()

	if gotCode != 1 {
		t.Fatalf("expected exit code 1, got %d", gotCode)
	}
}
