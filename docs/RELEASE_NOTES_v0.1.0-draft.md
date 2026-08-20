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

Every entry below is quoted **verbatim** from `CHANGELOG.md`'s
`### Breaking changes / migration notes` section (items 1-14) and from the
templates epic's own numbered migration notes and the wave-7 "Upgrade impact"
paragraph (items 15-16) — no rewording, per this template's own rule. Fix-round
correction: an earlier draft of this file paraphrased items 1, 8 and 16-18;
they are restored to CHANGELOG's exact wording below (16-18 turn out to be one
CHANGELOG sentence with a (1)/(2)/(3) list, not three separate bullets — quoted
as such).

1. > **A route without `group_by` now groups upstream-style: ONE notification
   > per receiver, not one per `alertname`** (post-merge fix for
   > `FU-RECEIVERS-INTEGRATION`): `business/routing.TreeBuilder.inheritGroupBy`
   > used to substitute `["alertname"]` when neither the route, its ancestors,
   > nor `global.group_by` set it. Upstream Alertmanager's `DefaultRouteOpts`
   > leaves `group_by` EMPTY, which means every alert the route matches
   > aggregates into a single group whose labels are `{}` — that is what
   > `GET /api/v2/alerts/groups` and `amtool` report, and what the notify
   > chain sends. **If you relied on the implicit per-`alertname` grouping,
   > set `group_by: [alertname]` explicitly** (upstream requires the same).
   > Affects only configs that never specify `group_by` at any level; every
   > config with an explicit `group_by` is unchanged. This divergence
   > pre-dated the epic and was masked: a `route:`/`receivers:` config with an
   > integration-less receiver failed to load before blackhole receivers
   > became legal, so the groups API fell back to its legacy
   > single-synthetic-group path and the upstream-parity test passed for the
   > wrong reason.
2. > **PagerDuty Events API v2 endpoint corrected** (`FU-RECEIVERS-INTEGRATION`,
   > slice 1): the publisher posted trigger/acknowledge/resolve events to
   > `<base>/v2/events`, which is not a PagerDuty endpoint — Events API v2
   > enqueues at `<base>/v2/enqueue`. Every PagerDuty notification was
   > rejected. Nothing to change for real PagerDuty targets (they start
   > working), but **any mock, proxy or self-hosted listener built against the
   > old path must now serve `/v2/enqueue`** (change events keep using
   > `/v2/change/enqueue`). A target URL carrying the FULL endpoint
   > (`https://events.pagerduty.com/v2/enqueue`, upstream's
   > `pagerduty_configs[].url` semantics — or `/v2/events`, if a Secret was
   > written to compensate for the bug) is normalised to its base before the
   > path is appended, so both existing K8s Secret shapes keep working
   > without edits.
3. > **Renaming the ROOT route's receiver needs a restart** (`FU-RECEIVERS-INTEGRATION`,
   > slice 1): the degraded-routing fallback receiver (used only when a
   > configured route tree produced no decision — a non-fatal tree build
   > failure or a per-alert `Evaluate` error) is a startup snapshot of
   > `route.receiver`. After a reload that RENAMES it, that fallback points at
   > a name the config no longer declares, so degraded-state alerts fail
   > loudly (`routing unavailable` / `no targets found for receiver`) instead
   > of being delivered — a safe failure mode, but it stays until the process
   > restarts. Same restart class as adding a `route:` section to a config
   > that started without one. Normal (non-degraded) routing follows the
   > reload immediately.
4. > **`email_configs` now REQUIRE global SMTP settings — a previously-loadable
   > config can fail to boot** (`FU-RECEIVERS-INTEGRATION`, slice 2): a
   > receiver declaring `email_configs` must have `global.smtp_smarthost`,
   > plus either `global.smtp_from` or a `from` on every entry. Two shapes
   > that loaded before (with a WARN, and silently provisioned no email
   > target) now fail validation at startup AND on `/-/reload`, so an upgrade
   > can turn a running deployment into a boot loop: (1) `email_configs` with
   > no `global.smtp_*` block at all; (2) an upstream config that sets SMTP
   > **per `email_config`** (`smarthost`/`auth_username`/`auth_password`/
   > `require_tls`) — AMP models no per-integration SMTP fields, so those
   > keys are dropped by the parser and cannot substitute for the global
   > ones. Fix in one line: add `global.smtp_smarthost` (and
   > `global.smtp_from`) to your config. This matches upstream Alertmanager,
   > which fails `config.Load` with "no global SMTP smarthost set" / "no
   > global SMTP from set" for the same shape; the alternative (warn and
   > blackhole) is silent non-delivery of email.
5. > **Receiver names must be unique** (`FU-RECEIVERS-INTEGRATION`, slice 1):
   > duplicates are now a load error, as in upstream Alertmanager. A config
   > with two receivers of the same name previously loaded and silently used
   > only one of them.
6. > **A receiver with no integrations is now valid** (`FU-RECEIVERS-INTEGRATION`,
   > slice 1): upstream's blackhole receiver (`- name: 'null'`) and a receiver
   > whose only integration AMP cannot deliver (`opsgenie_configs`,
   > `victorops_configs`, `wechat_configs`) load instead of failing
   > validation, emit a load-time WARNING naming the unsupported integration,
   > and drop notifications routed to them (counted as
   > `alert_history_publishing_blackhole_drops_total{receiver=...}`).
   > configvalidator's `E024` became the non-blocking `W024`.
7. > `services.Publisher`'s methods were renamed `PublishToAll`/`PublishWithClassification`
   > → `PublishToReceiver`/`PublishToReceiverWithClassification` and now take
   > the routed receiver: the non-grouped publish path (the default,
   > `grouping.enabled: false`) used to fan every alert out to every target
   > regardless of receiver.
8. > Receiver names must not contain `/` (reserved as the group-key
   > separator, `receiver=<name>/<group-key>`); any other non-empty name is
   > accepted.
9. > `GET /api/v2/status`'s `config.original` now redacts secret-named fields
   > (PagerDuty `routing_key`, Slack/webhook URLs, Telegram `bot_token`, etc.)
   > with upstream's own `<secret>` placeholder instead of the raw config.
10. > `PagerDutyConfig.RoutingKey` validation relaxed from `validate:"len=32"`
    > to `validate:"required"`, matching upstream's plain non-empty `Secret`
    > type — any previously-32-char-enforced config still validates, but
    > non-32-char keys that were wrongly rejected before now pass too.
11. > `DELETE /api/v2/silence/{id}` expires the silence in place
    > (upstream-like semantics) rather than a hard delete; treat it as "mark
    > expired now," not row removal.
12. > Old bare group-level nflog Redis entries (`nflog:entry:{groupKey}`) are
    > never read by the new target-suffixed lookup
    > (`nflog:entry:{groupKey}:{target}`) and simply expire on their original
    > TTL — no migration step needed.
13. > `grouping.reconciliation_grace` default changed 20s → 90s (derived from
    > `delivery_confirmation_timeout`), and startup now **fails** on a pinned
    > value that is not strictly greater than the publish-claim TTL (65s at
    > defaults): a config carrying the old `reconciliation_grace: 20s` must
    > raise it (or drop it to inherit the derived default).
14. > HA failover recovery time grew accordingly: a crashed replica's groups
    > are adopted after up to ~90s grace + one reconciliation tick (was ~20s +
    > tick) — the price of closing the mid-flight adoption race (`DeleteTimer`
    > vs the live publisher's `SaveTimer`).
15. Templates-epic migration notes — CHANGELOG's own header: **"note 1 is a
    BREAKING CHANGE for every deployment that uses `route:`/`receivers:`,
    whether or not you configured a single template"**:
    1. > **BREAKING: your Slack and email notifications change shape by
       > default.** Upstream's presentation defaults are materialized for
       > every unset field of every `receivers:`-provisioned
       > `slack_configs`/`pagerduty_configs`/`telegram_configs`/`email_configs`
       > entry, and the template renderer is wired unconditionally — so a
       > config that names no template at all now renders **upstream's**
       > output instead of AMP's. Concretely, before → after:
       >   - **Slack**: AMP's Block Kit message (a `blocks` array with
       >     AMP-worded sections) → upstream's `attachments` shape — `title` =
       >     `[FIRING:1] <group labels> (<remaining common labels>)`,
       >     `title_link` + `fallback` = the alertmanager link, top-level
       >     `text` = upstream's fallback string, plus a new
       >     `username: "Alertmanager"`. The `blocks` array is **gone**.
       >   - **Email**: AMP's own subject and HTML body → upstream's
       >     `email.default.subject` and its ~10 KB `email.default.html`.
       >   - **PagerDuty / Telegram**: `summary`/`client`/`client_url` and the
       >     message body become upstream's `pagerduty.default.*` /
       >     `telegram.default.message` renderings; PagerDuty `custom_details`
       >     gains upstream's `firing`/`resolved`/`num_firing`/`num_resolved`
       >     entries on top of AMP's diagnostics.
       >   - **Webhook**: unchanged. Upstream does not template webhook
       >     payloads, and neither does AMP.
       >   - **Kubernetes-Secret-provisioned targets**: unchanged. Discovery
       >     does not populate template fields, so those targets keep the
       >     fixed formatters.
       >
       >   This is the drop-in parity the epic exists for — an untouched
       >   upstream config should render upstream's output — but it IS a
       >   visible change for anyone who migrated a config and got used to
       >   AMP's formatting. **The revert is one line:**
       >   `publishing.templates.enabled: false` restores the pre-epic fixed
       >   formatters wholesale (`templates:` files and per-integration
       >   presentation fields ignored, exactly as before). It is read at
       >   startup, so flipping it needs a restart, not a reload.
    2. > **A receiver that DOES name presentation fields renders your content,
       > not both.** For Slack the operator's presentation replaces the Block
       > Kit rendering rather than shipping two versions in one message. If
       > you want AMP's formatting for a specific integration, use the switch
       > in note 1 — removing the fields no longer restores it, because the
       > defaults are materialized.
    3. > **Relative `templates:` paths now work** (`templates: ['templates/*.tmpl']`,
       > upstream's canonical idiom) and resolve against the config file's
       > directory. Previously they resolved against the process CWD, matched
       > nothing, and loaded clean — if you worked around that with absolute
       > paths, those still work unchanged.
    4. > **A broken template file now fails config load** instead of being
       > ignored. Validate with a reload against a copy before rolling out.
    5. > **`{{ .Alerts }}` holds ONE alert per message for slack/pagerduty/telegram/email**
       > — a DELIVERY divergence, not just a data-model one. Upstream sends
       > one message per group; AMP sends one per alert, each with a
       > one-element `.Alerts`. A migrated `{{ range .Alerts }}` template that
       > upstream rendered as a single aggregated message therefore arrives
       > as N single-alert messages. Group labels, receiver and status are
       > correct; the alert SET is not upstream's. Webhook delivery is
       > unaffected (it batches the whole group, upstream's v4 shape).
    6. > `{{ .RouteLabels }}`, `{{ routeLabels "x" }}` and
       > `{{ .NotificationReason }}` are accepted and render **empty** — AMP
       > has no source for them. They do not error, so a migrated template
       > using them keeps working.
16. Wave-7 "Upgrade impact" paragraph, quoted whole (CHANGELOG's own (1)/(2)/(3)
    list, not three separate entries):
    > **Upgrade impact**, spelled out explicitly for the first time (all three
    > fix rounds' worth): (1) route `!=`/`!~` against a label an alert lacks
    > now evaluates against `""` instead of auto-passing — a route comparing
    > to an empty string (`env!=""`) can now route differently than before;
    > (2) inline (non-`config_file`) `source_match_re`/`target_match_re`
    > inhibit rules, previously a silent no-op, now actually suppress — new
    > suppression appearing with no config change; (3) regex anchoring
    > tightened at all three sites (routing, inhibition, and now silencing) —
    > a `=~`/`!~`/classic-regex pattern relying on substring matching may
    > stop matching/suppressing after upgrade until rewritten with explicit
    > `.*` wildcards.

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
