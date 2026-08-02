BINARY      := bin/couchhub
GO_PACKAGES := ./...

# podman and docker are interchangeable here; override to pick one explicitly.
CONTAINER_ENGINE ?= $(shell command -v podman 2>/dev/null || command -v docker 2>/dev/null || echo docker)

# process-compose's own API port. Exported so `up`, `down` and `process list`
# all agree - they are separate invocations that find each other by port, and
# the default 8080 collides with almost everything.
PC_PORT_NUM ?= 10029
export PC_PORT_NUM

# Always name the file when starting. process-compose auto-discovers its config,
# and `compose.yaml` - the Docker Compose file next to it - is one of the names
# it accepts, so a bare `up` loads that instead, finds no `processes:` key, and
# starts an empty project that looks like a silent hang.
#
# Only `up` takes --config; `down` and `process list` reach the running instance
# over PC_PORT_NUM instead.
PC_CONFIG := --config process-compose.yaml

.DEFAULT_GOAL := help

## help: list targets
help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/## //' | awk -F': ' '{printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'

## web: build the React app
web: web/node_modules
	cd web && npm run build

web/node_modules: web/package.json
	cd web && npm install --no-audit --no-fund
	@touch $@

## embed: stage the built UI where //go:embed picks it up
# Everything but .gitkeep is a build artifact, so clear it out first and drop
# stale content-hashed files with it.
embed: web
	find internal/httpapi/webdist -mindepth 1 ! -name .gitkeep -delete
	cp -R web/dist/. internal/httpapi/webdist/

## build: build the UI and compile the couchhub binary
build: embed
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o $(BINARY) ./cmd/couchhub

## build-go: compile only the Go binary, reusing whatever UI is already staged
build-go:
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o $(BINARY) ./cmd/couchhub

## test: run Go unit tests
test:
	go test $(GO_PACKAGES)

## vet: run go vet and gofmt check
vet:
	go vet $(GO_PACKAGES)
	@unformatted=$$(gofmt -l cmd internal); \
	if [ -n "$$unformatted" ]; then echo "gofmt needed:"; echo "$$unformatted"; exit 1; fi

## gen-template: regenerate internal/setupuri/template.json from the official livesync library
gen-template: scripts/node_modules
	node scripts/gen-template.mjs

## verify-uri: cross-check our Setup URI against @vrtmrz/livesync-commonlib (M1 acceptance gate)
verify-uri: scripts/node_modules
	node scripts/verify-setup-uri.mjs

## verify-livesync: cross-check chunk decryption against octagonal-wheels
verify-livesync: scripts/node_modules build-go
	node scripts/verify-livesync-crypto.mjs

scripts/node_modules: scripts/package.json
	cd scripts && npm install --no-audit --no-fund
	@touch $@

## image: build the container image
image:
	$(CONTAINER_ENGINE) build -f Containerfile -t couchhub:local .
	@$(CONTAINER_ENGINE) images couchhub:local --format '  image size: {{.Size}}'

## e2e: drive the real binary against a real CouchDB with Playwright
# Uses the same throwaway container as dev-server; `make dev-reset` clears it.
e2e: build
	@if ! curl -fsS -m 2 http://127.0.0.1:15984/_up >/dev/null 2>&1; then \
		echo "starting CouchDB for e2e..."; \
		$(CONTAINER_ENGINE) run -d --rm --name couchhub-dev-couchdb -p 15984:5984 \
			-e COUCHDB_USER=admin -e COUCHDB_PASSWORD=couchhub-dev \
			-v couchhub-dev-couchdb-data:/opt/couchdb/data \
			-v couchhub-dev-couchdb-etc:/opt/couchdb/etc/local.d \
			docker.io/library/couchdb:3.5 >/dev/null; \
		for i in $$(seq 1 60); do curl -fsS -m 2 http://127.0.0.1:15984/_up >/dev/null 2>&1 && break; sleep 1; done; \
	fi
	cd web && npx playwright test

## check: everything CI runs
check: vet test verify-uri verify-livesync

# --- remote deployment ------------------------------------------------------
#
# Everything expensive happens here. The remote receives a Linux binary with the
# UI embedded and assembles an image around it, so a small host never runs npm
# or the Go toolchain - and nothing has to be published to a registry.
#
# Override any of these on the command line:
#   make remote-deploy REMOTE_HOST=user@host REMOTE_DIR=/srv/couch-hub
REMOTE_HOST ?= dearmai@192.168.20.22
REMOTE_DIR  ?= /apps/couch-hub
REMOTE_COMPOSE ?= podman compose

# BatchMode fails immediately when key authentication is not set up, rather than
# hanging on a password prompt inside a make recipe.
SSH ?= ssh -o BatchMode=yes

# Image name for the build assembled on the remote. Not a registry reference -
# nothing is published. The tag is the binary's content hash, added at deploy
# time: compose only recreates a container whose configuration changed, and a
# fixed tag is not a change, so a new binary under an old tag would be built,
# shipped, and then quietly not run.
REMOTE_IMAGE ?= couchhub

# What the remote actually needs. No source, no toolchain: it receives a Linux
# binary with the UI already embedded and does nothing but copy it into an
# image. REMOTE_TREES are cleared before the archive is unpacked, so a stale
# binary or Caddyfile cannot survive a deploy - and .env, being outside the
# list, is never touched.
REMOTE_TREES := caddy dist
REMOTE_FILES := compose.yaml Containerfile.prebuilt caddy dist/couchhub-linux

REMOTE_PODMAN ?= podman

## remote-check: verify the remote host is reachable and ready to deploy
remote-check:
	@echo "checking $(REMOTE_HOST)..."
	@$(SSH) $(REMOTE_HOST) true || { echo "ssh 실패: 키 인증과 호스트를 확인하세요"; exit 1; }
	@$(SSH) $(REMOTE_HOST) 'command -v podman >/dev/null' || { echo "원격에 podman이 없습니다"; exit 1; }
	@$(SSH) $(REMOTE_HOST) 'command -v tar >/dev/null' || { echo "원격에 tar가 없습니다"; exit 1; }
	@$(SSH) $(REMOTE_HOST) '$(REMOTE_COMPOSE) version >/dev/null 2>&1' || { \
		echo "원격에서 '$(REMOTE_COMPOSE)'가 동작하지 않습니다. podman-compose 또는 docker-compose를 설치하세요"; exit 1; }
	@$(SSH) $(REMOTE_HOST) 'test -f $(REMOTE_DIR)/.env' || { \
		echo "$(REMOTE_DIR)/.env 가 없습니다. 'make remote-env' 로 로컬 .env를 올리거나 원격에서 직접 만드세요"; exit 1; }
	@echo "ok"

## remote-env-init: create a local .env from the example, with the secrets generated
# Never overwrites: COUCHHUB_SECRET is what every stored credential is sealed
# with, so replacing one already in use costs every vault a reissue.
remote-env-init:
	@test ! -f .env || { echo ".env가 이미 있습니다. 다시 만들려면 먼저 옮기거나 지우세요 (기존 COUCHHUB_SECRET을 잃으면 모든 Vault를 재발급해야 합니다)"; exit 1; }
	@umask 077; \
	pw=$$(openssl rand -hex 24); \
	secret=$$(openssl rand -base64 32); \
	sed -e "s|^COUCHDB_PASSWORD=.*|COUCHDB_PASSWORD=$$pw|" \
	    -e "s|^COUCHHUB_SECRET=.*|COUCHHUB_SECRET=$$secret|" .env.example > .env
	@chmod 600 .env
	@echo ".env 생성됨 (0600). 시크릿 2개는 채워졌습니다."
	@echo "직접 채울 것: SYNC_DOMAIN, HUB_DOMAIN"
	@echo "다음: 수정 후 'make remote-env' 로 업로드, 'make remote-deploy' 로 배포"

## remote-env: upload the local .env to the remote (0600, never synced otherwise)
# Secrets move only when asked for explicitly: remote-deploy leaves the remote
# .env alone, so a deploy can never overwrite the running configuration.
remote-env:
	@test -f .env || { echo "로컬 .env가 없습니다. .env.example을 복사해 채우세요"; exit 1; }
	@# umask, not a later chmod: the file must never exist world-readable, not
	@# even for the moment between writing it and fixing its mode.
	$(SSH) $(REMOTE_HOST) 'mkdir -p $(REMOTE_DIR) && umask 077 && cat > $(REMOTE_DIR)/.env' < .env

## remote-dist: cross-compile a Linux binary for the remote, UI embedded
# The architecture is read from the remote rather than assumed: a binary built
# for the wrong one starts and exits immediately with an exec format error,
# which reads like a corrupt image rather than a wrong compiler flag.
remote-dist: embed
	@arch=$$($(SSH) $(REMOTE_HOST) uname -m); \
	case "$$arch" in \
		x86_64|amd64) goarch=amd64;; \
		aarch64|arm64) goarch=arm64;; \
		*) echo "지원하지 않는 원격 아키텍처: $$arch"; exit 1;; \
	esac; \
	echo "building linux/$$goarch for $(REMOTE_HOST)"; \
	mkdir -p dist; \
	GOOS=linux GOARCH=$$goarch CGO_ENABLED=0 \
		go build -trimpath -ldflags="-s -w" -o dist/couchhub-linux ./cmd/couchhub
	@ls -lh dist/couchhub-linux | awk '{print "  binary: " $$5}'

## remote-sync: ship the built binary and the compose files
remote-sync:
	tar czf - $(REMOTE_FILES) | \
		$(SSH) $(REMOTE_HOST) 'mkdir -p $(REMOTE_DIR) && cd $(REMOTE_DIR) && rm -rf $(REMOTE_TREES) && tar xzf -'

## remote-deploy: build here, ship the binary, and start or restart the stack
# Nothing is compiled on the remote: it receives a Linux binary with the UI
# already embedded and assembles an image around it, which is a copy and a
# 4 MB alpine pull. That matters on a host too small to run npm and the Go
# toolchain without swapping.
#
# `up -d` is both the first start and every restart after it. podman-compose
# recreates the project's containers whenever anything in the configuration
# changed, CouchDB included - a few seconds of downtime per deploy, and nothing
# lost, since the data is in named volumes. A deploy that changes nothing
# recreates nothing.
remote-deploy: remote-check remote-dist remote-sync
	@tag=$$(shasum -a 256 dist/couchhub-linux | cut -c1-12); \
	echo "image $(REMOTE_IMAGE):$$tag"; \
	$(SSH) $(REMOTE_HOST) "cd $(REMOTE_DIR) && \
		$(REMOTE_PODMAN) build -f Containerfile.prebuilt -t $(REMOTE_IMAGE):$$tag . && \
		COUCHHUB_IMAGE=$(REMOTE_IMAGE):$$tag $(REMOTE_COMPOSE) up -d --remove-orphans && \
		$(REMOTE_PODMAN) images --format '{{.Repository}}:{{.Tag}}' \
			| grep -E '(^|/)$(REMOTE_IMAGE):' | grep -v ':$$tag\$$' \
			| xargs -r $(REMOTE_PODMAN) rmi -f >/dev/null 2>&1 || true"
	@$(MAKE) --no-print-directory remote-ps

## remote-restart: restart the running containers without rebuilding
remote-restart:
	$(SSH) $(REMOTE_HOST) 'cd $(REMOTE_DIR) && $(REMOTE_COMPOSE) restart'

## remote-ps: show the remote container states
remote-ps:
	$(SSH) $(REMOTE_HOST) 'cd $(REMOTE_DIR) && $(REMOTE_COMPOSE) ps'

## remote-logs: follow the remote logs (SERVICE=couchhub for one service)
remote-logs:
	$(SSH) -t $(REMOTE_HOST) 'cd $(REMOTE_DIR) && $(REMOTE_COMPOSE) logs -f --tail 100 $(SERVICE)'

## remote-down: stop the remote stack, keeping its volumes
remote-down:
	$(SSH) $(REMOTE_HOST) 'cd $(REMOTE_DIR) && $(REMOTE_COMPOSE) down'

## remote-boot: make the containers come back after a reboot (rootless podman)
# `restart: unless-stopped` is honoured by the podman restart service, and a
# rootless user's services only run outside a login session with lingering on.
# Without both, a reboot leaves the host with nothing running.
# enable-linger is a privileged call - polkit denies it to a plain ssh session
# with no seat - so it falls back to sudo on a tty before giving up.
remote-boot:
	@$(SSH) $(REMOTE_HOST) 'loginctl enable-linger $$USER' 2>/dev/null \
		|| $(SSH) -t $(REMOTE_HOST) 'sudo loginctl enable-linger $$USER' \
		|| { echo "linger 설정 실패. 원격에서 'sudo loginctl enable-linger <계정>' 을 직접 실행하세요"; exit 1; }
	$(SSH) $(REMOTE_HOST) 'systemctl --user enable --now podman-restart.service'

## dev-server: run CouchDB, the Vite dev server and the API under process-compose
dev-server: web/node_modules .air/air
	process-compose $(PC_CONFIG) up

## dev-down: stop the dev stack started by dev-server
dev-down:
	process-compose down

## dev-ps: show dev stack process states
dev-ps:
	process-compose process list

# air lives in tools/, its own module: it needs a newer Go than the server does,
# and keeping it in the root module dragged the production build image along.
.air/air: tools/go.mod tools/go.sum
	cd tools && go build -o ../.air/air github.com/air-verse/air

## dev-reset: throw away the development CouchDB and local store
dev-reset:
	-$(CONTAINER_ENGINE) rm -f couchhub-dev-couchdb
	-$(CONTAINER_ENGINE) volume rm couchhub-dev-couchdb-data couchhub-dev-couchdb-etc
	rm -rf data

## clean: remove build output
# webdist keeps .gitkeep: `go build` needs the directory to exist.
clean:
	rm -rf bin dist web/dist
	find internal/httpapi/webdist -mindepth 1 ! -name .gitkeep -delete

.PHONY: help web embed build build-go image test vet gen-template verify-uri verify-livesync e2e check \
	remote-check remote-env-init remote-env remote-dist remote-sync remote-deploy remote-restart remote-ps remote-logs remote-down remote-boot \
	dev-server dev-down dev-ps dev-reset clean
