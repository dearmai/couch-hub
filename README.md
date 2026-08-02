# CouchHub

CouchDB control panel for [obsidian-livesync](https://github.com/vrtmrz/obsidian-livesync), built for a homelab.

Provisions CouchDB to livesync's requirements, creates a vault per Obsidian
vault with its own database and account, hands the client a QR code, and shows
what each vault is costing in disk and writes.

- Single static binary with the UI embedded — ~8 MB, ~13 MB RSS
- Runs on `scratch` as a non-root user
- Works against an existing CouchDB; nothing has to be installed by CouchHub
- Mobile-first UI

## Quick start

```sh
cp .env.example .env
# fill in COUCHDB_PASSWORD, COUCHHUB_SECRET, SYNC_DOMAIN, HUB_DOMAIN
podman compose up -d          # or: docker compose up -d
```

Open `https://<HUB_DOMAIN>`. The CouchDB this file creates is registered and
provisioned on first start — same `.env`, so there is nothing to retype — and
the panel opens on the dashboard rather than the install wizard.

The wizard is still there for a CouchDB compose did not create, and for the case
where the bootstrap could not run: it is prefilled from the same environment,
minus the administrator password, which the API does not hand out.

`COUCHHUB_SECRET` seals credentials at rest. Without it CouchHub still runs, but
vault credentials are shown once and never stored — Setup URIs cannot be
reissued, more CouchDB servers cannot be registered, and a vault cannot be moved
between them.

## The two addresses

CouchHub keeps them separate on purpose, and conflating them is the most common
way to end up with sync that works on the desktop and nowhere else.

| Setting | Who uses it | Example |
|---|---|---|
| CouchHub 연동용 | CouchHub → CouchDB, over the container network | `http://couchdb:5984` |
| Obsidian 연동용 | Obsidian → CouchDB, through the reverse proxy | `https://sync.example.com` |

The second one is what goes into every Setup URI, so it has to be reachable from
a phone.

## Connecting a client

Creating a vault issues two forms of the same settings. They are consumed
differently, and the plugin accepts each in only one place:

| Form | How it is used |
|---|---|
| `?settingsQR=` (default) | Scan the QR with the phone camera, or click "이 컴퓨터의 Obsidian에서 열기". Nothing to type. |
| `?settings=` | Paste into Obsidian's *Use the setup URI* dialog and enter the PIN as the passphrase. |

The plugin has **no in-app camera** — its "Scan QR Code" dialog only instructs
you to use the phone's camera app, which hands the `obsidian://` URL to Obsidian.
Both forms travel that same path.

> The default QR carries the CouchDB password and the end-to-end passphrase in
> the clear. That is exactly what removes the passphrase prompt. Anyone who
> photographs it has the vault; use the PIN-protected form for anything that
> leaves the room.

## Browsing documents

The vault detail page can list the notes a vault holds and show each one's
markdown before and after rendering. A note is not one document - it is an entry
plus a chain of chunks, and with encryption on both its path and every chunk are
ciphertext - so CouchHub reassembles and decrypts it server-side.

That means the panel handles vault contents, which not every deployment wants
from a web UI. Set `COUCHHUB_DOCUMENTS=false` to remove it: the tab disappears
and the endpoints answer 403, so it is closed rather than hidden. Enabled by
default.

Rendered markdown is sanitised. Note content is not trusted input even when it
is your own - a vault can be adopted from a database CouchHub did not create,
and script in a note would run inside the panel that holds every vault's
credentials.

## CouchDB servers

CouchHub manages a list of them rather than a single one. Adding a server runs
the same provisioning the install wizard does, so a registered server is always
one a vault can actually be created on.

One server is the **primary**: it is where a vault lands when none is chosen,
and where the database list comes from by default. Moving the flag is its own
action — editing a server's address never changes it.

A server holding vaults cannot be removed. Removing an empty one only forgets
it; the CouchDB behind it is left alone, because CouchHub did not necessarily
install it.

## Moving a vault to another CouchDB

The vault detail page's 관리 tab copies a vault's database to another
registered server:

1. CouchHub creates the database, the vault's account and its `_security`
   document on the target.
2. It writes a one-shot document into the target's `_replicator`. **CouchDB does
   the copying** — the panel only polls the scheduler for progress.
3. Nothing has moved yet. The vault still serves from the old server, so a copy
   that stalls or fails costs only the target database.
4. Finishing points the vault at the new server, and optionally removes the
   original.

Two things this deliberately does not do. It does not stop clients writing to
the source, so notes saved after the copy started stay behind — stop syncing
before the switch-over, or copy again. And it does not re-issue Setup URIs: when
the two servers are published under different addresses, every device needs the
new URI before it syncs again, which the panel says at switch-over time.

The replication document lives on the target, so the **target CouchDB** has to
be able to reach the source. CouchHub fills in the source's Obsidian-facing
address for that, being the one that is routable from another host.

## Deploying to a server

`make remote-deploy` builds here, ships the result, and starts the stack under
`podman compose`. The same command is the restart: podman-compose recreates the
project's containers whenever the configuration changed, CouchDB included — a
few seconds of downtime per deploy, with nothing lost, since the data lives in
named volumes. A deploy that changes nothing recreates nothing.

The image is tagged with the binary's content hash. A fixed tag would not read
as a configuration change, so a rebuilt binary would be shipped and then quietly
not run; hashing it also means an unchanged build redeploys as a no-op. Older
tags are removed on the way out.

Nothing is compiled on the remote. The UI is built and the binary
cross-compiled locally — `GOARCH` read from the host rather than assumed, since
the wrong one produces a container that exits with an exec format error — and
the remote assembles an image around it with `Containerfile.prebuilt`, which is
a copy and a 4 MB alpine pull. A server too small to run npm and the Go
toolchain comfortably never has to.

`Containerfile` remains the self-contained build for `make image` and CI: it
takes source and needs nothing prepared in advance.

```sh
make remote-env-init    # .env from the example, both secrets generated
$EDITOR .env            # SYNC_DOMAIN and HUB_DOMAIN are yours to fill in
make remote-env         # upload it once - 0600, and never synced afterwards
make remote-deploy
```

`remote-env-init` refuses to overwrite an existing `.env`. `COUCHHUB_SECRET` is
what every stored credential is sealed with, so replacing one already in use
costs every vault a reissue.

After that, `make remote-deploy` on its own. `.env` stays out of every sync, so
the remote keeps the configuration it is running with.

| Target | What it does |
|---|---|
| `remote-env-init` | write a local `.env` with generated secrets, never overwriting |
| `remote-check` | ssh, podman, compose provider and `.env`, without changing anything |
| `remote-dist` | cross-compile the Linux binary into `dist/couchhub-linux` |
| `remote-deploy` | build here, ship the binary, start or restart |
| `remote-restart` | restart the containers without rebuilding |
| `remote-ps` / `remote-logs` | states; `SERVICE=couchhub make remote-logs` for one |
| `remote-down` | stop the stack, keeping its volumes |
| `remote-boot` | survive a reboot: lingering plus `podman-restart.service` |

| Variable | Default |
|---|---|
| `REMOTE_HOST` | `dearmai@192.168.20.22` |
| `REMOTE_DIR` | `/apps/couch-hub` |
| `REMOTE_COMPOSE` | `podman compose` |

Ports on the deployed host:

| Service | Host | Container |
|---|---|---|
| CouchDB | `0.0.0.0:${COUCHDB_PORT:-10021}` | 5984 |
| CouchHub | `127.0.0.1:${COUCHHUB_PORT:-10020}` | 10020 |
| Caddy | 80, 443 — opt-in, `--profile proxy` | 80, 443 |

Both are published so something outside the compose network can reach them
without going through Caddy — a Cloudflare tunnel running on the host, for
instance.

They default to different addresses on purpose. CouchDB carries its own admin
account: the image disables admin party once `COUCHDB_PASSWORD` is set, and
CouchHub's provisioning then requires a valid user for everything but `/_up`, so
a LAN-reachable port is a port that asks for credentials. **The panel has no
login.** Anything that reaches it can read every vault's credentials and reissue
its Setup URIs, so it stays on loopback and the access control belongs in
whatever terminates its public hostname — Cloudflare Access, or an
authenticating proxy. `COUCHHUB_BIND=0.0.0.0` opens it to the LAN, and is only
sensible where the LAN itself is the trust boundary.

Caddy is one way to publish this stack and no longer the default one, so it sits
behind a compose profile: `podman compose --profile proxy up -d` starts it,
`make remote-deploy` does not. Starting it unasked would fail on a host that
already has something on 80/443.

### Publishing through a Cloudflare tunnel

`cloudflared` on the host reaches both published ports, and terminates the
hostnames itself — nothing here needs 80 or 443.

| Hostname | Service |
|---|---|
| `SYNC_DOMAIN` | `http://localhost:10021` |
| `HUB_DOMAIN` | `http://localhost:10020` |

CouchDB has to be at the root of its own hostname; livesync does not support a
subpath. Put an access policy on the panel's hostname — the tunnel is the
internet, and the panel has no login.

Cloudflare's own limits apply to sync traffic: request bodies are capped at
100 MB on the free plan, and a proxied request that goes 100 s without bytes
answers 524. Do not add CORS headers at the edge — CouchHub configures them on
CouchDB, and a duplicated `Access-Control-Allow-Origin` fails the browser check
outright.

Note that 10021 is also the Vite dev server's port, so a host running
`make dev-server` and the deployed stack at once needs one of them moved.

The remote needs ssh key authentication, `podman` with a compose provider, and
`tar`. What travels is `compose.yaml`, `Containerfile.prebuilt`, the Caddyfile
and the binary, as a tar stream rather than over rsync — which a minimal server
install does not necessarily have. `caddy/` and `dist/` are cleared before the
archive is unpacked, so a stale binary cannot survive a deploy, and `.env` is
outside that list and never touched.

## Development

```sh
make dev-server     # CouchDB + Vite + the API under process-compose
make dev-ps         # process states
make dev-down       # stop it
make dev-reset      # throw away the dev CouchDB and local store
make check          # vet, unit tests, and the Setup URI cross-check
make e2e            # Playwright against the real binary and a real CouchDB
make image          # container image
```

Ports in development: **10020** panel, 10021 Vite, **10029** process-compose's
own API, 15984 CouchDB. The process-compose port is set through `PC_PORT_NUM`
in the Makefile — its default of 8080 collides with almost everything, and `up`,
`down` and `process list` are separate invocations that find each other by port,
so they all have to agree.

`make dev-server` serves the UI from Vite through the Go server on
<http://localhost:10020>, so the dev origin matches production. The API
hot-reloads via `air`, which lives in its own `tools/` module — dev tooling
requires a newer Go than the server does, and keeping it in the root module
dragged the production build image up with it.

### The Setup URI cross-check

`internal/setupuri` reimplements livesync's encryption and its positional QR
encoding. Both are formats CouchHub does not own, and getting either subtly
wrong corrupts credentials rather than failing loudly — so
`scripts/verify-setup-uri.mjs` checks the output against the published
`@vrtmrz/livesync-commonlib` in both directions, byte for byte, and `make check`
runs it.

The index map and the settings template are **generated** from that library
(`make gen-template`), never hand-copied: the QR format is positional, so a
stale map would write values into the wrong settings without any error.

## Layout

```
cmd/couchhub/        entry point and the headless setup-uri tools
internal/
  config/            flags, environment, defaults
  couch/             CouchDB HTTP client
  provision/         the livesync configuration and the diff against a server
  setupuri/          Setup URI + QR encoding  ← cross-checked against upstream
  vault/             database, account and _security provisioning
  metrics/           statistics poller
  store/             bbolt persistence
  secret/            credential sealing
  httpapi/           REST API, embedded UI, install guide
web/                 React + Vite + Tailwind + shadcn/ui
scripts/             dev-only: template generation and the cross-check
```
