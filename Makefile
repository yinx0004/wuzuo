BIN        := bin/wuzuo
PKG        := github.com/yinx0004/wuzuo
VERSION    ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS    := -s -w -X $(PKG)/internal/version.Version=$(VERSION) \
              -X $(PKG)/internal/version.Commit=$(shell git rev-parse --short HEAD 2>/dev/null || echo none) \
              -X $(PKG)/internal/version.Date=$(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LAB        := deploy/lab/docker-compose.yml
# Lab passwords are generated per checkout by `make lab-secrets` into this
# gitignored file; nothing secret is committed.
LAB_SECRETS := deploy/lab/.env
-include $(LAB_SECRETS)
LAB_DSN      = wuzuo:$(LAB_WUZUO_PASSWORD)@tcp(127.0.0.1:13306)/
LAB_ROOT_DSN = root:$(LAB_ROOT_PASSWORD)@tcp(127.0.0.1:13306)/

.PHONY: all build test test-integration lint fmt tidy clean lab-secrets lab-up lab-down lab-env require-lab

all: lint test build

build:
	go build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/wuzuo

test:
	go test -race ./...

test-integration: require-lab
	@WUZUO_TEST_MYSQL_DSN='$(LAB_DSN)' WUZUO_TEST_MYSQL_ROOT_DSN='$(LAB_ROOT_DSN)' \
		go test -race -tags integration ./toolsets/...

lint:
	golangci-lint run

fmt:
	gofmt -w .

tidy:
	go mod tidy

clean:
	rm -rf bin dist

lab-secrets:
	@if [ ! -f $(LAB_SECRETS) ]; then \
		umask 077; \
		printf 'LAB_ROOT_PASSWORD=%s\nLAB_WUZUO_PASSWORD=%s\nLAB_APP_PASSWORD=%s\n' \
			"$$(openssl rand -hex 16)" "$$(openssl rand -hex 16)" "$$(openssl rand -hex 16)" > $(LAB_SECRETS); \
		echo "generated $(LAB_SECRETS)"; \
	fi

lab-up: lab-secrets
	docker compose -f $(LAB) up -d --wait

# Also deletes the generated passwords; the next lab-up creates new ones.
# (compose needs the variables set even to tear down, hence lab-secrets.)
lab-down: lab-secrets
	docker compose -f $(LAB) down -v
	rm -f $(LAB_SECRETS)

require-lab:
	@test -f $(LAB_SECRETS) || { echo "no lab secrets; run make lab-up first" >&2; exit 1; }

# eval "$(make -s lab-env)" points WuZuo at the lab.
lab-env: require-lab
	@echo "export WUZUO_CONFIG=$(CURDIR)/deploy/lab/wuzuo.yaml"
	@echo "export WUZUO_LAB_DSN='$(LAB_DSN)'"
