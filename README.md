<div align="center">

<img src="src/assets/garage-logo.svg" alt="Garage WebUI-NG" width="120" />

# Garage WebUI-NG

**A modern, production-ready admin dashboard for [Garage](https://garagehq.deuxfleurs.fr/) — the self-hosted, S3-compatible, distributed object storage service.**

[![CI](https://github.com/d7eeem/garage-webui-ng/actions/workflows/ci.yml/badge.svg)](https://github.com/d7eeem/garage-webui-ng/actions/workflows/ci.yml)
[![Docker Publish](https://github.com/d7eeem/garage-webui-ng/actions/workflows/docker-publish.yml/badge.svg)](https://github.com/d7eeem/garage-webui-ng/actions/workflows/docker-publish.yml)
[![GHCR](https://img.shields.io/badge/ghcr.io-garage--webui--ng-2496ED?logo=docker&logoColor=white)](https://github.com/d7eeem/garage-webui-ng/pkgs/container/garage-webui-ng)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.24-00ADD8?logo=go&logoColor=white)](backend/go.mod)
[![React](https://img.shields.io/badge/React-18-61DAFB?logo=react&logoColor=black)](package.json)

[Features](#-key-features) · [Screenshots](#-screenshots) · [Quick Start](#-installation) · [Configuration](#-configuration) · [API](#-api) · [Roadmap](#-roadmap)

<img src="docs/screenshots/dashboard-dark.png" alt="Garage WebUI-NG dashboard" width="860" />

</div>

---

## 📖 Overview

**Garage WebUI-NG** is the next-generation web console for operating a [Garage](https://garagehq.deuxfleurs.fr/) cluster. It ships as a **single, self-contained binary** (a Go backend that embeds the compiled React frontend) or as a **~12 MB non-root Docker image**, and it holds no state of its own — it is a thin, secure gateway to your existing Garage cluster.

Point it at a running Garage node and you get a clean dashboard for cluster health, buckets, objects, access keys, static-website hosting, and object sharing — with optional authentication, a read-only operator role, and a structured audit trail.

## ✨ Key Features

- **📊 Live dashboard** — cluster health, node/partition status, total usage, and a live metrics panel (S3 requests, errors, block I/O) parsed from Garage's Prometheus endpoint.
- **🗂️ Bucket management** — create, inspect, and configure buckets: global/local aliases, quotas, and **static website hosting** with a correct, copy-ready public URL.
- **📁 Object browser** — navigate prefixes, upload and download objects, create folders, **bulk-delete** selections, and clean up orphaned **multipart uploads**.
- **🔗 Object sharing** — generate expiring **presigned links** for private objects and surface public website URLs, all from one dialog.
- **🔑 Access keys** — create, inspect, and assign keys to buckets with fine-grained read / write / owner permissions.
- **🔐 Authentication & roles** — optional session auth with bcrypt-hashed credentials, **multiple users**, and a fail-closed **read-only viewer role**.
- **📝 Audit log** — every state-changing request is emitted as a structured JSON line to stdout (who / what / path / status), including denied writes.
- **🎨 Themed UI** — 10 built-in light/dark themes, fully responsive down to mobile.
- **🚀 Production-ready** — multi-arch (amd64/arm64) image, non-root runtime, healthcheck, graceful shutdown, and GHCR publishing out of the box.

## 📸 Screenshots

<div align="center">

| Dashboard | Buckets |
|:---:|:---:|
| [![Dashboard](docs/screenshots/dashboard-light.png)](docs/screenshots/dashboard-light.png) | [![Buckets](docs/screenshots/buckets-light.png)](docs/screenshots/buckets-light.png) |
| **Object browser** | **Bucket overview** |
| [![Browse](docs/screenshots/browse-light.png)](docs/screenshots/browse-light.png) | [![Overview](docs/screenshots/bucket-overview-light.png)](docs/screenshots/bucket-overview-light.png) |
| **Access keys** | **Cluster & layout** |
| [![Keys](docs/screenshots/keys-light.png)](docs/screenshots/keys-light.png) | [![Cluster](docs/screenshots/cluster-light.png)](docs/screenshots/cluster-light.png) |

**Share / export dialog** — presigned private links + public website URL

[![Share](docs/screenshots/share-export.png)](docs/screenshots/share-export.png)

**Dark mode**

| Dashboard | Object browser |
|:---:|:---:|
| [![Dashboard dark](docs/screenshots/dashboard-dark.png)](docs/screenshots/dashboard-dark.png) | [![Browse dark](docs/screenshots/browse-dark.png)](docs/screenshots/browse-dark.png) |

**Mobile**

<img src="docs/screenshots/mobile-dashboard.png" width="220" /> <img src="docs/screenshots/mobile-buckets.png" width="220" /> <img src="docs/screenshots/mobile-browse.png" width="220" />

</div>

## 🏗️ Architecture

Garage WebUI-NG is a **stateless gateway** between your browser and two Garage APIs.

```
          ┌──────────────────────────────────────────────┐
          │            Garage WebUI-NG (1 binary)         │
 Browser ─┤  React SPA  ─►  Go server                     ├─► Garage Admin API (v2)
   (SPA)  │   (embedded)     ├─ session auth + roles      │      /v2/GetClusterStatus, …
          │                  ├─ audit log (stdout)        │
          │                  ├─ reverse proxy  ───────────┼─► Garage Admin API (catch-all)
          │                  └─ S3 client ────────────────┼─► Garage S3 API
          └──────────────────────────────────────────────┘      (object browse / share)
```

- **Single artifact** — the Go binary embeds the built frontend via `//go:embed` (release builds only); the same code runs from the non-root Docker image.
- **Admin API gateway** — a few explicit routes plus a catch-all reverse proxy forward any `/api/v2/…` request to Garage's Admin API with the admin token injected server-side. The token is **never** sent to the browser.
- **S3 path** — object browsing/upload/download/sharing uses the AWS SDK v2 with per-bucket credentials fetched (and briefly cached) from the Admin API.
- **Config reuse** — reads your existing `garage.toml` (`CONFIG_PATH`, default `/etc/garage.toml`) for endpoints and tokens; every value can be overridden by an environment variable.

See [CLAUDE.md](CLAUDE.md) for a deeper architecture reference.

## 🚀 Installation

### Docker (quickest)

```bash
docker run -d --name garage-webui-ng \
  -p 3909:3909 \
  -v ./garage.toml:/etc/garage.toml:ro \
  -e API_BASE_URL=http://garage:3903 \
  -e S3_ENDPOINT_URL=http://garage:3900 \
  --restart unless-stopped \
  ghcr.io/d7eeem/garage-webui-ng:latest
```

Then open **http://localhost:3909**.

### Prebuilt binary

Download the `linux/amd64` or `linux/arm64` binary from the [latest release](https://github.com/d7eeem/garage-webui-ng/releases) and run it next to your Garage node:

```bash
chmod +x garage-webui-ng-linux-amd64
API_BASE_URL=http://127.0.0.1:3903 S3_ENDPOINT_URL=http://127.0.0.1:3900 ./garage-webui-ng-linux-amd64
```

## 🐳 Docker Deployment

The image is multi-arch (`linux/amd64`, `linux/arm64`), runs as a **non-root** user, exposes a **healthcheck**, and shuts down gracefully on `SIGTERM`.

```bash
docker pull ghcr.io/d7eeem/garage-webui-ng:latest
```

Available tags: `latest`, `2`, `2.0`, `2.0.0`, and `sha-<commit>`.

## 🧩 Docker Compose Deployment

A production Compose stack (Garage + WebUI-NG) is provided. Copy the example env file and start it:

```bash
cp .env.example .env       # edit as needed
docker compose up -d
```

`docker-compose.yml` includes named volumes, restart policies, healthchecks, JSON log rotation, environment interpolation from `.env`, and optional Traefik reverse-proxy labels. See [`docker-compose.yml`](docker-compose.yml).

## ⚙️ Configuration

Garage WebUI-NG reads your `garage.toml` and lets every setting be overridden by an environment variable. A full reference lives in [`.env.example`](.env.example).

### Environment variables

| Variable | Default | Description |
|---|---|---|
| `API_BASE_URL` | from `garage.toml` | Garage **Admin API** endpoint (cluster/bucket/key management). |
| `S3_ENDPOINT_URL` | from `garage.toml` | Garage **S3 API** endpoint (object browse/upload/download). |
| `S3_PUBLIC_ENDPOINT_URL` | *(unset)* | Public S3 endpoint the browser can reach — enables **presigned share links**. |
| `S3_REGION` | `garage` | S3 region name. |
| `CONFIG_PATH` | `/etc/garage.toml` | Path to the Garage config file to read. |
| `HOST` | `0.0.0.0` | Address the server binds to. |
| `PORT` | `3909` | Port the server listens on. |
| `BASE_PATH` | *(unset)* | Mount the UI under a path prefix (e.g. `/garage`). |
| `AUTH_USER_PASS` | *(unset)* | `user:bcrypt-hash` (comma-separated for multiple users). Enables auth. |
| `AUTH_VIEWER_USER_PASS` | *(unset)* | Read-only viewer accounts, same format. |
| `SESSION_COOKIE_SECURE` | `false` | Send the session cookie only over HTTPS. |

> **Note:** if neither `AUTH_USER_PASS` nor `AUTH_VIEWER_USER_PASS` is set, the UI **and** the admin-token-injecting proxy are open — rely on network isolation, or enable authentication.

## 🖱️ Usage

1. Open the UI and land on the **Dashboard** for cluster health and live metrics.
2. Use **Cluster** to review nodes, capacity, and the layout.
3. Under **Buckets**, create a bucket, assign an alias, set quotas, or enable website hosting.
4. Open a bucket's **Browse** tab to manage objects; use **Keys** to mint and assign access keys.

### Importing / Exporting

- **Import (upload)** — open a bucket → **Browse** → the **upload** button (top-right of the toolbar) to add objects, or **new folder** to create a prefix.
- **Export (download / share)** — use the per-row **download** action, or the row menu → **Share** to generate an expiring **presigned link** (15 min → 7 days) or copy the object's **public website URL** when website hosting is enabled.
- **Bulk operations** — select multiple objects to delete them in one request.

## 🔌 API

The backend serves everything under `/api`. It is primarily a **gateway to Garage's Admin API v2** (any unmatched `/api/v2/…` request is reverse-proxied with the admin token attached), plus first-class endpoints:

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/api/config` | Browser-safe subset of the Garage config (no secrets). |
| `GET` | `/api/metrics` | Parsed Prometheus metrics for the dashboard panel. |
| `GET` | `/api/buckets` | Enriched bucket list. |
| `GET/PUT/DELETE` | `/api/browse/{bucket}/{key...}` | List / upload / download / delete objects. |
| `POST` | `/api/browse/{bucket}` | Bulk-delete selected objects. |
| `GET/DELETE` | `/api/multipart/{bucket}` | List / abort orphaned multipart uploads. |
| `GET` | `/api/share/{bucket}/{key...}?expires=` | Generate a presigned share link. |
| `POST` | `/api/auth/login` · `/api/auth/logout` · `GET /api/auth/status` | Session auth. |

## 🔒 Security

- **Secrets stay server-side** — `rpc_secret`, `admin_token`, and `metrics_token` are never exposed to the browser (`/api/config` returns a filtered subset).
- **Optional authentication** — session-based, bcrypt-hashed credentials via `AUTH_USER_PASS`; login is rate-limited and renews the session token.
- **Read-only viewer role** — `AUTH_VIEWER_USER_PASS` grants a fail-closed role that can view but not mutate (and cannot reveal secret keys).
- **Audit trail** — mutating requests (incl. denied ones) are logged as structured JSON to stdout for your log pipeline.
- **Hardened runtime** — the Docker image runs as a non-root user with a minimal (distroless) base and no shell.

> Because the open (no-auth) mode proxies the Garage admin token, **always** enable authentication or restrict network access when exposing the UI beyond localhost.

## 🗺️ Roadmap

- [x] Live cluster metrics panel
- [x] Bulk object delete & multipart cleanup
- [x] Presigned share links & correct website URLs
- [x] Multi-user auth + read-only viewer role
- [x] Structured audit log
- [ ] **Full in-browser multipart upload** (resumable, large-file uploads)
- [ ] Richer static-website hosting management surface
- [ ] Historical metrics (time-series) view

## ❓ FAQ

**Does this replace Garage?** No — it's an admin UI for an existing Garage cluster. [Install Garage](https://garagehq.deuxfleurs.fr/documentation/quick-start/) first.

**Do I need to run it inside Docker?** No. It's a single static binary; Docker is just the easiest deployment.

**A bucket won't browse — why?** Object browsing addresses buckets by **global alias**. Add a global alias to the bucket first.

**How do I generate a bcrypt hash for `AUTH_USER_PASS`?**
```bash
htpasswd -bnBC 10 "" 'your-password' | tr -d ':\n' | sed 's/^$2y/$2a/'
```

**Why is lint "red" in CI?** A pre-existing lint backlog is tracked separately and runs non-blocking; new code is kept clean.

## 🤝 Contributing

Contributions are welcome! Please open an issue to discuss substantial changes first. Before submitting a PR, make sure `pnpm run typecheck`, `pnpm run test`, `pnpm run build`, and the backend `go build ./... && go vet ./... && go test -race ./...` all pass.

## 🛠️ Development

**Prerequisites:** Node 20+ with `pnpm` (via `corepack enable`) and Go 1.24+.

```bash
pnpm install
pnpm run dev          # Vite (frontend) + air (backend) together
# or separately:
pnpm run dev:client   # frontend only
pnpm run dev:server   # backend only (cd backend && air)
```

**Useful scripts:**

```bash
pnpm run build        # tsc -b && vite build → dist/
pnpm run typecheck    # tsc -b
pnpm run test         # Vitest
pnpm run lint         # ESLint

cd backend
go build ./... && go vet ./... && go test -race ./...
```

A **release build** (single binary with the embedded UI) is produced by copying `dist/` into `backend/ui/dist/` and running `make` (`-tags=prod`); the [`Dockerfile`](Dockerfile) does this automatically. See [CLAUDE.md](CLAUDE.md) for conventions and architecture notes.

## 📄 License

Released under the [MIT License](LICENSE). Garage WebUI-NG is a next-generation fork; the original copyright is retained below.

## 🙏 Acknowledgements

- **[Garage](https://garagehq.deuxfleurs.fr/)** by [Deuxfleurs](https://deuxfleurs.fr/) — the object storage engine this UI operates.
- **[garage-webui](https://github.com/khairul169/garage-webui)** by **Khairul Hidayat** (© 2024) — the original project this "-NG" edition builds upon, under the MIT License.
