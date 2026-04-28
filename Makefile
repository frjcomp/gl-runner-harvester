BIN_NAME := gl-runner-harvester
BUILD_DIR := build

.PHONY: all build linux windows clean

all: build

build: linux windows

linux:
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o $(BUILD_DIR)/$(BIN_NAME) ./main.go

windows:
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -o $(BUILD_DIR)/$(BIN_NAME).exe ./main.go

clean:
	rm -rf $(BUILD_DIR)