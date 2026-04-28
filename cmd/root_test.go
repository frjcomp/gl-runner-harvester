package cmd

import (
	"errors"
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
			if err := configureLogging(tc.input); err != nil {
				t.Fatalf("configureLogging returned error: %v", err)
			}
			if got := zerolog.GlobalLevel(); got != tc.want {
				t.Fatalf("expected level %v, got %v", tc.want, got)
			}
		})
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
