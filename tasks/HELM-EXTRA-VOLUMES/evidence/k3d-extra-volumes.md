# k3d: receiver credentials from a mounted Secret

Date: 2026-10-20. Cluster: k3d `amp-extravol` (k3s v1.35.5), created with `--kubeconfig-switch-context=false`; every command used `--context`/`--kube-context k3d-amp-extravol`. Image `amp:extra-volumes` built from `f62a517` (Go code = main), `k3d image import`. Helm v4.3.0. Chart: branch working tree (after the review fixes F3/F9).

Values: `profile: lite`, local image, `monitoring.prometheusEnabled: false`, `postgresql.monitoring.prometheusRules.enabled: false` (no prometheus-operator CRDs), plus

```yaml
extraVolumes:
  - name: receiver-secrets
    secret:
      secretName: amp-receiver-secrets
      defaultMode: 0400        # stricter than the documented 0440, on purpose
extraVolumeMounts:
  - name: receiver-secrets
    mountPath: /etc/amp/secrets   # nested inside the config-file ConfigMap mount at /etc/amp
    readOnly: true
```

Secret created `--from-file` from files holding a fake bot token `123456789:FAKE-drill-token` (with a trailing `\n`) and a fake Slack URL `https://hooks.slack.com/services/T000DRILL/B000DRILL/drillsecretvalue` (without one).

## 1. Slack `api_url_file` — load fails (review F1)

`configFile.content` with `slack_configs[].api_url_file: /etc/amp/secrets/slack-webhook-url` and a Telegram receiver: the pod crash-loops.

```
{"level":"ERROR","msg":"failed to load configuration","path":"/etc/amp/config.yaml","error":"route config validation failed: alertmanager config validation failed (1 error(s)):\n  [E115] receivers[0].slack_configs[0].api_url: Slack API URL is required (set here or in global.slack_api_url) (Provide the Slack incoming-webhook URL)"}
```

The chart is not the cause: `pkg/configvalidator` runs before `routing.Parse` on types without `*_file` fields. See `review-findings.md` F1.

## 2. Telegram `bot_token_file` from the Secret

The same values without the Slack receiver (`route.receiver: telegram-panic`), `helm upgrade` + `kubectl rollout restart deploy/amp` (the config ConfigMap has no checksum annotation by design).

- Pod `Running 1/1`, log `config parsed successfully receivers=1`, `Config-provisioned publishing targets updated names=["cfg:telegram-panic/telegram0"]`.
- Nested mount works: `/etc/amp` holds `config.yaml` (ConfigMap) and `secrets/` (Secret).
- `id` → `uid=65534(nobody) gid=65534(nobody)`. Files: `-r--r----- root nobody` — kubelet turned `defaultMode: 0400` into 0440 with the `fsGroup` group; `cat` as 65534 works.
- `GET /api/v2/status` → `config.original`: `bot_token: <secret>`, `bot_token_file: /etc/amp/secrets/telegram-bot-token`; `grep -c FAKE-drill-token` on the response = 0.
- `POST /api/v2/alerts` with `alertname=ExtraVolumesDrill, severity=critical` → 200; after `group_wait` the Telegram publisher calls api.telegram.org and gets `telegram API error 401: Unauthorized` — the token from the file (trailing newline trimmed) reached the API. `grep -c FAKE-drill-token` over the whole pod log = 0.
