BIN        := bin/yuanfang
PKG        := github.com/yinx0004/yuanfang
VERSION    ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS    := -s -w -X $(PKG)/internal/version.Version=$(VERSION) \
              -X $(PKG)/internal/version.Commit=$(shell git rev-parse --short HEAD 2>/dev/null || echo none) \
              -X $(PKG)/internal/version.Date=$(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LAB        := lab/docker-compose.yml
# Lab passwords are generated per checkout by `make lab-secrets` into this
# gitignored file; nothing secret is committed.
LAB_SECRETS := lab/.env
-include $(LAB_SECRETS)
LAB_DSN      = yuanfang:$(LAB_YUANFANG_PASSWORD)@tcp(127.0.0.1:13306)/
LAB_ROOT_DSN = root:$(LAB_ROOT_PASSWORD)@tcp(127.0.0.1:13306)/

.PHONY: all build test test-integration lint fmt tidy clean lab-secrets lab-up lab-stop lab-destroy lab-env require-lab eval

all: lint test build

build:
	go build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/yuanfang

test:
	go test -race ./...

test-integration: require-lab
	@YUANFANG_TEST_MYSQL_DSN='$(LAB_DSN)' YUANFANG_TEST_MYSQL_ROOT_DSN='$(LAB_ROOT_DSN)' \
		go test -race -tags integration ./toolsets/...

# Examine Yuanfang on incidents staged in the lab. Calls the model for real
# and restarts the lab MySQL; see eval/README.md.
#   make eval EVAL_ARGS="-scenario lock-wait -runs 3 -m sonnet"
eval: require-lab
	go run ./eval $(EVAL_ARGS)

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
		printf 'LAB_ROOT_PASSWORD=%s\nLAB_YUANFANG_PASSWORD=%s\nLAB_APP_PASSWORD=%s\n' \
			"$$(openssl rand -hex 16)" "$$(openssl rand -hex 16)" "$$(openssl rand -hex 16)" > $(LAB_SECRETS); \
		echo "generated $(LAB_SECRETS)"; \
	fi

# Start the lab, creating and seeding it on first use.
lab-up: lab-secrets
	docker compose -f $(LAB) up -d --wait

# Pause the lab; data and passwords are kept for the next lab-up.
lab-stop: require-lab
	docker compose -f $(LAB) stop

# Remove the lab completely: container, data and generated passwords. The
# next lab-up starts from scratch. (compose needs the password variables set
# even to tear down, hence lab-secrets.)
lab-destroy: lab-secrets
	docker compose -f $(LAB) down -v
	rm -f $(LAB_SECRETS)

require-lab:
	@test -f $(LAB_SECRETS) || { echo "no lab secrets; run make lab-up first" >&2; exit 1; }

# eval "$(make -s lab-env)" points Yuanfang at the lab.
lab-env: require-lab
	@echo "export YUANFANG_CONFIG=$(CURDIR)/lab/yuanfang.yaml"
	@echo "export YUANFANG_LAB_DSN='$(LAB_DSN)'"
