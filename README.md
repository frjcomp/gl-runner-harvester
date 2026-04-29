# gl-runner-harvester

A reconnaissance tool for GitLab runner hosts that discovers and harvests CI/CD job artifacts, source code, registry images, secure files, and secrets.

## Purpose

`gl-runner-harvester` is designed for red-teamers and security researchers who have shell access to a GitLab runner host. It:

- **Detects** the GitLab runner executor type (shell, SSH, Docker, Kubernetes)
- **Monitors** active CI/CD job execution in real-time
- **Harvests** source code, environment variables, CI context, secure files, and registry images
- **Scans** harvested data for exposed secrets (PATs, runner tokens, API keys)

## Quick Start

Run the harvester on a runner host to collect all active job artifacts:

```bash
./gl-runner-harvester harvest --collection-path /tmp/gl-harvest --interval 2 --log-level info
```

This will:
1. Detect the runner executor type
2. Poll for active CI/CD jobs every 2 seconds
3. Collect job data into `/tmp/gl-harvest/<job_id>_<timestamp>/`
4. Scan harvested data for secrets
5. Log progress and findings at info level

Example output directory structure:
```
/tmp/gl-harvest/
└── 14136599304_20260429_072045/
    ├── source/
    ├── secure_files/
    ├── image/
    └── summary.json
```

## Installation

Install the latest Linux/macOS release with:

```bash
curl -fsSL https://frjcomp.github.io/gl-runner-harvester/install.sh | sh
```
You can also download binaries manually from [GitHub Releases](https://github.com/frjcomp/gl-runner-harvester/releases).
