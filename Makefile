GO ?= go
DIST := dist

# name:os:arch triples for the cross-compile matrix
TARGETS := lin-x64:linux:amd64 lin-arm64:linux:arm64 \
           mac-darwin:darwin:arm64 mac-darwin-amd64:darwin:amd64 \
           :windows:amd64

GOOS := $(shell $(GO) env GOOS)
GOARCH := $(shell $(GO) env GOARCH)

ifeq ($(GOOS),linux)
  ifeq ($(GOARCH),amd64)
    HOST_NAME := lin-x64
  else
    HOST_NAME := lin-arm64
  endif
else ifeq ($(GOOS),darwin)
  ifeq ($(GOARCH),arm64)
    HOST_NAME := mac-darwin
  else
    HOST_NAME := mac-darwin-amd64
  endif
else ifeq ($(GOOS),windows)
  HOST_NAME := windows
endif

ifeq ($(GOOS),windows)
  HOST_OUT := $(DIST)/dashboard.exe
else
  HOST_OUT := $(DIST)/dashboard-$(HOST_NAME)
endif

LDFLAGS := -trimpath -ldflags "-s -w"

.PHONY: build build-all test dev clean

build:
	@mkdir -p $(DIST)
	CGO_ENABLED=0 $(GO) build $(LDFLAGS) -o $(HOST_OUT) ./cmd/dashboard
	@echo "Built $(HOST_OUT)"

build-all:
	@mkdir -p $(DIST)
	@for t in $(TARGETS); do \
		name=$${t%%:*}; rest=$${t#*:}; os=$${rest%%:*}; arch=$${rest#*:}; \
		out=$(DIST)/dashboard-$$name; \
		[ "$$os" = windows ] && out=$(DIST)/dashboard.exe; \
		echo "Building $$out (GOOS=$$os GOARCH=$$arch)"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch $(GO) build $(LDFLAGS) -o $$out ./cmd/dashboard || exit 1; \
	done

test:
	$(GO) test ./...
	node --test test/filter.test.mjs test/i18n.test.mjs test/refresh.test.mjs test/dbfield.test.mjs

dev:
	$(GO) run ./cmd/dashboard -dev

clean:
	rm -rf dist build