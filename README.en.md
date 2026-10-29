# Gateway Manager (gateway-manager) v2.2.0

[中文](README.md) | **English**

[![ci](https://github.com/fengye102/Gateway-Manager/actions/workflows/ci.yml/badge.svg)](https://github.com/fengye102/Gateway-Manager/actions/workflows/ci.yml)
[![release](https://img.shields.io/github/v/release/fengye102/Gateway-Manager?color=blue)](https://github.com/fengye102/Gateway-Manager/releases/latest)
[![license](https://img.shields.io/badge/license-MIT-green.svg)](LICENSE)
[![go](https://img.shields.io/badge/go-1.21%2B-00ADD8)](go.mod)
[![docker](https://img.shields.io/badge/docker-compose%20ready-2496ED?logo=docker&logoColor=white)](docker-compose.yml)

**A single-binary, dependency-free local AI gateway.** It speaks OpenAI and Anthropic
protocols, aggregates many upstream gateways behind one endpoint, injects strategy
(system prompt) text on the way through, and gives you the operational pieces a real
deployment needs: health probes, key pools, circuit breaking, budgets, observability,
snapshots, A/B experiments and federation.

Built with Go standard library only — no third-party modules, no cgo, one static binary
for Windows / Linux / macOS / Docker.

## Why

Wiring several local model gateways together is easy; running them **safely** is not.
gateway-manager gives you a single stable endpoint (`http://127.0.0.1:7878/v1`) plus the control
plane around it:

- one endpoint, many upstreams (priority, weights, canary, mirror)
- per-key health scoring, automatic key rotation, circuit breaker with backoff
- deterministic prompt/strategy injection for chat / responses / messages shapes
- a rules engine (drag & drop UI **and** a text DSL) with regex conditions
- first-class observability: 150+ Prometheus series, histograms, Grafana dashboard, 9 alerts
- state that survives restarts, config that can be rolled back (and git-versioned)

## Quick start

### Windows

1. Download `gateway-manager-<version>-windows-amd64.zip` from [Releases](https://github.com/fengye102/Gateway-Manager/releases/latest)
2. **Right-click the zip → Properties → Unblock** (downloaded binaries trip SmartScreen)
3. Extract, then double-click `gateway-manager-windows-amd64.exe` (or `start-windows.bat`)
   - first run auto-creates `config.json` next to the exe and starts listening on `127.0.0.1:7878`
   - the console opens `/ui` automatically; errors keep the window open ("press Enter to exit")

| Symptom | Fix |
| --- | --- |
| window flashes and closes | fixed in v2.2.0 (auto-init + stays open); older builds: use `start-windows.bat` |
| "Windows protected your PC" | Unblock the file, or More info → Run anyway |
| port 7878 busy | `serve -config config.json -listen 127.0.0.1:8787` |
| antivirus warning | unsigned Go binary false positive; add exclusion or build locally |

### Docker (recommended)

```bash
git clone https://github.com/fengye102/Gateway-Manager.git && cd Gateway-Manager
docker compose up -d          # gateway-manager + Prometheus + Grafana
# UI        http://127.0.0.1:7878/ui
# metrics   http://127.0.0.1:7878/metrics
# Grafana   http://127.0.0.1:3000   (admin/admin)
```

```bash
docker build -t gateway-manager .
docker run -d --name gateway-manager -p 7878:7878 -v "$PWD/data:/data" gateway-manager
```

### Binary

```bash
# Linux / macOS
curl -LO https://github.com/fengye102/Gateway-Manager/releases/latest/download/gateway-manager-2.2.0-linux-amd64.tar.gz
chmod +x gateway-manager-linux-amd64 && ./gateway-manager-linux-amd64 init -config config.json
./gateway-manager-linux-amd64 serve -config config.json
```

```powershell
# Windows
.\gateway-manager.exe init -config config.json
.\gateway-manager.exe serve -config config.json
```

### One-liner installers

```bash
sudo ./install.sh                                   # /opt/gateway-manager + systemd
sudo PORT=7878 BIND=0.0.0.0 ACCESS_KEY=sk-local ./install.sh
sudo ./install.sh --uninstall
```

```powershell
.\install.ps1 -Port 7878 -AccessKey sk-local -AdminToken admin -Service
.\install.ps1 -Uninstall
```

## Endpoints

| Path | Behaviour |
| --- | --- |
| `GET /` | service info |
| `GET /health` | liveness (no auth) |
| `GET /metrics` | Prometheus exposition (token required when `admin_token` set) |
| `GET /ui` | web console |
| `GET /v1/models` | aggregated models + aliases |
| `POST /v1/chat/completions` | OpenAI chat, injects `messages[0] = {role:"system", content:<strategy>}` |
| `POST /v1/responses`, `/v1/responses/compact` | injects `instructions` |
| `POST /v1/messages` | injects `system` (string concat or array prepend) |

Paths without `/v1` are normalised automatically. Errors use
`{"error":{"message":"...","type":"proxy_error"}}`.

## Channels

Built-in reverse-proxy channels (base URL / path / auth / extra headers ported from the
original app's network layer). Fill in a credential and it becomes an upstream (`ch-<id>`):

| preset | vendor | auth | status |
| --- | --- | --- | --- |
| `opencode-zen` | OpenCode Zen (anonymous) | Bearer `public` | ✅ OpenAI-compatible |
| `zhipu-autoclaw` | Zhipu AutoGLM | `X-Authorization: Bearer` | ✅ OpenAI-compatible |
| `workbuddy` | WorkBuddy / CodeBuddy | Cookie | ✅ OpenAI-compatible |
| `raccoon` | SenseTime Raccoon | `X-Auth-Token` | ✅ OpenAI-compatible |
| `xunfei-loomy` | iFlytek Loomy | query `token` | ✅ OpenAI-compatible |
| `deepseek-web`, `tabbit`, `trae`, `catpaw`, `dangbei`, `runable`, `meoo`, `wenxin` | — | — | ⚠️ endpoint registered, protocol adapter pending |

Manage them under **Channels** in the web console (`GET/POST/DELETE /admin/api/channels`,
`POST /admin/api/channels/test`). Credentials stay in your local `config.json`; the API only
returns masked values.

## Configuration (essentials)

Full reference: [README.md](README.md) (Chinese, 35 sections) and `config.example.json`.

```json
{
  "server": {
    "bind": "127.0.0.1", "port": 7878,
    "access_keys": [], "admin_token": "",
    "merge_sse": true, "metrics_enabled": true,
    "key_strategy": "score", "key_cooldown_seconds": 60,
    "persist_state": true, "snapshot_before_save": true, "snapshot_git": false
  },
  "retry": {
    "max_retries": 2, "failure_threshold": 2, "cooldown_seconds": 60,
    "half_open_limit": 1, "cooldown_max_seconds": 1800, "request_timeout_seconds": 180
  },
  "upstreams": [
    { "id": "local-gw", "name": "local gateway", "base_url": "http://127.0.0.1:8080",
      "api_keys": [], "models": [], "priority": 0, "weight": 0, "enabled": true,
      "probe": { "path": "/healthz", "expect_status": [200] },
      "budget": { "max_requests_per_day": 2000, "max_tokens_per_day": 500000 } }
  ],
  "profiles": [
    { "id": "default", "name": "default strategy", "models": [], "instruction": "...", "enabled": true }
  ],
  "rules": [], "templates": [], "adapters": [], "experiments": [],
  "federation": { "enabled": false, "peers": [] }
}
```

| Field | Meaning |
| --- | --- |
| `access_keys` | client keys for the proxy API (`Authorization: Bearer` / `x-api-key`); empty = open |
| `admin_token` | token for `/ui`, `/admin/api/*` and `/metrics` |
| `upstreams[].api_keys` | **key pool**, picked by health score, rotated on 401/403/429 |
| `upstreams[].mappings` | alias → real model name |
| `upstreams[].weight` | weighted split inside the same priority (canary) |
| `upstreams[].probe` | custom health probe (path / method / expect status / expect body) |
| `upstreams[].budget` | daily request / token budget (tokens are reconciled from `usage`) |
| `profiles[].models` | substring match; empty = fallback |
| `rules` | ordered rule chain (see below) |
| `adapters` | per-vendor path / auth header / extra headers |
| `experiments` | weighted arms, each with own profile and upstream |
| `federation.peers` | other gateway-manager instances (read-only status, or `as_upstream: true`) |

## Rules engine

Five node types, evaluated in order: `match` · `inject` · `override` · `header` · `deny`.

Conditions support plain fields (`models`, `paths`, `header:Key=Value`, `body_contains`)
and an expression language:

```yaml
when:
  expr: model ~ "^danger-" and path == "/v1/chat/completions"
```

Operators: `==` `!=` `~` (regex) `!~` `contains`, combined with `and` / `or` / `!` / `(...)`.
The same rules can be edited as a text DSL (`GET|POST /admin/api/rules/dsl`) with
line-numbered parse errors, and re-run over stored requests via `POST /admin/api/rules/batch`.

## Observability

- `/metrics`: request/upstream/key/rule/budget/snapshot/mirror series, four histograms
  (`request_duration`, `upstream_duration`, `queue`, `upstream_ttfb`, `relay`)
- `deploy/grafana-dashboard.json`: 12-panel dashboard (import or use `docker compose`)
- `deploy/prometheus-alerts.yml`: 9 alerts (upstream down, all keys cooling, p95 latency,
  deny spike, auth burst, stale state…)
- `deploy/prometheus.yml`: minimal scrape config

## Operations

| Feature | How |
| --- | --- |
| state persistence | `server.persist_state` + `-state` file; stores key fingerprints (never plaintext) and rule hit counters |
| config snapshots | `/admin/api/snapshots` — auto snapshot before every config/rules/template change, one-click rollback |
| snapshot git | `server.snapshot_git: true` commits each snapshot into a local git repo (`/admin/api/snapshots/git/log`) |
| blocked replay | recent denied requests (headers redacted) + re-run against current rules |
| sampling | every N-th successful request kept in memory for regression |
| A/B experiments | weighted arms with own profile/upstream, per-arm latency & token stats |
| mirroring | shadow-copy a percentage of traffic to another upstream |
| federation | aggregate several instances; optionally route to peers as upstreams |
| budgets | daily request/token caps per upstream, reconciled with real `usage` |

## Build & test

```bash
go vet ./...          # clean
go test ./... -v      # 47/47 passing
./build.ps1           # Windows: 7 targets
./build.sh            # Linux/macOS: same targets
```

CI runs on ubuntu / windows / macOS (gofmt + vet + test + build) plus a cross-compile job
and a Docker build job.

## Repository layout

```
main.go config.go strategy.go rules.go rules_dsl.go expr.go keys.go histogram.go
server.go state.go models.go health.go discover.go usage.go federation.go
snapshots.go git.go metrics.go webui.go webui_handlers.go templates.go gateway_test.go
install.sh install.ps1 build.sh build.ps1 Dockerfile docker-compose.yml
deploy/            systemd unit, Windows service script, Grafana dashboard, Prometheus config + alerts
docker/            container entrypoint, Prometheus/Grafana provisioning
```

## License & scope

MIT. This repository contains only original code for a local gateway. It ships no
third-party binaries or assets, and it intentionally does **not** implement vendor account
automation (cookie harvesting / private API impersonation) — point it at upstreams you are
allowed to use.
