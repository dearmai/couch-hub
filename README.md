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

Open `https://<HUB_DOMAIN>` and follow the install wizard.

`COUCHHUB_SECRET` seals credentials at rest. Without it CouchHub still runs, but
vault credentials are shown once and never stored — Setup URIs cannot be
reissued, and zones cannot be used at all.

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
is your own - a vault can receive documents from a zone peer, and script in a
note would run inside the panel that holds every vault's credentials.

## Zones

A zone connects two CouchHub instances. Each side exposes its vault registry on
a token-protected endpoint; the other pulls it and writes documents into
CouchDB's `_replicator`, so **CouchDB does the replicating** and a zone keeps
running while CouchHub is restarted.

Create a zone on one side, copy the token it shows once, and create the matching
zone on the peer. Vaults are paired by database name — a vault that exists on
only one side is reported as skipped rather than created silently.

The zone token hands out live vault credentials. Only expose the panel over
HTTPS.

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
  zone/              peer registry exchange and _replicator planning
  metrics/           statistics poller
  store/             bbolt persistence
  secret/            credential sealing
  httpapi/           REST API, embedded UI, install guide
web/                 React + Vite + Tailwind + shadcn/ui
scripts/             dev-only: template generation and the cross-check
```
