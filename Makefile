OUTPUT_DIR ?= _output

PKG = ./pkg/...


GO_FLAGS ?= -mod=vendor
GO_TEST_FLAGS ?= -race -cover

GO_PATH ?= $(shell go env GOPATH)
GO_CACHE ?= $(shell go env GOCACHE)

INSTALL_LOCATION ?= /usr/local/bin

ARGS ?=

CONTAINER_ENGINE ?= podman
LOCATION_SERVICE_IMAGE ?= quay.io/redhat-ai-dev/model-catalog-location-service
LOCATION_SERVICE_TAG ?= latest
RHOAI_NORMALIZER_IMAGE ?= quay.io/redhat-ai-dev/model-catalog-rhoai-normalizer
RHOAI_NORMALIZER_TAG ?= latest
STORAGE_REST_IMAGE ?= quay.io/redhat-ai-dev/model-catalog-storage-rest
STORAGE_REST_TAG ?= latest

QUICKTYPE_VERSION ?= 23.0.175
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

install-quicktype:
	npm install -g quicktype@${QUICKTYPE_VERSION}

generate-types-all: generate-typescript generate-golang

# Requires quicktype to be installed before running
# Run 'make install-quicktype' first if it's not installed
generate-typescript:
	cd schema/types/typescript; yarn generate

generate-golang:
	cd schema; sed 's|\#/$$defs/modelServerAPI|\#/$$defs/modelServer/$$defs/modelServerAPI|g' model-catalog.schema.json | quicktype -s schema -o types/golang/model-catalog.go --package golang

build-container-location:
	${CONTAINER_ENGINE} build -t ${LOCATION_SERVICE_IMAGE}:${LOCATION_SERVICE_TAG} -f Dockerfile.location .

build-container-rhoai-normalizer:
	${CONTAINER_ENGINE} build -t ${RHOAI_NORMALIZER_IMAGE}:${RHOAI_NORMALIZER_TAG} -f Dockerfile.rhoai-normalizer .

build-container-storage-rest:
	${CONTAINER_ENGINE} build -t ${STORAGE_REST_IMAGE}:${STORAGE_REST_TAG} -f Dockerfile.storage-rest .

build-containers: build-container-location build-container-rhoai-normalizer build-container-storage-rest