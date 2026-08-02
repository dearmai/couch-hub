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

.PHONY: help web embed build build-go image test vet gen-template verify-uri verify-livesync e2e check dev-server dev-down dev-ps dev-reset clean
