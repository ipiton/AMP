# AMP v0.1.0 Release Notes (DRAFT)

**Release date:** TBD
**Previous version:** none — first tagged release
**Generated from:** `CHANGELOG.md`'s `[Unreleased]` block as of 2026-08-20 (the
Alertmanager-parity program, epic through wave 7, plus everything else that
had accumulated in `[Unreleased]` before it).

## Summary

This release turns AMP from an API-compatible shape into a functional
Alertmanager replacement: routing, grouping/dedup, inhibition, silencing and
notification templates now implement upstream's actual *mechanics*, not just
matching endpoints. The single biggest visible change for an existing
deployment: **an untouched `route:`/`receivers:` config now renders
upstream-shaped notifications by default** (see Breaking Changes) instead of
AMP's own formatting — this is the point of the parity work, but it is not
invisible.

## Features

- **Alertmanager parity epic** (`AMP-PARITY`, 7 phases): recursive routing
  tree with all 4 matcher operators and anchored regex, `time_intervals:`;
  per-route `group_by`/`group_wait`/`group_interval`/`repeat_interval` with the
  upstream notify-chain order (Inhibit → Silence → TimeMute → Dedup); Redis-backed
  HA clustering (cross-replica notification dedup, distributed timer
  reconciliation, silence sync via pub/sub, leader-elected GC, peer heartbeat);
  `pkg/configvalidator` wired into startup and hot reload (`SIGHUP` + `POST /-/reload`);
  Telegram/Email publishers; wire-level group batching (one HTTP POST per
  `(group, target)` for webhook/alertmanager targets, upstream v4 JSON shape).
- **Upstream-compatible notification templates** (`TEMPLATES-EPIC`): `templates:`
  files and per-integration presentation fields (`slack_configs[].title`,
  `pagerduty_configs[].description`, `email_configs[].subject`/`html`, etc.)
  now render through a faithful port of upstream's template engine and default
  library (byte-for-byte `default.tmpl`/`email.tmpl`), instead of being parsed
  and ignored. Guarded by a render timeout + output cap that AMP adds on top of
  upstream (a broken template degrades to AMP's fixed formatter, never drops a
  notification) and by `publishing.templates.enabled` (default `true`) as a
  restart-only kill switch back to the pre-epic formatters.
- **`receivers:` integrations provision delivery endpoints** (`AMP-PARITY-WAVE6`,
  `FU-RECEIVERS-INTEGRATION`): an untouched upstream `alertmanager.yml` now
  delivers with zero Kubernetes Secrets. `webhook_configs`/`slack_configs`/
  `pagerduty_configs`/`telegram_configs`/`email_configs` auto-provision targets
  merged with any Secret-discovered ones; `global.slack_api_url`/`pagerduty_url`/
  `telegram_api_url` fallbacks; `send_resolved` per integration; blackhole
  receivers (`- name: 'null'`); the PagerDuty endpoint bug (`/v2/events` →
  `/v2/enqueue`) is fixed here too.
- **Inhibition matcher-list + secret/HTTP flexibility** (`AMP-PARITY-WAVE7`):
  `inhibit_rules[].source_matchers`/`target_matchers` (upstream's modern
  syntax) now actually inhibits; `*_file` secret variants (`url_file`,
  `routing_key_file`, `bot_token_file`, etc.) for every integration credential;
  per-integration `http_config` (proxy, TLS, basic/bearer auth, redirect
  policy) reaching the wire for webhook/Slack/PagerDuty/Telegram HTTP clients.
- **Delivery reliability hardening** (waves 3-4): a notification only counts as
  delivered — and only then suppresses a resend — once the target's HTTP call
  is *confirmed*, not merely enqueued (`publishing.queue.delivery_confirmation_timeout`,
  default 45s). A partial per-alert failure no longer re-sends alerts that
  already landed. Runtime Redis failback/failforward for group storage, with
  write-through + deletion-replay reconciliation on recovery.
- **Lite-profile restart durability** (`FU-LITE-FILE-SNAPSHOT`): the
  zero-dependency lite profile now survives a restart without losing silences
  or notification dedup state, via plain versioned JSON snapshots
  (`storage.path`, disabled by default).
- **Investigation tooling** (`PHASE-6A-BUILTIN-TOOLS`): four built-in tools for
  the LLM investigation agent — `prometheus_query_range`, `loki_query_range`,
  `kubernetes` (pods/events/logs/deployments), `database_diagnostics`
  (read-only Postgres) — wired conditionally from `investigation.tools.*` config.
- **Operational endpoint + config-API parity**: `GET /api/v2/status`/`receivers`/
  `alerts/groups`, `POST /-/reload`; a runtime config read/write/rollback API
  (`GET/POST /api/v2/config`, history, revisions, dry-run rollback/prune).
- **LLM provider flexibility**: `llm.provider` (`proxy` legacy endpoints or
  OpenAI-compatible `chat/completions`), classification stats/health endpoints.

## Performance / Improvements

- Sliding delivered-state TTL fix (a persistently-failing alert no longer
  suppresses already-delivered ones past one `repeat_interval`).
- Duplicate-metric-key flake, PagerDuty/Rootly cache keys, Slack Block Kit
  payload correctness (`{"text":""}`/`invalid_blocks` bugs from wave-3),
  per-alert delivery-outcome tracking, and multiple goroutine-leak fixes across
  `PublisherFactory.Shutdown()`.
- Go toolchain raised to 1.26.

## Breaking Changes

Carried verbatim from `CHANGELOG.md`'s Breaking Changes section and the
templates epic's own migration notes — do not paraphrase these when quoting
to users.

1. **A route without `group_by` now groups upstream-style: ONE notification
   per receiver, not one per `alertname`.** Upstream's `DefaultRouteOpts`
   leaves `group_by` empty, aggregating every matched alert into a single
   group with `{}` labels. **If you relied on the implicit per-`alertname`
   grouping, set `group_by: [alertname]` explicitly.** Affects only configs
   that never specify `group_by` at any level.
2. **PagerDuty Events API v2 endpoint corrected**: the publisher now posts to
   `<base>/v2/enqueue` instead of the non-existent `<base>/v2/events`. Real
   PagerDuty targets start working; **any mock/proxy/self-hosted listener
   built against the old path must now serve `/v2/enqueue`**. A target URL
   carrying the full old or new endpoint is normalised automatically.
3. **Renaming the ROOT route's receiver needs a restart.** The degraded-routing
   fallback receiver is a startup snapshot; after a reload that renames it,
   degraded-state alerts fail loudly instead of delivering until the process
   restarts. Normal (non-degraded) routing follows the reload immediately.
4. **`email_configs` now REQUIRE global SMTP settings — a previously-loadable
   config can fail to boot.** A receiver declaring `email_configs` must have
   `global.smtp_smarthost`, plus either `global.smtp_from` or a `from` on
   every entry. Two shapes that loaded before (with a silent-non-delivery
   WARN) now fail validation at startup **and** on `/-/reload`: no
   `global.smtp_*` block at all, or SMTP set per-`email_config` (AMP models no
   per-integration SMTP fields — those keys are dropped by the parser). **Fix
   in one line:** add `global.smtp_smarthost` (and `global.smtp_from`).
5. **Receiver names must be unique** — a load error now, as in upstream. A
   config with two same-named receivers previously loaded and silently used
   only one.
6. **A receiver with no integrations is now valid** (upstream's blackhole
   receiver, `- name: 'null'`, and any receiver whose only integration AMP
   cannot deliver — `opsgenie_configs`/`victorops_configs`/`wechat_configs`).
   It loads, warns once, and drops notifications routed to it instead of
   failing to load.
7. `services.Publisher`'s methods were renamed `PublishToAll`/
   `PublishWithClassification` → `PublishToReceiver`/
   `PublishToReceiverWithClassification` and now take the routed receiver —
   the non-grouped publish path (the default, `grouping.enabled: false`) used
   to fan every alert out to every target regardless of receiver.
8. Receiver names must not contain `/` (reserved as the group-key separator).
9. `GET /api/v2/status`'s `config.original` now redacts secret-named fields
   (PagerDuty `routing_key`, Slack/webhook URLs, Telegram `bot_token`, etc.)
   with upstream's `<secret>` placeholder instead of the raw config.
10. `PagerDutyConfig.RoutingKey` validation relaxed from `len=32` to
    `required`, matching upstream's plain non-empty `Secret` type.
11. `DELETE /api/v2/silence/{id}` now expires the silence in place
    (upstream-like) rather than hard-deleting it — treat it as "mark expired
    now," not row removal.
12. Old bare group-level nflog Redis entries (`nflog:entry:{groupKey}`) are
    never read by the new target-suffixed lookup and simply expire on their
    original TTL — no migration step needed.
13. **`grouping.reconciliation_grace` default changed 20s → 90s** (derived
    from `delivery_confirmation_timeout`), and startup now **fails** on a
    pinned value that is not strictly greater than the publish-claim TTL (65s
    at defaults). **A config carrying the old `reconciliation_grace: 20s` must
    raise it, or drop it to inherit the derived default.**
14. HA failover recovery time grew accordingly: a crashed replica's groups are
    adopted after up to ~90s grace + one reconciliation tick (was ~20s + tick)
    — the price of closing a mid-flight adoption race.
15. **Notification templates change your Slack/email/PagerDuty/Telegram
    output by default, even if you never configured a template**, because the
    renderer is now wired unconditionally and upstream's presentation
    defaults materialize for every unset field:
    - **Slack**: AMP's Block Kit `blocks` message → upstream's `attachments`
      shape (`title`, `title_link`+`fallback`, top-level `text`, new
      `username: "Alertmanager"`). The `blocks` array is gone.
    - **Email**: AMP's own subject/HTML → upstream's `email.default.subject`/
      `email.default.html` (~10 KB).
    - **PagerDuty / Telegram**: message bodies become upstream's
      `pagerduty.default.*`/`telegram.default.message`; PagerDuty
      `custom_details` gains upstream's `firing`/`resolved`/`num_firing`/
      `num_resolved` fields alongside AMP's own diagnostics.
    - **Webhook**: unchanged — upstream doesn't template webhook payloads either.
    - **Kubernetes-Secret-provisioned targets**: unchanged — discovery doesn't
      populate template fields.
    - **The revert is one line**: `publishing.templates.enabled: false`
      restores the pre-epic fixed formatters wholesale. Read at startup —
      needs a restart, not a reload.
    - A receiver that DOES name presentation fields renders that content
      instead of AMP's formatting, not both — removing the fields no longer
      restores AMP's own rendering once templates are enabled.
    - `{{ .Alerts }}` holds **one alert per message** for Slack/PagerDuty/
      Telegram/email (AMP sends one message per alert; upstream sends one per
      group). A migrated `{{ range .Alerts }}` template arrives as N
      single-alert messages instead of one aggregated one. Webhook delivery
      (batches the whole group) is unaffected.
    - A broken template file now **fails config load** instead of being
      ignored — validate with a reload against a copy before rolling out.
16. **Regex anchoring tightened at all three matcher sites** (routing,
    inhibition, and silencing): a `=~`/`!~`/classic-regex pattern relying on
    substring matching (e.g. `job=~"prod"` matching `preprod-2`) may stop
    matching/suppressing after upgrade until rewritten with explicit `.*`
    wildcards — upstream double-anchors (`^(?:pattern)$`) at every one of
    these sites and AMP now matches that everywhere, not just in routing.
17. **Absent-label matcher semantics changed**: a route or inhibition rule's
    `!=`/`!~` against a label an alert lacks now evaluates against `""`
    instead of auto-passing. A route comparing to an empty string
    (`env!=""`) can now route differently than before.
18. **Inline (non-`config_file`) `source_match_re`/`target_match_re` inhibit
    rules, previously a silent no-op, now actually suppress** — upgrading may
    surface new suppression with no config change on your part if you have
    such rules.

## Backward Compatibility

- **Kubernetes-Secret-provisioned publishing targets**: unchanged end to end —
  discovery, credentials, and (per note 15 above) template-field behavior.
- **Webhook payload shape**: unchanged. Never templated, before or after this
  release; still the upstream v4 batch JSON shape.
- **Existing `group_by`, explicit at any level**: unchanged behavior (note 1
  only affects configs with no `group_by` anywhere).
- **A target URL already carrying the full PagerDuty endpoint** (correct or
  the old bugged path some Secrets were written to compensate for): keeps
  working unedited (note 2).
- **`amtool`/API consumers reading expired silences**: `GET /api/v2/silences`
  already returned every state including `expired`; only the `DELETE`
  semantics change (note 11).

## Upgrade Steps

1. **Grep your config for implicit grouping.** If any route has no `group_by`
   at any level (route, ancestor, or `global.group_by`) and you want the old
   per-`alertname` behavior, add `group_by: [alertname]` explicitly before
   upgrading (breaking change 1).
2. **Check `email_configs` receivers** have `global.smtp_smarthost` and
   (`global.smtp_from` or a per-entry `from`) — add them now, this is a
   startup-failing change (breaking change 4).
3. **Decide on notification templates now, not after the first incident**:
   read breaking change 15. If you want to keep AMP's pre-epic formatting,
   set `publishing.templates.enabled: false` and restart *before* upgrading
   traffic over. Otherwise expect Slack/email/PagerDuty/Telegram output to
   change shape on this deploy.
4. **Drop any pinned `grouping.reconciliation_grace: 20s`** (or raise it past
   the publish-claim TTL) — leaving it unset lets it derive correctly from
   `publishing.queue.delivery_confirmation_timeout` (breaking change 13).
5. **Audit regex matchers** (`match_re`, `source_match_re`/`target_match_re`,
   `=~`/`!~` in `matchers:`) for substring-matching patterns lacking explicit
   `.*` — they will stop matching post-upgrade (breaking change 16).
6. **If any mock/proxy PagerDuty listener exists in your test setup**, point
   it at `/v2/enqueue` (breaking change 2).
7. Deploy. Watch `alert_history_publishing_blackhole_drops_total`,
   `alert_history_publishing_template_fallbacks_total`, and
   `amp_group_operations_total{status="capped"}` for the first hour —
   non-zero-but-expected under the new behavior, unexpectedly-high is a signal
   something above wasn't accounted for.

## Known Gaps / Not Yet Supported

- `FU-INTEGRATION-FIELD-FIDELITY` (mostly closed, see feature list) — SMTP
  `smtp_auth_secret`/`smtp_auth_secret_file` (CRAM-MD5) has no field to carry
  it; AMP's SMTP publisher doesn't model it at all.
- `FU-MATCHER-LABEL-CHARSET` — `pkg/configvalidator/matcher.Parse`'s
  label-name charset is narrower than upstream's (no `:`); a colon-containing
  label in a `matchers:` entry hard-fails config load.
- `FU-TOPLEVEL-INHIBIT-RULES` — a top-level `inhibit_rules:` key (upstream's
  own placement) is parsed but not consumed.
- `FU-HEALTH-HTTP-CONFIG` — publisher health probes ignore per-target
  `http_config` (one process-wide client); a proxied/mTLS/auth-gated target
  can report unhealthy while still delivering.
- `FU-HTTP-OAUTH2` — an `oauth2:` block is detected and warned on, not
  authenticated; own-`http_config` OAuth2 targets are skipped (fail closed),
  global-inherited ones deliver unauthenticated with a warning.
- `FU-HTTP-CONNECT-PROXY-COVERAGE` — CONNECT-proxy path lacks dedicated test
  coverage (implementation exists).
- OpsGenie publisher: intentionally not built (OpsGenie is EOL for new customers).
