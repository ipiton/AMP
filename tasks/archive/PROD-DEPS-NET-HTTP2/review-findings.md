# Deep Review Findings: net/http и x/net — GO-2026-6603…6617

**Trigger classification:** mandatory (`S` — supply chain)
**Reviewer perspective:** независимый read-only агент (Plan), не автор; линзы: supply chain, premises, correctness, maintainability + git history, runtime
**Reviewed at:** 2026-10-16
**Reviewed tree:** bugfix/prod-deps-net-http2 @ 2180c80
**Verdict:** pass (см. `review-verdict.json`); все находки закрыты автором после ревью (F1–F6 — текст, F7 — BACKLOG, F8 — прогон тестов); версии и код не менялись

Ревьюер независимо подтвердил: все новые версии ≥ 7 дней (8 модулей + toolchain), хеши `go.sum` побайтно совпадают с sum.golang.org, `go mod verify` — all verified, `go 1.26.0` не изменился, `1.26.8` вне planning/архива/CHANGELOG не осталось, все Go-job'ы CI берут `go-version-file`, `govulncheck` на `go1.26.9` — 0 достижимых, `.go`-файлов в диффе нет.

## Findings

### F1 — `x/tools`/`x/mod` сдвинулись в build list, evidence писал «не сдвинулись»
- **Severity:** minor
- **Location:** `go-app/go.sum` (`x/mod v0.41.0`, `x/tools v0.49.0`); `tasks.md` Implement notes 1.3; `evidence/new-module-dates.txt`
- **Issue:** `x/text` v0.42.0 тянет `x/tools` v0.49.0 и `x/mod` v0.41.0; в `go.mod` их нет, но хеши добавлены в `go.sum`, а дат в evidence не было. Карантин проходят (2026-08-13 / 2026-08-24), в бинарники не линкуются (только test-граф `modernc.org/libc`).
- **Recommendation:** дописать даты, поправить notes.
- **Disposition:** fix-here — сделано (автор перепроверил `go list -m` и proxy `.info`).
- **Follow-up:** n/a

### F2 — инвариант «1 в импортируемых пакетах» не выполнен, отклонение не записано
- **Severity:** minor
- **Location:** `Spec.md` § Invariants п. 4; `tasks.md` Implement notes
- **Issue:** baseline-находка в импортируемых пакетах — GO-2026-6604 (`os`, Windows) — тоже закрыта `go1.26.9`; результат лучше ожидаемого, но notes утверждали «отклонений нет».
- **Recommendation:** записать отклонение, сохранить verbose-вывод.
- **Disposition:** fix-here — сделано (`evidence/govulncheck-verbose-go1.26.8-branch.txt`, `evidence/govulncheck-verbose-go1.26.9.txt`).
- **Follow-up:** n/a

### F3 — CHANGELOG неверно объяснял бамп `x/net` и не упоминал GO-2026-6604
- **Severity:** minor
- **Location:** `CHANGELOG.md:11`
- **Issue:** «`x/net` … to fix 12 standard library vulnerabilities»: на деле у пяти ID (6603, 6610, 6611, 6612, 6617) вторая находка в `x/net/http2` через Kubernetes-клиент, без v0.60.0 `govulncheck` остался бы красным. GO-2026-6604 (импорт без вызова) не упомянута, хотя прецедент PROD-DEPS-VULN перечисляет такие.
- **Recommendation:** переформулировать, добавить «Also fixed, not called».
- **Disposition:** fix-here — сделано.
- **Follow-up:** n/a

### F4 — «HTTP/2 server crash» завышает экспозицию
- **Severity:** nit
- **Location:** `CHANGELOG.md:11`; `requirements.md` Root Cause / Story 2
- **Issue:** серверы AMP — plaintext `ListenAndServe` без h2c; серверные HTTP/2-advisories достижимы по символам, но в штатном деплое не эксплуатируются. В Root Cause requirements в списке `x/net` стоит 6613 вместо 6603.
- **Recommendation:** смягчить CHANGELOG; requirements — исторический артефакт.
- **Disposition:** fix-here — CHANGELOG (вместе с F3) и requirements (уточнение под Root Cause, исходный текст сохранён) — сделано по решению владельца «фиксить все находки».
- **Follow-up:** n/a

### F5 — исключение из карантина для образа уже, чем описано
- **Severity:** nit
- **Location:** `research.md` § 5
- **Issue:** 2026-10-10 перепушены только index и riscv64; манифесты amd64/arm64 — 2026-10-08, уже старше 7 дней. Digest `397ecc…` — манифест amd64, а не index (`3082400e…`). Пиннинг по тегу согласован с репо; digest-пины — BACKLOG `CI-SUPPLY-CHAIN`.
- **Recommendation:** уточнить текст; решение владельца не меняется.
- **Disposition:** fix-here — сделано.
- **Follow-up:** n/a

### F6 — `git diff --check main...HEAD` красный
- **Severity:** nit
- **Location:** `tasks/PROD-DEPS-NET-HTTP2/Spec.md:16` (trailing spaces из `tasks/templates/Spec.md:16`)
- **Issue:** шаг 5.2 упал бы.
- **Recommendation:** убрать пробелы; шаблон — отдельно.
- **Disposition:** fix-here — сделано в Spec.md. Шаблон `tasks/templates/Spec.md` vendored из upstream Solo Kanban (`AGENTS.md`: «do not edit by hand») — правка там затрётся следующим `solo-kanban-sync.sh`; это вопрос upstream, не этой задачи.
- **Follow-up:** n/a

### F7 — устаревшие `1.26.8` в planning
- **Severity:** nit
- **Location:** `docs/06-planning/BACKLOG.md:54` (`CI-SUPPLY-CHAIN`), `DECISIONS.md:183` (ADR-013), `NEXT.md:29`
- **Issue:** BACKLOG — живой пункт со старой версией; ADR и NEXT — исторический контекст / правится в finalize.
- **Recommendation:** в finalize поправить BACKLOG:54 на нейтральное «builder `golang:*-alpine`»; ADR не трогать.
- **Disposition:** fix-here — `BACKLOG.md:54` → «builder `golang:*-alpine`» (без версии, чтобы не устаревало); ADR-013 не тронут (исторический контекст), `NEXT.md` правится в finalize.
- **Follow-up:** n/a

### F8 — поведение `x/text` / `x/crypto` для прямых импортов
- **Severity:** nit
- **Location:** `go-app/go.mod:33-34`; `internal/application/webauth.go` (`bcrypt`), `internal/business/templating/funcs.go`, `internal/notification/template/functions.go` (`x/text/cases`, `language`)
- **Issue:** minor-бампы; сборка и vet чистые, сдвиг поведения — вопрос прогона.
- **Recommendation:** покрывается release-gate (`webauth_test.go`, тесты templating).
- **Disposition:** fix-here — `go vet` + `go test -count=1` на `go1.26.9` для всех пакетов с прямыми импортами `x/crypto`/`x/text` (`internal/application`, `internal/business/templating`, `internal/notification/template`) — ok, `evidence/f8-direct-imports-tests.txt`. Полный модуль — release-gate (5.1): PASS 11/11, включая `-race` (`evidence/release-gate.txt`) — F8 опровергнут, сдвига поведения нет.
- **Follow-up:** n/a

## Design Premises (по ревью)

| # | Верна? | Класс заслужен? |
|---|---|---|
| P1 | да | measured |
| P2 | да (`x/tools`/`x/mod` — «возможно», сдвинулись в build list, F1) | после implement — measured |
| P3 | да | code-read |
| P4 | да (`ci.yml:37,88,106`, лог run 38047566510) | measured |
| P5 | да, на решение не влияет | code-read |
| P6 | да (`1.26.9-alpine` = `1.26.9-alpine3.24`, index `3082400e…`) | фактически measured |
| P7 | пока да (build/vet чистые) | assumed — честно, проверка в testing |

## Anti-Pattern Check

- [x] Self-audit was not treated as a substitute for independent review.
