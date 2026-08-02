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
# The remote host builds its own image from the synced source, so nothing has to
# be published to a registry and the image matches the architecture it runs on -
# which a build on an arm64 laptop for an amd64 server would not.
#
# Override any of these on the command line:
#   make remote-deploy REMOTE_HOST=user@host REMOTE_DIR=/srv/couch-hub
REMOTE_HOST ?= dearmai@192.168.20.22
REMOTE_DIR  ?= /apps/couch-hub
REMOTE_COMPOSE ?= podman compose

# BatchMode fails immediately when key authentication is not set up, rather than
# hanging on a password prompt inside a make recipe.
SSH ?= ssh -o BatchMode=yes

# The tree is shipped as a tar stream rather than with rsync, which a minimal
# server install does not necessarily have. tar and ssh always do.
#
# REMOTE_TREES are removed before the archive is unpacked, so a file deleted
# here disappears there too. Naming them - instead of clearing the directory -
# keeps .env and anything else the remote owns out of the blast radius.
REMOTE_TREES := cmd internal web caddy scripts

# Build inputs only. Everything else is either regenerated inside the image,
# host-specific, or - in .env's case - the remote's own to keep.
REMOTE_EXCLUDES := \
	--exclude ./.git --exclude ./.env --exclude ./data --exclude ./bin --exclude ./.air \
	--exclude ./tools --exclude ./node_modules --exclude ./web/node_modules \
	--exclude ./scripts/node_modules --exclude ./web/dist --exclude ./web/.e2e-data \
	--exclude ./web/test-results --exclude ./web/playwright-report \
	--exclude ./internal/httpapi/webdist/assets

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

## remote-env: upload the local .env to the remote (0600, never synced otherwise)
# Secrets move only when asked for explicitly: remote-deploy leaves the remote
# .env alone, so a deploy can never overwrite the running configuration.
remote-env:
	@test -f .env || { echo "로컬 .env가 없습니다. .env.example을 복사해 채우세요"; exit 1; }
	@# umask, not a later chmod: the file must never exist world-readable, not
	@# even for the moment between writing it and fixing its mode.
	$(SSH) $(REMOTE_HOST) 'mkdir -p $(REMOTE_DIR) && umask 077 && cat > $(REMOTE_DIR)/.env' < .env

## remote-sync: copy the source tree to the remote host
remote-sync:
	tar czf - $(REMOTE_EXCLUDES) . | \
		$(SSH) $(REMOTE_HOST) 'mkdir -p $(REMOTE_DIR) && cd $(REMOTE_DIR) && rm -rf $(REMOTE_TREES) && tar xzf -'

## remote-deploy: sync, build on the remote, and start or restart the stack
# `up -d --build` is both the first start and every restart after it: compose
# recreates the containers whose image or configuration changed and leaves the
# rest running, so CouchDB is not bounced for a CouchHub-only change.
remote-deploy: remote-check remote-sync
	$(SSH) $(REMOTE_HOST) 'cd $(REMOTE_DIR) && $(REMOTE_COMPOSE) up -d --build --remove-orphans'
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
remote-boot:
	$(SSH) $(REMOTE_HOST) 'loginctl enable-linger $$USER && systemctl --user enable --now podman-restart.service'

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
	rm -rf bin web/dist
	find internal/httpapi/webdist -mindepth 1 ! -name .gitkeep -delete

.PHONY: help web embed build build-go image test vet gen-template verify-uri verify-livesync e2e check \
	remote-check remote-env remote-sync remote-deploy remote-restart remote-ps remote-logs remote-down remote-boot \
	dev-server dev-down dev-ps dev-reset clean
