BINARY := trasmetto
CMD := ./cmd/trasmetto
DIST_DIR := dist
GOCACHE ?= /tmp/go-cache
BUILD_ENV := CGO_ENABLED=0 GOCACHE=$(GOCACHE)
BUILD_FLAGS := -trimpath -buildvcs=false -ldflags="-s -w"

.PHONY: build build-all build-linux build-windows build-macos test run clean

build:
	$(BUILD_ENV) go build $(BUILD_FLAGS) -o $(BINARY) $(CMD)

build-all: build-linux build-windows build-macos

build-linux:
	mkdir -p $(DIST_DIR)
	$(BUILD_ENV) GOOS=linux GOARCH=amd64 go build $(BUILD_FLAGS) -o $(DIST_DIR)/$(BINARY)-linux-amd64 $(CMD)
	$(BUILD_ENV) GOOS=linux GOARCH=arm64 go build $(BUILD_FLAGS) -o $(DIST_DIR)/$(BINARY)-linux-arm64 $(CMD)

build-windows:
	mkdir -p $(DIST_DIR)
	$(BUILD_ENV) GOOS=windows GOARCH=amd64 go build $(BUILD_FLAGS) -o $(DIST_DIR)/$(BINARY)-windows-amd64.exe $(CMD)
	$(BUILD_ENV) GOOS=windows GOARCH=arm64 go build $(BUILD_FLAGS) -o $(DIST_DIR)/$(BINARY)-windows-arm64.exe $(CMD)

build-macos:
	mkdir -p $(DIST_DIR)
	$(BUILD_ENV) GOOS=darwin GOARCH=amd64 go build $(BUILD_FLAGS) -o $(DIST_DIR)/$(BINARY)-macos-amd64 $(CMD)
	$(BUILD_ENV) GOOS=darwin GOARCH=arm64 go build $(BUILD_FLAGS) -o $(DIST_DIR)/$(BINARY)-macos-arm64 $(CMD)

test:
	GOCACHE=$(GOCACHE) go test ./...

run:
	GOCACHE=$(GOCACHE) go run $(CMD)

clean:
	rm -f $(BINARY)
	rm -rf $(DIST_DIR)
