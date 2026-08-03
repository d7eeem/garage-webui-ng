# syntax=docker/dockerfile:1

# ──────────────────────────────────────────────────────────────────────────
# Stage 1 — build the frontend (Vite → dist/)
# ──────────────────────────────────────────────────────────────────────────
FROM node:20-slim AS frontend
WORKDIR /app
ENV PNPM_HOME=/pnpm
ENV PATH="$PNPM_HOME:$PATH"
RUN corepack enable

# Install deps first (cached until the lockfile changes).
COPY package.json pnpm-lock.yaml ./
RUN --mount=type=cache,id=pnpm,target=/pnpm/store \
    pnpm install --frozen-lockfile

COPY . .
RUN pnpm run build

# ──────────────────────────────────────────────────────────────────────────
# Stage 2 — build the Go backend (embeds the built UI via -tags=prod)
# ──────────────────────────────────────────────────────────────────────────
FROM golang:1.24-alpine AS backend
WORKDIR /app

# Download modules first (cached until go.mod/go.sum change).
COPY backend/go.mod backend/go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY backend/ ./
COPY --from=frontend /app/dist ./ui/dist

# Static, stripped, reproducible binary. CGO off → runs on distroless/scratch.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux \
    go build -tags=prod -trimpath -ldflags="-s -w" -o /main main.go

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

# distroless "nonroot" runs as uid/gid 65532 — no root in the final image.
USER nonroot:nonroot

ENV HOST=0.0.0.0 \
    PORT=3909
EXPOSE 3909

# Self-contained probe (no shell/curl in the image); honours PORT and BASE_PATH.
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD [ "/main", "-health" ]

ENTRYPOINT [ "/main" ]
