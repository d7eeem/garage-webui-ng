# Plan 016: Live cluster metrics panel (D1)

> **Executor instructions**: Follow step by step. Run every verification command
> and confirm the expected result before moving on. Touch only in-scope files.
> On a STOP condition, stop and report. SKIP updating `plans/README.md`.
>
> **Base reset FIRST**: `git checkout -B advisor/016-cluster-metrics-panel main`
> then `git log --oneline -1` — MUST show `bcedef0` or a "Merge branch
> 'advisor/015" commit, NOT `ee420fb`. If wrong, STOP.

## Status

- **Priority**: P3 (feature)
- **Effort**: M
- **Risk**: LOW (read-only; a new page section + one GET endpoint)
- **Depends on**: none
- **Category**: direction / feature
- **Planned at**: commit `bcedef0` (main after 015), 2026-08-03

## Why this matters

`metrics_token` is parsed from `garage.toml` but the UI uses it nowhere — the home
dashboard shows only cluster **health** (up/down counts), never throughput,
request rates, or I/O. Garage exposes a Prometheus `/metrics` endpoint (44 metric
families) guarded by that token. This adds a **live cluster metrics panel** to the
dashboard: current values + small live charts (recharts) of the last N polls.

**Honest constraint (do not try to exceed it):** the backend is **stateless** —
no database. Garage's `/metrics` is point-in-time. So this panel shows **live,
current values** and charts only the polls collected **while the page is open**
(held in browser memory). It is NOT historical time-series; do not add persistence
or a scraper. `GetClusterStatistics` was checked and returns only a human-readable
`freeform` text blob — **not** a usable data source; use `/metrics`.

## Current state

### `src/pages/home/page.tsx` — the dashboard (append a panel below the stats grid)

It renders `<Page title="Dashboard" />` then a `<section>` grid of `<StatsCard>`
components fed by `useNodesHealth()` and `useBuckets()`. The new metrics panel goes
**after** that `</section>`, inside the `.container` div.

### `backend/utils/garage.go` — `Fetch` uses the ADMIN token; metrics needs the METRICS token

```go
func (g *garage) Fetch(url string, options *FetchOptions) ([]byte, error) {
	reqUrl := fmt.Sprintf("%s%s", g.GetAdminEndpoint(), url)
	// ...
	req.Header.Add("Authorization", fmt.Sprintf("Bearer %s", g.GetAdminKey()))
	// ...
}
```

`Fetch` hardcodes the admin token, so it can't fetch `/metrics` (which uses
`metrics_token`, in `g.Config.Admin.MetricsToken`). Add a dedicated `FetchMetrics`.
A shared `adminHTTPClient` (with a 30s timeout) already exists in this file from an
earlier plan — reuse it. `net/http`, `fmt`, `io`, `errors` are imported.

### `backend/router/router.go`

Explicit routes then `router.HandleFunc("/", ProxyHandler)` (catch-all). Register
`GET /metrics` explicitly (it wins over the catch-all by specificity). This is the
**webui's** `/metrics` (the frontend calls `/api/metrics`); it is distinct from
Garage's `/metrics`, which `FetchMetrics` reads server-side.

### Conventions

- Backend handlers: methods on an empty struct, `utils.ResponseSuccess`/`ResponseError`
  (always `return` after error). One domain per file in `backend/router/`.
- Frontend: TanStack Query hooks in a page's `hooks.ts`; `api.get`; daisyUI +
  `@/components/ui/*`; the home page uses `StatsCard` (`src/pages/home/components/stats-card.tsx`).
  The `@/` alias maps to `src/`.

## Commands

`pnpm` not installed → `npx pnpm@9 <cmd>` (run `npx pnpm@9 install` first).
Add recharts with `npx pnpm@9 add recharts` (a real runtime dependency — the
maintainer chose it).

| Purpose | Command | Expected |
|---|---|---|
| Go build/vet/fmt | `cd backend && go build ./... && go vet ./... && gofmt -l .` | exit 0, no output |
| Go tests | `cd backend && go test -race ./...` | `ok` |
| Typecheck | `npx pnpm@9 run typecheck` | exit 0 |
| Frontend test | `npx pnpm@9 run test` | all pass |
| Build | `npx pnpm@9 run build` | exit 0 |

## Scope

**In scope**:
- `backend/utils/garage.go` (add `FetchMetrics`)
- `backend/router/metrics.go` (create — handler + Prometheus parser)
- `backend/router/router.go` (register `GET /metrics`)
- `backend/router/metrics_test.go` (create — test the parser)
- `package.json` + `pnpm-lock.yaml` (add recharts)
- `src/pages/home/hooks.ts` (add `useClusterMetrics`)
- `src/pages/home/types.ts` (metrics response type)
- `src/pages/home/components/metrics-panel.tsx` (create — the panel + charts)
- `src/pages/home/page.tsx` (render the panel)

**Out of scope**: persistence/history, a Prometheus scraper, editing the existing
StatsCard grid, the `metrics_token` secret exposure (it stays server-side — never
send it to the browser).

## Steps

### Step 1: Backend — `FetchMetrics`

In `garage.go`, add:

```go
// FetchMetrics fetches Garage's Prometheus /metrics endpoint using the
// metrics_token (distinct from the admin token). Returns the raw text body.
func (g *garage) FetchMetrics() ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, g.GetAdminEndpoint()+"/metrics", nil)
	if err != nil {
		return nil, err
	}
	if t := g.Config.Admin.MetricsToken; t != "" {
		req.Header.Set("Authorization", "Bearer "+t)
	}
	res, err := adminHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("metrics endpoint returned status %d (is metrics_token set?)", res.StatusCode)
	}
	return io.ReadAll(res.Body)
}
```

**Verify**: `cd backend && go build ./...` → exit 0.

### Step 2: Backend — parser + handler

Create `backend/router/metrics.go`:

```go
package router

import (
	"khairul169/garage-webui/utils"
	"net/http"
	"strconv"
	"strings"
)

type Metrics struct{}

// curatedMetrics are the Prometheus metric family names surfaced to the UI.
var curatedMetrics = []string{
	"api_s3_request_counter",
	"api_s3_error_counter",
	"block_bytes_read",
	"block_bytes_written",
}

// parsePromMetrics sums each wanted metric family across all its label sets,
// producing one number per family. Prometheus text format: comment lines start
// with '#'; data lines are `name{labels} value` or `name value`.
func parsePromMetrics(body []byte, want []string) map[string]float64 {
	wantSet := make(map[string]bool, len(want))
	for _, w := range want {
		wantSet[w] = true
	}
	out := make(map[string]float64, len(want))
	for _, w := range want {
		out[w] = 0 // ensure every requested key is present, even if absent upstream
	}
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name := line
		if i := strings.IndexAny(line, "{ "); i >= 0 {
			name = line[:i]
		}
		if !wantSet[name] {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		if v, err := strconv.ParseFloat(fields[len(fields)-1], 64); err == nil {
			out[name] += v
		}
	}
	return out
}

// GET /metrics — curated, JSON-shaped subset of Garage's Prometheus metrics.
func (m *Metrics) Get(w http.ResponseWriter, r *http.Request) {
	body, err := utils.Garage.FetchMetrics()
	if err != nil {
		utils.ResponseError(w, err)
		return
	}
	utils.ResponseSuccess(w, parsePromMetrics(body, curatedMetrics))
}
```

**Verify**: `cd backend && go build ./... && go vet ./... && gofmt -l .` → clean.

### Step 3: Register the route

In `router.go`, add before the catch-all:

```go
	metrics := &Metrics{}
	router.HandleFunc("GET /metrics", metrics.Get)
```

**Verify**: `cd backend && go build ./...` → exit 0; `grep -c "GET /metrics" backend/router/router.go` = 1.

### Step 4: Parser test

Create `backend/router/metrics_test.go`, `package router`. `TestParsePromMetrics`:
feed a small Prometheus text fixture with comment lines, a family with two label
sets (assert they SUM), an unwanted family (assert ignored), and a `name value`
line without labels; assert the returned map has every curated key present (0 when
absent) and the summed values. Table/stdlib style, matching `backend/utils/utils_test.go`.

**Verify**: `cd backend && go test -race ./router/... -run TestParsePromMetrics -v` → PASS.

### Step 5: Add recharts + the hook/types

```
npx pnpm@9 add recharts
```

`src/pages/home/types.ts` — add:
```ts
export type ClusterMetrics = Record<string, number>;
```

`src/pages/home/hooks.ts` — add (polls every 5s for the "live" feel):
```ts
export const useClusterMetrics = () => {
  return useQuery({
    queryKey: ["cluster-metrics"],
    queryFn: () => api.get<ClusterMetrics>("/metrics"),
    refetchInterval: 5000,
    retry: false,
  });
};
```

Import `ClusterMetrics` and ensure `useQuery`/`api` imports exist.

**Verify**: `npx pnpm@9 run typecheck` → exit 0.

### Step 6: The metrics panel component

Create `src/pages/home/components/metrics-panel.tsx`:

- Calls `useClusterMetrics()`.
- Keeps an in-memory history: a `useState<{ t: number } & ClusterMetrics>` array,
  appended each time `data` changes (cap to the last ~30 entries). This is the
  chart source; it resets on unmount (stateless, by design).
- Renders a titled `Card` ("Cluster Metrics (live)"). If the query errors (e.g.
  metrics_token not set), show a small muted note ("Metrics unavailable — set
  `metrics_token` in garage.toml") instead of charts. If loading, a skeleton/nothing.
- For each curated metric, a small stat (current value; `readableBytes` for the
  `block_bytes_*` ones) plus a recharts `<LineChart>` sparkline of the in-memory
  history (`<ResponsiveContainer>` + `<Line>` + hidden axes; keep it compact).
- Use recharts imports: `LineChart, Line, ResponsiveContainer, Tooltip, XAxis, YAxis`.
  Keep axes minimal (`hide`) for a sparkline look.

Keep styling consistent with daisyUI `Card` usage elsewhere (see
`src/pages/buckets/manage/overview/overview-tab.tsx`).

**Verify**: `npx pnpm@9 run typecheck && npx pnpm@9 run build` → exit 0. (recharts
must resolve; if the build complains about ESM/CJS interop, that's a real issue —
see STOP conditions.)

### Step 7: Render it on the dashboard

In `src/pages/home/page.tsx`, import `MetricsPanel` and render it after the
`</section>` (the stats grid), still inside `.container`:

```tsx
      <MetricsPanel />
```

**Verify**: `npx pnpm@9 run typecheck && npx pnpm@9 run lint && npx pnpm@9 run build` →
typecheck & build exit 0; lint red only on the pre-existing backlog (confirm
`metrics-panel` / `home/hooks` add no NEW errors).

### Step 8: Full gate sweep

```
cd backend && go build ./... && go vet ./... && test -z "$(gofmt -l .)" && go test -race ./...
npx pnpm@9 run typecheck && npx pnpm@9 run test && npx pnpm@9 run build
```
All exit 0.

## Test plan

- **Go**: `TestParsePromMetrics` covers the parser (the real logic — label
  summing, comment skipping, absent-key-as-zero). `FetchMetrics` and the handler
  are HTTP plumbing (no brittle mock).
- **Frontend**: typecheck + build gate the wiring and recharts resolution. No
  component test required.
- **Live verification is the reviewer's job**: run the backend against Garage,
  `GET /api/metrics` → JSON with the 4 curated keys and plausible numbers; open
  the dashboard and confirm the panel renders and the sparklines populate over a
  few polls; confirm the graceful message when `metrics_token` is unset.

## Done criteria

- [ ] `cd backend && go build ./... && go vet ./... && test -z "$(gofmt -l .)" && go test -race ./...` all exit 0
- [ ] `npx pnpm@9 run typecheck && npx pnpm@9 run test && npx pnpm@9 run build` all exit 0
- [ ] `grep -n "FetchMetrics" backend/utils/garage.go` and `grep -n "parsePromMetrics" backend/router/metrics.go` match
- [ ] `grep -n "recharts" package.json` matches (dependency added; lockfile updated)
- [ ] `grep -rn "metrics_token\|MetricsToken" src/` returns **nothing** (the token never reaches the browser)
- [ ] `git diff --name-only bcedef0..HEAD` shows only the in-scope files (plus `plans/README.md`)
- [ ] `plans/README.md` row for 016 updated

## STOP conditions

- Base reset shows `ee420fb`.
- Current-state excerpts don't match live code.
- `npx pnpm@9 add recharts` fails or recharts can't be imported/built under Vite 5 —
  report the error rather than swapping to another chart lib (the maintainer chose
  recharts specifically).
- Garage's `/metrics` requires a different auth scheme than `metrics_token` Bearer,
  or the parser can't find the curated families in the reviewer's live check —
  report the actual `/metrics` sample.

## Maintenance notes

- **`metrics_token` stays server-side.** The browser only ever sees the parsed
  numbers via `/api/metrics`; never expose the token (the done-criteria grep guards
  this — the same discipline as plan 001's secret projection).
- **Live-only, by design.** Charts show data since the page opened. If historical
  metrics are ever wanted, that's a separate, larger project (persistence + a
  scraper) — not an extension of this panel.
- **Curated metric list** is deliberately small (4 families). Adding more is a
  one-line change to `curatedMetrics`; keep it curated rather than dumping all 44.
- **recharts is the first charting dependency.** A reviewer should note the bundle
  impact; if it's a concern later, the sparklines could be hand-rolled SVG, but the
  maintainer opted for recharts here.
