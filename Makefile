PLUGIN_NAME := copilot.so
BUILD_DIR := build
PROVIDER_DIR := $(HOME)/.config/elephant/providers

.PHONY: build install clean test

build:
	mkdir -p "$(BUILD_DIR)"
	go build -buildmode=plugin -o "$(BUILD_DIR)/$(PLUGIN_NAME)"

install: build
	mkdir -p "$(PROVIDER_DIR)"
	cp "$(BUILD_DIR)/$(PLUGIN_NAME)" "$(PROVIDER_DIR)/"

test:
	go test ./...

clean:
	rm -rf "$(BUILD_DIR)"
