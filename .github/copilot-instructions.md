# Copilot Instructions — gl-runner-harvester

## Project Purpose

`gl-runner-harvester` is a Go binary that runs on a GitLab runner host OS in the context of the runner user. It monitors newly created GitLab CI/CD jobs and, when detected, harvests source code, environment variables, and context information from those jobs. It optionally scans harvested data for secrets.

## Guiding Principles

- **KISS** — Keep It Simple. Prefer the simplest solution that works. Do not over-engineer.
- **Testability** — Every non-trivial package must have accompanying unit tests. Use interfaces and dependency injection to keep code testable without external services.
- **Clean organization** — Code belongs in the most specific package that makes sense. No cross-cutting concerns dumped into `main` or `cmd`.
- **No unnecessary abstractions** — Only create helpers, interfaces, or abstractions when they serve an immediate need (e.g. testability or multiple implementations).

## Stack

| Concern | Library |
|---|---|
| CLI | `github.com/spf13/cobra` |
| Logging | `github.com/rs/zerolog` |
| Process inspection | `github.com/shirou/gopsutil/v4` |
| Docker monitoring | `github.com/docker/docker` (Docker SDK) |
| Secret scanning | `github.com/praetorian-inc/titus` + custom GitLab rules |
| Config parsing | `github.com/BurntSushi/toml` |

## Package Layout

```
cmd/                  Cobra CLI commands (root, harvest, version)
internal/
  detector/           Detect OS, executor type (shell/docker/k8s), and permissions
  harvester/          Copy source files and env vars; write job output JSON
  monitor/            Job discovery loop — process-based and Docker-based strategies
  scanner/            Secret scanning via titus + custom GitLab PAT rules
```

`internal/` packages must not import from `cmd/`. `cmd/` wires everything together.

## Coding Conventions

### Logging
Use zerolog structured logging throughout. Always use the chained field style:
```go
log.Info().Str("key", value).Int("count", n).Msg("description")
log.Warn().Err(err).Str("job_id", jobID).Msg("something went wrong")
```
Never use `fmt.Println` or `log` from the standard library.

### Interfaces and Dependency Injection
Define narrow interfaces at the point of use. Inject dependencies via constructor functions:
```go
type jobHarvester interface {
    HarvestJob(jobDir string) error
    HarvestProcess(jobID string, env map[string]string, cmdline string) error
}

func New(osInfo detector.OSInfo, execType detector.ExecutorType, interval int, h jobHarvester) *Monitor { ... }
```

### Cobra Commands
- One file per command in `cmd/` (e.g. `harvest.go`, `root.go`).
- Flags are registered in the `init()` function of each command file.
- Command logic lives in a named `run*` function (e.g. `runHarvest`) for testability.

### Error Handling
- Return errors from `RunE` in Cobra commands; do not call `os.Exit` directly inside command logic.
- Wrap errors with context using `fmt.Errorf("doing X: %w", err)`.

### Testing
- Place tests in `*_test.go` files in the same package.
- Use table-driven tests with named subtests (`t.Run`).
- Use interfaces to mock external dependencies (filesystem, Docker API, process list).
- Do not test `main()` directly; test the underlying logic functions.

## Code Quality Checks

Before committing or submitting changes:
1. Run `go fmt ./...` to ensure consistent formatting — the project enforces standard Go formatting.
2. Run `go test ./...` and verify **all tests pass**. Add tests for any new logic.
3. Ensure no new external dependencies are introduced without justification.
4. Check that code aligns with Guiding Principles and Coding Conventions.

## Threat Model Context

This tool is designed to run in the context of an attacker or red-teamer who has shell access on a GitLab runner host as the runner user. It is a reconnaissance tool. All code must be written with this adversarial context in mind.
