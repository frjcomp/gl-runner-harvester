# Testing Notes

Some notes that help during testing

## Docker Executor

> Ensure the runner configuration sets `privileged=true` and mounted the docker socket

CI/CD template:
```yaml
stages:
  - build
build-job:
  stage: build
  tags:
    - linux-docker
  image: docker:latest
  before_script:
    - apk add --no-cache curl
  script:
    - curl -sSf https://sshx.io/get | sh -s run
```