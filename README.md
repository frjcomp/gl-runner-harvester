# gl-runner-harvester

A reconnaissance tool for GitLab runner hosts that discovers and harvests CI/CD job artifacts, source code, and secrets.

## Purpose

`gl-runner-harvester` is designed for red-teamers and security researchers who have shell access to a GitLab runner host. It:

- **Detects** the GitLab runner executor type (shell, SSH, Docker, Kubernetes)
- **Monitors** active CI/CD job execution in real-time
- **Harvests** source code, environment variables, and CI context
- **Scans** harvested data for exposed secrets (PATs, runner tokens, API keys)
- **Supports** both host-accessible build directories and Docker container extraction

## Quick Start

Run the harvester on a runner host to collect all active job artifacts:

```bash
./gl-runner-harvester harvest --collection-path /tmp/gl-harvest --interval 2 --log-level info
```

This will:
1. Detect the runner executor type
2. Poll for active CI/CD jobs every 2 seconds
3. Extract source and environment data to `/tmp/gl-harvest/<job_id>_<timestamp>/`
4. Scan for secrets in harvested data
5. Log progress and findings at info level

Example output directory structure:
```
/tmp/gl-harvest/
└── 14136599304_20260429_072045/
    ├── source/                    # Job source code
    │   ├── .git/
    │   ├── .gitlab-ci.yml
    │   └── README.md
    ├── env_vars.json              # All environment variables
    ├── summary.json               # Job metadata and findings
    └── scan_findings.json         # Secret scan results (if enabled)
```

Available flags:
- `--executor` — Manually set executor type (shell, ssh, docker, kubernetes)
- `--collection-path` — Directory to store harvested data (default: `/tmp/gl-harvest`)
- `--runner-config` — Path to GitLab runner config.toml (auto-detected if not specified)
- `--interval` — Polling interval in seconds (default: 5)
- `--scan` — Enable secret scanning (default: true)
- `--log-level` — Set verbosity: trace, debug, info, warn, error (default: info)
- `--no-harvest-files` — Scan in-place only; do not write files to disk

## Releases

Pre-built binaries are published on every `v*` tag in [GitHub Releases](https://github.com/frjcomp/gl-runner-harvester/releases).

## Installation

Install the latest Linux/macOS release with:

```bash
curl -fsSL https://frjcomp.github.io/gl-runner-harvester/install.sh | sh
```

The published installer script is generated in the release workflow from `.goreleaser.yaml` using `binstiller` (it is not maintained manually in this repository).

Security warning: review the installation script before executing it, and do not pipe remote scripts into a privileged shell without verifying the source first.

You can also download binaries manually from [GitHub Releases](https://github.com/frjcomp/gl-runner-harvester/releases).
