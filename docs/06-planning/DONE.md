# DONE

Закрытые срезы и проверенные итоги **текущего месяца**. Прошлые месяцы — в `archive/DONE-YYYY-MM.md`. Проверка «закрыт ли slug» читает этот файл **и** все архивы:

```bash
grep -rn "SLUG" docs/06-planning/DONE.md docs/06-planning/archive/DONE-*.md
```

| Архив | Период |
|---|---|
| `archive/DONE-2026-10.md` | 2026-10 |
| `archive/DONE-2026-09.md` | 2026-09 |
| `archive/DONE-2026-08.md` | 2026-08 |
| `archive/DONE-2026-05.md` | 2026-05 |
| `archive/DONE-2026-04.md` | 2026-04 |
| `archive/DONE-2026-03.md` | 2026-03 |
| `archive/DONE-2026-02.md` | 2026-02 |

## 2026-11

- 2026-11-08 — (Architecture / ADR-019) **UPSTREAM-NON-UPSTREAM-RECEIVERS** — срез 3 эпика ADR-019: K8s-Secret таргеты и Rootly на upstream-ядре `cmd/amp-upstream`; все интеграции `alertmanager.yml` — upstream `notify/*` как есть.
  - **Rootly** — без своего Notifier: upstream `webhook_configs` на alert source «Alertmanager» (Bearer, `send_resolved: true`); таргет на REST API (`api.rootly.com`) пропускается с WARN. `ROOTLY-INCIDENT-KEY-STABLE` на upstream-ядре не существует → `BACKLOG.md` `[-]`, снимается срезом 5.
  - **K8s-Secret таргеты** (решение владельца 2026-11-08 — адаптер): `internal/upstreamtargets` — снимок из `business/publishing` discovery, на каждом применении конфига копия `config.Config` с `webhook_configs`/`slack_configs`/`pagerduty_configs` в receiver'ах по `amp.receiver`; каждая интеграция проверяется `receiver.BuildReceiverIntegrations`, битая пропускается; опрос `publishing.refresh.interval` → `Reload` при смене. Семантика старого ядра: принадлежность, `send_resolved`, PagerDuty URL и routing key; `Authorization` → `authorization`/`basic_auth`, зарезервированные заголовки отбрасываются. Метрика `amp_upstream_k8s_targets{state}`. Включается с `--amp.config.file`, `profile: standard`, `publishing.enabled`.
  - **Wiring:** `upstreamcore.Options.ExtendConfig` (`core.go`, `reloader.go`, `doc.go`), `cmd/amp-upstream/{main.go,targets.go}` (AMP-конфиг грузится один раз), `go.mod` (`yaml.v2` → direct).
  - **BUGS:** `K8S-CLIENT-HEALTH-TIMEOUT` закрыт (пагинация `ListSecrets`, `Health` с дедлайном через REST `/version`, `Close` без гонки; `REVIEW-PIPELINE` #43); `REFRESH-WORKER-WARMUP-STOP-FLAKY`, `FLAKE-REFRESH-IN-PROGRESS` → «Снимается переходом на upstream», срез 5.
  - **Проверка:** tier эскалирован Standard → Full (`C S X R`). Deep-review независимым агентом: раунд 1 `fix_required` (F1 major — PagerDuty base URL без `/v2/enqueue`), раунд 2 `pass` @ `b71c653`, N1–N4 исправлены следом. Тесты: юниты `upstreamtargets`, e2e на `upstreamcore` с фейковым K8s (доставка, `/api/v2/status` без секретов, reload, смена Secret), k8s-клиент (пагинация, health, `Close` под `-race`). `quality-gates-fast`, `git diff --check` — зелёные; `release-gate.sh` 12/13 PASS, `lint` красный на предсуществующем `LINT-ERRCHECK-CHANNEL-RECEIVE` (файлы вне диффа).
  - **Follow-ups:** TECH-DEBT `K8S-CLIENTSET-BINARY-SIZE` (бинарь 43 → 79 МБ), `UPSTREAM-TARGETS-DISCOVERY-DEPENDENCY` (срез 5 должен сохранить discovery). Принятые ограничения: сдвиг nflog-индексов K8s-интеграций при добавлении таргета (F4), K8s недоступен на старте → только файл до рестарта (F6). Ротация `DONE.md` за 2026-10 не сделана, чтобы не конфликтовать с параллельными срезами 2b/4 — за последним из них. Архив: `tasks/archive/UPSTREAM-NON-UPSTREAM-RECEIVERS/`.

- 2026-11-08 — (Architecture / ADR-019) **UPSTREAM-AI-HISTORY-INVESTIGATION** — срез 2b эпика ADR-019, вместе с 2a закрывает `UPSTREAM-AI-HOOKS` (запись удалена из BACKLOG). История и investigation на upstream-ядре `cmd/amp-upstream`.
  - **История:** `upstreamai.History` — колбэк store: `PostStore` неблокирующе кладёт событие в шард по fingerprint (4×256), воркер пишет через `DeduplicationService.ProcessAlert` (путь `cmd/server`) с таймаутом 5s; firing/resolved, заглушённые тоже; resolved по истёкшему `EndsAt` — из `PostDelete` при GC. Метрика `amp_upstream_history_writes_total{outcome}`.
  - **Investigation:** классификатор ставит `Submit` после результата (classified и fallback — паритет с `cmd/server`) и повторно на каждый resend; dedup очереди — одно на инцидент. `agent_mode` не поддержан (Warn), claim — на реплику.
  - **Ключ:** история, investigation и кеш — SHA-256 от labels (`alertconv.Fingerprint`), FNV `api/v2` — `UpstreamFingerprint` тех же labels; схема не менялась.
  - **Открытые вопросы 2a:** inhibition — `mutes` = silence ∨ inhibitor активной конфигурации (`reloader.inhibits`); после снятия mute — перепроверка не чаще раза в 1m (outcome `skipped_muted`). `PreStore` ничего не отвергает (решение Spec: отказ убрал бы алерт из store; шум — `llm.drop_noise` на notify).
  - **Wiring:** новый `cmd/amp-upstream/amp.go` (`newAMPLayer`), `main.go` — вызов; `upstreamcore/core.go` (`var r` выше `mem.NewAlerts`, mutes), `reloader.go` (`inhibits`), `doc.go`. Конфиг-ключей нет. PG недоступен на старте → Error, доставка без истории до рестарта.
  - **Проверка:** Full; deep-review независимым агентом: раунд 1 `fix_required` (F1–F3 major — очередь забивалась muted-алертами, investigation один раз за жизнь записи, старт ждал PG без границы), исправлены вместе с F4–F6, F8–F10; F7 отклонён (паритет); раунд 2 `pass` @ `d77534b` (N2 — `Submit` после `Stop` — исправлен). Тесты `upstreamai`, `upstreamcore/mutes_test.go`, `cmd/amp-upstream`, `-race`. `quality-gates-fast` PASS, `release-gate.sh` 12/13 PASS, `lint` красный только на предсуществующем `LINT-ERRCHECK-CHANNEL-RECEIVE`; `git diff --check` чисто. Evidence — `tasks/archive/UPSTREAM-AI-HISTORY-INVESTIGATION/evidence/testing.md`.
  - **Follow-ups:** TECH-DEBT `UPSTREAM-HISTORY-PG-RECONNECT` (F3), `UPSTREAM-HISTORY-CONVERT-UNDER-LOCK` (F11), `UPSTREAM-AI-REFIRE-STALE-CLASSIFICATION` (N5); в BACKLOG: общий claim investigation → срез 4, `agent_mode` → срез 5, поиск по FNV → срез 6.
