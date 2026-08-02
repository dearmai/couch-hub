# Three stages so the runtime image carries neither Node nor the Go toolchain.
# Works with podman build and docker build alike.

# --- 1. build the React app -------------------------------------------------
FROM docker.io/library/node:22-alpine AS web

WORKDIR /src/web
# Dependencies first: they change far less often than the source, so this layer
# survives most rebuilds.
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund

COPY web/ ./
RUN npm run build


# --- 2. compile the binary --------------------------------------------------
# Pinned to the version go.mod requires. Keep the two in step: a newer go.mod
# directive than the image provides fails the build with a toolchain download
# attempt, which is exactly what a hermetic build should not do.
FROM docker.io/library/golang:1.25-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY cmd/ ./cmd/
COPY internal/ ./internal/

# Overwrite the committed placeholder with the real UI before //go:embed runs.
COPY --from=web /src/web/dist/ ./internal/httpapi/webdist/

# Static, stripped, reproducible: the result runs on scratch.
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/couchhub ./cmd/couchhub

# Staged here so the final image gets a /data owned by the runtime user. A named
# volume inherits its ownership from the image, and without this it would be
# root-owned and unwritable by a non-root process.
RUN mkdir -p /out/data && chown 65532:65532 /out/data


# --- 3. runtime -------------------------------------------------------------
FROM scratch

# CouchDB may sit behind HTTPS, and zone peers certainly do.
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build --chown=65532:65532 /out/data /data
COPY --from=build /out/couchhub /couchhub

# Non-root. A numeric id needs no /etc/passwd, which scratch does not have.
USER 65532:65532

ENV COUCHHUB_ADDR=:10020 \
    COUCHHUB_DATA_DIR=/data

EXPOSE 10020
VOLUME ["/data"]

ENTRYPOINT ["/couchhub"]
CMD ["serve"]
