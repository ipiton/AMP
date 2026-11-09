# Alertmanager++ (AMP)

> Upstream Prometheus Alertmanager, with alert history, LLM classification and investigation, Kubernetes-Secret notification targets and HA, in one binary.

[![CI](https://github.com/ipiton/AMP/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/ipiton/AMP/actions/workflows/ci.yml)
[![License](https://img.shields.io/badge/License-AGPL%203.0-blue.svg)](LICENSE)
[![Go Report Card](https://goreportcard.com/badge/github.com/ipiton/AMP)](https://goreportcard.com/report/github.com/ipiton/AMP)

AMP's alerting core is not a reimplementation: the `amp-upstream` binary is built from the packages of [Prometheus Alertmanager](https://github.com/prometheus/alertmanager) **v0.34.1** (`config`, `dispatch`, `notify`, `silence`, `inhibit`, `nflog`, `provider/mem`, `cluster`, `api/v2`). Routing, grouping, silences, inhibition, time intervals, all 18 receiver integrations, templates and the `api/v2` handlers are upstream's code, pinned to that exact version. AMP adds a layer around it ([ADR-019](docs/06-planning/DECISIONS.md)).

## Features

- **Upstream Alertmanager core**: your `alertmanager.yml` as it is, `amtool`, the Grafana Alertmanager data source and [karma](https://github.com/prymitive/karma) work as with upstream. What differs is listed in [docs/ALERTMANAGER_COMPATIBILITY.md](docs/ALERTMANAGER_COMPATIBILITY.md).
- **Alert history** in PostgreSQL: every alert the core accepts, firing and resolved, silenced and inhibited ones included, written off the ingest path. Delivery does not depend on PostgreSQL.
- **LLM classification**: each firing alert is classified asynchronously; a notification waits up to `llm.notify_wait` (default 5s) and carries the result in `llm_*` annotations, so templates can use it. Optional: drop alerts classified as noise (`llm.drop_noise`, off by default). Bring your own OpenAI-compatible endpoint and key.
- **Investigation**: an LLM investigation per firing incident, optionally agentic with Prometheus, Loki, Kubernetes and PostgreSQL tools and runbooks.
- **Kubernetes-Secret targets**: Secrets labelled `publishing-target=true` become webhook, Slack or PagerDuty integrations of your receivers, without putting credentials in `alertmanager.yml`.
- **HA**: upstream's memberlist gossip between replicas; silences and the notification log are replicated and snapshotted to disk.
- **API**: upstream `/api/v2` unchanged, plus AMP's routes under `/api/amp/v1/` (alert history and investigation by fingerprint).

AMP v0.1.0 has **no web UI of its own** (planned after the release, `UPSTREAM-UI`), and the upstream UI is not part of the Go module, so it is not served either. Use karma, Grafana or `amtool` to look at alerts and silences.

Status: AMP is a pilot-stage project; v0.1.0 is its first tagged release.

## Quick Start

### Helm

Put your `alertmanager.yml` under `alertmanager.config` in a values file:

```yaml
# values.yaml
alertmanager:
  config:
    route:
      receiver: team
      group_by: [alertname]
    receivers:
      - name: team
        webhook_configs:
          - url: http://hooks.example.internal/alerts
```

```bash
helm install amp ./helm/amp -n monitoring --create-namespace -f values.yaml
```

The defaults run one replica with a bundled PostgreSQL (dev/pilot only, no backups) and LLM off. Production values, external PostgreSQL, authentication and HA: [helm/amp/README.md](helm/amp/README.md).

Point Prometheus at AMP as at any Alertmanager:

```yaml
alerting:
  alertmanagers:
    - static_configs:
        - targets: ["amp.monitoring.svc:9093"]   # with replicas > 1: every pod, see the chart README
```

### Docker

```bash
docker run -p 9093:9093 -v "$PWD/alertmanager.yml:/etc/amp/alertmanager.yml:ro" ghcr.io/ipiton/amp:0.1.0
```

Without `--amp.config.file` the container is plain upstream Alertmanager (no history, classification or K8s targets). Images (`linux/amd64`, `linux/arm64`) are published to GHCR by tagged releases; until `v0.1.0` is tagged, build locally with `docker build -t amp .` ([docs/CI.md](docs/CI.md)).

### From source

```bash
git clone https://github.com/ipiton/AMP.git
cd AMP/go-app
make build
./amp-upstream --config.file=alertmanager.yml --storage.path=data/
```

## Configuration

Two files:

| File | Flag | Contents |
|---|---|---|
| `alertmanager.yml` | `--config.file` | Upstream Alertmanager configuration: `global`, `route`, `receivers`, `inhibit_rules`, `time_intervals`, `templates`. Read with upstream's parser. |
| AMP configuration (`amp.yaml`) | `--amp.config.file` | `profile`, `database`, `llm`, `investigation`, `publishing` (K8s-Secret targets), `app`, `server`. Optional. |

```yaml
# amp.yaml
profile: standard          # lite: no PostgreSQL, so no history, investigations or K8s targets
database:
  host: postgres.example.internal
  database: alerthistory
  username: amp            # password: DATABASE_PASSWORD
llm:
  enabled: true
  provider: openai
  base_url: https://api.openai.com/v1
  model: gpt-4o-mini       # key: LLM_API_KEY
investigation:
  enabled: true
```

All keys: [config.yaml.example](config.yaml.example) and [docs/CONFIGURATION_GUIDE.md](docs/CONFIGURATION_GUIDE.md). Other flags are upstream's (`--web.listen-address`, `--web.external-url`, `--web.config.file`, `--data.retention`, `--cluster.*`); `amp-upstream --help` lists them.

## API

| Path | What |
|---|---|
| `/api/v2/*` | Upstream Alertmanager API v2, unchanged |
| `/-/healthy`, `/-/ready`, `POST /-/reload` | Upstream probes and reload |
| `/metrics` | Upstream `alertmanager_*` metrics plus AMP's (`amp_*`) |
| `GET /api/amp/v1/alerts/{fingerprint}` | The alert's history record, also after it resolved |
| `GET /api/amp/v1/alerts/{fingerprint}/investigation` | Its latest investigation |

`{fingerprint}` is the one `api/v2` reports. All paths sit under the path of `--web.external-url`, behind the same basic auth (`--web.config.file`). Details: [docs/ALERTMANAGER_COMPATIBILITY.md](docs/ALERTMANAGER_COMPATIBILITY.md#amp-api).

## Documentation

- [Alertmanager compatibility](docs/ALERTMANAGER_COMPATIBILITY.md): what is upstream, what AMP adds, the differences
- [Configuration guide](docs/CONFIGURATION_GUIDE.md): the AMP configuration file, the chart values that generate it
- [Migrating from Alertmanager](docs/MIGRATION_QUICK_START.md) and the [rollout plan](docs/ROLLOUT_PLAN.md)
- [Upgrading from AMP's own core (`cmd/server`)](docs/MIGRATION_FROM_LEGACY_CORE.md)
- [Rollback runbook](docs/ROLLBACK_RUNBOOK.md)
- [Helm chart](helm/amp/README.md)
- [CI and image publishing](docs/CI.md), [upstream version bumps](docs/UPSTREAM_UPGRADE.md)
- [Security policy](SECURITY.md)

## Security

- HTTP basic auth via the upstream `--web.config.file` format (off by default); probes stay open.
- No TLS termination in AMP: terminate TLS at the ingress or service mesh.
- `/debug/*` (pprof, `expvar`) is served on the API port, as upstream: keep the port off public networks and do not pass secrets in flags.
- Namespaced, least-privilege RBAC and an optional NetworkPolicy in the Helm chart; `govulncheck` on every pull request.

See [SECURITY.md](SECURITY.md).

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) and [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md). Before a pull request, run `./scripts/release-gate.sh`; CI runs the same gate ([docs/CI.md](docs/CI.md)).

## License

AGPL 3.0, see [LICENSE](LICENSE). The upstream Alertmanager code AMP uses and ports is Apache-2.0, by The Prometheus Authors (`go-app/internal/upstreamcore/doc.go`).

## Acknowledgments

The Prometheus Alertmanager team, whose code is AMP's core.
