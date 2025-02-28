OUTPUT_DIR ?= _output

PKG = ./pkg/...


GO_FLAGS ?= -mod=vendor
GO_TEST_FLAGS ?= -race -cover

GO_PATH ?= $(shell go env GOPATH)
GO_CACHE ?= $(shell go env GOCACHE)

INSTALL_LOCATION ?= /usr/local/bin

ARGS ?=

.EXPORT_ALL_VARIABLES:



build:
	go build $(GO_FLAGS) -o _output/location ./cmd/location/...
	go build $(GO_FLAGS) -o _output/rhoai-normalizer ./cmd/rhoai-normalizer/...
	go build $(GO_FLAGS) -o _output/storage-rest ./cmd/storage-rest/...


clean:
	rm -rf "$(OUTPUT_DIR)"

# runs all tests
test: test-unit

.PHONY: test-unit
test-unit:
	go test $(GO_FLAGS) $(GO_TEST_FLAGS) ./cmd/... ./pkg/...

