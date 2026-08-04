# syntax=docker/dockerfile:1

# ──────────────────────────────────────────────────────────────────────────
# Stage 1 — build the frontend (Vite → dist/)
# ──────────────────────────────────────────────────────────────────────────
FROM node:20-slim AS frontend
WORKDIR /app
ENV PNPM_HOME=/pnpm
ENV PATH="$PNPM_HOME:$PATH"

# Install deps first (cached until the lockfile changes). The pnpm version is
# pinned via the "packageManager" field in package.json, so corepack activates
# exactly that version instead of pulling a possibly-incompatible latest.
COPY package.json pnpm-lock.yaml ./
RUN corepack enable && corepack prepare pnpm@9.15.9 --activate
RUN --mount=type=cache,id=pnpm,target=/pnpm/store \
    pnpm install --frozen-lockfile

COPY . .
RUN pnpm run build

# ──────────────────────────────────────────────────────────────────────────
# Stage 2 — build the Go backend (embeds the built UI via -tags=prod)
# ──────────────────────────────────────────────────────────────────────────
# Must be >= the `go` directive in backend/go.mod (1.25.0, raised by
# modernc.org/sqlite), otherwise the build stalls on a toolchain download.
#
# Pinned to an EXACT patch on purpose. A floating `golang:1.25-alpine` resolves
# to whatever 1.25.x the registry serves that day, which is how a stdlib patch
# with open advisories ends up in a release image. 1.25.12 carries the fixes for
# the crypto/tls, crypto/x509, net, net/textproto and net/http/httputil
# advisories that govulncheck (blocking, in ci.yml) flags on older 1.25 patches.
# Bumping this is a deliberate chore: keep it in lockstep with the `go-version`
# pins in .github/workflows/ci.yml (x2) and .github/workflows/release.yml.
FROM golang:1.25.12-alpine AS backend
WORKDIR /app

# Download modules first (cached until go.mod/go.sum change).
COPY backend/go.mod backend/go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY backend/ ./
COPY --from=frontend /app/dist ./ui/dist

# Static, stripped, reproducible binary. CGO off → runs on distroless/scratch.
# The SQLite driver is modernc.org/sqlite (pure Go) precisely so this stays
# possible; a cgo-based driver would not link here.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux \
    go build -tags=prod -trimpath -ldflags="-s -w" -o /main main.go

# Staging directory for the runtime volume mount point. It exists only to be
# copied (with the right ownership) into the runtime stage below.
RUN mkdir -p /data

# ──────────────────────────────────────────────────────────────────────────
# Stage 3 — minimal, non-root runtime image
# ──────────────────────────────────────────────────────────────────────────
FROM gcr.io/distroless/static-debian12:nonroot AS runtime

# OCI image metadata (dynamic values are also injected by the CI build).
LABEL org.opencontainers.image.title="Garage WebUI-NG" \
      org.opencontainers.image.description="Modern admin dashboard for Garage S3-compatible object storage." \
      org.opencontainers.image.source="https://github.com/d7eeem/garage-webui-ng" \
      org.opencontainers.image.documentation="https://github.com/d7eeem/garage-webui-ng#readme" \
      org.opencontainers.image.licenses="MIT" \
      org.opencontainers.image.vendor="garage-webui-ng"

COPY --from=backend /main /main

# The user database lives here. Baking /data into the image owned by 65532 is
# what makes it writable: Docker seeds a fresh named volume from the image
# directory, so the volume inherits that ownership and the non-root process
# can create its database. Without this the app fails fast at startup with
# "cannot open user database".
COPY --from=backend --chown=65532:65532 /data /data

# distroless "nonroot" runs as uid/gid 65532 — no root in the final image.
USER nonroot:nonroot

ENV HOST=0.0.0.0 \
    PORT=3909 \
    DB_PATH=/data/garage-webui-ng.db
EXPOSE 3909

# Users are persistent application state: mount a volume here or every
# container recreation loses every account.
VOLUME ["/data"]

# Self-contained probe (no shell/curl in the image); honours PORT and BASE_PATH.
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD [ "/main", "-health" ]

ENTRYPOINT [ "/main" ]
