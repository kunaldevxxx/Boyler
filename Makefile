OS = linux
ARCH = amd64
VERSION ?= dev
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS = -s -w -X boyler/internal/version.Version=$(VERSION) -X boyler/internal/version.Commit=$(COMMIT) -X boyler/internal/version.BuildDate=$(BUILD_DATE)

.PHONY: compile
compile:
	CGO_ENABLED=0 GOOS=$(OS) GOARCH=$(ARCH) go build -trimpath -ldflags "$(LDFLAGS)" -o bin/boyler ./cmd/boyler
	CGO_ENABLED=0 GOOS=$(OS) GOARCH=$(ARCH) go build -trimpath -ldflags "$(LDFLAGS)" -o bin/myrunc ./cmd/myrunc
	CGO_ENABLED=0 GOOS=$(OS) GOARCH=$(ARCH) go build -trimpath -ldflags "$(LDFLAGS)" -o bin/boyler-shim ./cmd/boyler-shim
	CGO_ENABLED=0 GOOS=$(OS) GOARCH=$(ARCH) go build -trimpath -ldflags "$(LDFLAGS)" -o bin/daemon_boyler_$(OS) ./cmd/boylerd
	@echo "Binary files was created"


.PHONY: genproto
genproto:
	protoc --proto_path=proto \
		--go_out=internal/daemon/infrastructure/inbound/api/grpc/gen \
		--go_opt=paths=source_relative \
		--go-grpc_out=internal/daemon/infrastructure/inbound/api/grpc/gen \
		--go-grpc_opt=paths=source_relative \
		proto/daemon.proto


.PHONY: prepare
prepare:
	-mkdir lib
	-mkdir lib/containers
	-mkdir lib/images
	-mkdir bin


.PHONY: clean
clean:
	-sudo ip link del boyler0
