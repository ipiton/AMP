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

- 2026-11-08 — (Architecture / ADR-019) **UPSTREAM-AI-HISTORY-INVESTIGATION** — срез 2b эпика ADR-019, вместе с 2a закрывает `UPSTREAM-AI-HOOKS` (запись удалена из BACKLOG). История и investigation на upstream-ядре `cmd/amp-upstream`.
  - **История:** `upstreamai.History` — колбэк store: `PostStore` неблокирующе кладёт событие в шард по fingerprint (4×256), воркер пишет через `DeduplicationService.ProcessAlert` (путь `cmd/server`) с таймаутом 5s; firing/resolved, заглушённые тоже; resolved по истёкшему `EndsAt` — из `PostDelete` при GC. Метрика `amp_upstream_history_writes_total{outcome}`.
  - **Investigation:** классификатор ставит `Submit` после результата (classified и fallback — паритет с `cmd/server`) и повторно на каждый resend; dedup очереди — одно на инцидент. `agent_mode` не поддержан (Warn), claim — на реплику.
  - **Ключ:** история, investigation и кеш — SHA-256 от labels (`alertconv.Fingerprint`), FNV `api/v2` — `UpstreamFingerprint` тех же labels; схема не менялась.
  - **Открытые вопросы 2a:** inhibition — `mutes` = silence ∨ inhibitor активной конфигурации (`reloader.inhibits`); после снятия mute — перепроверка не чаще раза в 1m (outcome `skipped_muted`). `PreStore` ничего не отвергает (решение Spec: отказ убрал бы алерт из store; шум — `llm.drop_noise` на notify).
  - **Wiring:** новый `cmd/amp-upstream/amp.go` (`newAMPLayer`), `main.go` — вызов; `upstreamcore/core.go` (`var r` выше `mem.NewAlerts`, mutes), `reloader.go` (`inhibits`), `doc.go`. Конфиг-ключей нет. PG недоступен на старте → Error, доставка без истории до рестарта.
  - **Проверка:** Full; deep-review независимым агентом: раунд 1 `fix_required` (F1–F3 major — очередь забивалась muted-алертами, investigation один раз за жизнь записи, старт ждал PG без границы), исправлены вместе с F4–F6, F8–F10; F7 отклонён (паритет); раунд 2 `pass` @ `d77534b` (N2 — `Submit` после `Stop` — исправлен). Тесты `upstreamai`, `upstreamcore/mutes_test.go`, `cmd/amp-upstream`, `-race`. `quality-gates-fast` PASS, `release-gate.sh` 12/13 PASS, `lint` красный только на предсуществующем `LINT-ERRCHECK-CHANNEL-RECEIVE`; `git diff --check` чисто. Evidence — `tasks/archive/UPSTREAM-AI-HISTORY-INVESTIGATION/evidence/testing.md`.
  - **Follow-ups:** TECH-DEBT `UPSTREAM-HISTORY-PG-RECONNECT` (F3), `UPSTREAM-HISTORY-CONVERT-UNDER-LOCK` (F11), `UPSTREAM-AI-REFIRE-STALE-CLASSIFICATION` (N5); в BACKLOG: общий claim investigation → срез 4, `agent_mode` → срез 5, поиск по FNV → срез 6.
