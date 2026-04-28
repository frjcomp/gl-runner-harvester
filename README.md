# gl-runner-harvester
A GitLab Runner Harvester

gl-runner-harvester detects GitLab CI runner configuration, monitors active CI/CD jobs on runner hosts, harvests source and environment data when enabled, and scans for exposed secrets with Titus.

## Releases

Pre-built binaries are published on every `v*` tag in [GitHub Releases](https://github.com/frjcomp/gl-runner-harvester/releases).

## Installation

Install the latest Linux/macOS release with:

```bash
curl -fsSL https://frjcomp.github.io/gl-runner-harvester/install.sh | sh
```

The published installer script is generated in the release workflow from `.goreleaser.yaml` using `binstaller` (it is not maintained manually in this repository).

Security warning: review the installation script before executing it, and do not pipe remote scripts into a privileged shell without verifying the source first.

You can also download binaries manually from [GitHub Releases](https://github.com/frjcomp/gl-runner-harvester/releases).
