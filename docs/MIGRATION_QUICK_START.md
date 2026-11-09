# Quick Start: Migrating from Alertmanager

AMP's alerting core is upstream Alertmanager v0.34.1 ([ALERTMANAGER_COMPATIBILITY.md](ALERTMANAGER_COMPATIBILITY.md)), so moving from Alertmanager means moving your `alertmanager.yml` and silences, not rewriting them. For a staged move of live traffic, see [ROLLOUT_PLAN.md](ROLLOUT_PLAN.md). Upgrading an AMP release that still runs AMP's own core (port 8080, `cmd/server`): [MIGRATION_FROM_LEGACY_CORE.md](MIGRATION_FROM_LEGACY_CORE.md).

## 1. Check the configuration

AMP reads `alertmanager.yml` with upstream's parser. If your Alertmanager is older than v0.34, check the file against the version AMP carries:

```bash
amtool check-config alertmanager.yml
```

Use `amtool` v0.34.x. Keys removed upstream before v0.34 fail here as they would on upstream v0.34.

## 2. Deploy AMP

### Helm

```yaml
# values.yaml
alertmanager:
  config:            # your alertmanager.yml, as YAML
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
kubectl -n monitoring rollout status statefulset/amp
```

The defaults: one replica, the bundled PostgreSQL (dev/pilot only), LLM off, no authentication. Production values, external PostgreSQL, LLM, basic auth and HA: [helm/amp/README.md](../helm/amp/README.md). Receiver secrets: mount them with `extraVolumes`/`extraVolumeMounts` and use the `*_file` fields.

### Docker

```bash
docker run -d --name amp -p 9093:9093 \
  -v "$PWD/alertmanager.yml:/etc/amp/alertmanager.yml:ro" \
  ghcr.io/ipiton/amp:0.1.0
```

This runs the core only. Alert history, classification and K8s targets need an AMP configuration file and PostgreSQL ([CONFIGURATION_GUIDE.md](CONFIGURATION_GUIDE.md)):

```bash
docker run -d --name amp -p 9093:9093 \
  -v "$PWD/alertmanager.yml:/etc/amp/alertmanager.yml:ro" \
  -v "$PWD/amp.yaml:/etc/amp/amp.yaml:ro" \
  -e DATABASE_PASSWORD -e LLM_API_KEY \
  ghcr.io/ipiton/amp:0.1.0 \
  --config.file=/etc/amp/alertmanager.yml --amp.config.file=/etc/amp/amp.yaml --storage.path=/data
```

## 3. Copy the silences

```bash
amtool --alertmanager.url=http://alertmanager.monitoring.svc:9093 silence query -o json > silences.json
amtool --alertmanager.url=http://amp.monitoring.svc:9093 silence import < silences.json
```

Run the import once; a second run creates duplicates. Silences created on the old Alertmanager after the export are not copied.

## 4. Point Prometheus at AMP

```yaml
# prometheus.yml
alerting:
  alertmanagers:
    - static_configs:
        - targets: ["amp.monitoring.svc:9093"]
```

With `alertmanager.replicas` > 1 list every pod (`amp-0.amp-headless.monitoring.svc:9093`, …) or use DNS service discovery on the headless Service: as with upstream, every replica must receive every alert. With the Prometheus Operator, `alertmanagerEndpoints` in the `Prometheus` resource.

To compare first, send to both: list Alertmanager and AMP side by side, and give AMP receivers that do not page anyone ([ROLLOUT_PLAN.md](ROLLOUT_PLAN.md) stage 1).

## 5. Verify

```bash
AMP=http://amp.monitoring.svc:9093
curl -fsS $AMP/-/ready
amtool --alertmanager.url=$AMP config show
amtool --alertmanager.url=$AMP alert query
amtool --alertmanager.url=$AMP silence query
amtool --alertmanager.url=$AMP cluster show
```

Alerts and silences in a UI: karma or Grafana ([ALERTMANAGER_COMPATIBILITY.md](ALERTMANAGER_COMPATIBILITY.md#viewing-alerts-karma-grafana-amtool)); AMP v0.1.0 serves no UI.

## Rollback

Point Prometheus back at Alertmanager. Silences created on AMP meanwhile: export them from AMP and import them into Alertmanager the same way. See [ROLLBACK_RUNBOOK.md](ROLLBACK_RUNBOOK.md).

## Troubleshooting

- **No alerts in AMP**: check Prometheus' `prometheus_notifications_errors_total` and `prometheus_notifications_alertmanagers_discovered`; with basic auth on, Prometheus needs `basic_auth` in its `alertmanagers` entry.
- **AMP does not start**: the log names the `alertmanager.yml` error, exactly as upstream would. An AMP configuration error is reported as `error loading AMP configuration file`.
- **Keys of your AMP configuration have no effect**: look for `AMP configuration keys ignored` in the log.
- **`POST /api/v2/alerts` answers 415**: the client must send `Content-Type: application/json`.
