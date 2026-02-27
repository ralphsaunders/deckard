BINARY      := deckard
INSTALL_DIR := $(HOME)/.local/bin
BUILD_FLAGS := -ldflags="-s -w"

.PHONY: build dev install

build:
	go build $(BUILD_FLAGS) -o $(BINARY) .

dev:
	$(shell go env GOPATH)/bin/air

install: build
	mkdir -p $(INSTALL_DIR)
	cp $(BINARY) $(INSTALL_DIR)/$(BINARY)
