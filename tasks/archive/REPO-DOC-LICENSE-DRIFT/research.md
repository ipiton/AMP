# Research: REPO-DOC-LICENSE-DRIFT

## Контекст
Задача пришла из `docs/06-planning/BUGS.md` как residual cleanup после `DOCS-HONESTY-PASS`: top-level public/docs surface уже приведен к honest contract, но в repo остались internal/subpackage docs со старыми claims про лицензию, production readiness и исторический product/runtime scope.

## Source of Truth
- `LICENSE` = `AGPL-3.0`
- top-level contract в `README.md`: текущий допустимый claim это `controlled replacement`, а не full drop-in replacement
- `docs/06-planning/DECISIONS.md` / `ADR-002`: public claims должны опираться на active runtime, а не на historical docs/tests
- `docs/06-planning/BUGS.md`: текущий slice ограничен docs cleanup, без runtime/API changes

## Findings

### 1. `CONTRIBUTING.md`
- Основной verified drift: contribution clause все еще говорит `Apache 2.0`, хотя repo license = `AGPL-3.0`.
- Остальная часть файла выглядит как generic contribution guide и не тянет за собой обязательный runtime rewrite.

### 2. `examples/README.md`
- Файл использует исторический branding `Alert History` / `Alert History Service`, а не текущий repo identity.
- Внизу файла остался `License: Apache 2.0`.
- Support links ведут на старый repo slug `ipiton/alert-history-service`.
- Есть сломанная ссылка на `../docs/adrs/`, такого пути в repo сейчас нет.
- Вывод: drift здесь не только license, а более широкий stale doc contract в пределах этого README.

### 3. `go-app/pkg/core/README.md`
- Это самый drifted документ в текущем scope.
- Verified issues:
  - historical branding `Alert History`;
  - placeholder package path `github.com/yourusername/alertmanager-plusplus/pkg/core`;
  - `Status: Production-Ready`;
  - `License: Apache 2.0`;
  - claim `100% API v2 compatibility`, который конфликтует с current active-runtime-first truth;
  - ссылки и примеры на `pkg/core/services/`, которого в repo нет;
  - ссылки на несуществующие docs (`../../docs/ARCHITECTURE.md`);
  - unverified guarantees вроде `90%+ coverage`, `100% test coverage for new code`, stable semver promises;
  - footer с `Alert History Community`, `Apache 2.0`, `Production-Ready`.
- Вывод: для этого файла недостаточно заменить две строки; нужен honest rewrite до narrow package-level contract.

### 4. `go-app/internal/infrastructure/llm/README.md`
- Verified issues:
  - `Status: Production-Ready`;
  - `License: Apache 2.0`;
  - hard benchmark/performance claims без зафиксированного benchmark source;
  - provider narrative шире подтвержденного code path.
- Code reality:
  - `client.go` явно различает только `proxy` и `openai/openai-compatible`;
  - отдельной provider-specific реализации для Anthropic в текущем пакете нет;
  - README одновременно рекламирует `OpenAI`, `Anthropic`, `Azure OpenAI`, `Custom Proxy`, что шире явно проверяемого contract.
- Вывод: здесь нужен не только license/status cleanup, но и сужение claims до того, что подтверждается текущим кодом и top-level honesty policy.

## Scope Assessment
- Задачу можно держать narrow и docs-only.
- Но practical unit of work должен быть шире, чем просто `Apache 2.0 -> AGPL-3.0` и `Production-Ready -> Experimental`.
- Иначе в тех же файлах останутся другие сильные misleading claims: stale product name, wrong package paths, unsupported links, unsupported capability promises.

## Options

### Option A: Минимальный string replacement
- Меняем только license/status mentions.
- Плюс: самый маленький diff.
- Минус: оставляет misleading contract в `examples/README.md`, `pkg/core/README.md`, `llm/README.md`.

### Option B: Honest stale-contract cleanup внутри уже заданных 4 файлов
- Для каждого файла убираем не только license/status drift, но и соседние stale claims, если они находятся в том же документе и конфликтуют с active truth.
- Плюс: закрывает bug по сути, а не по формальному паттерну.
- Минус: diff больше, чем у string replacement, но все еще остается docs-only и narrow.

### Option C: Широкий repo-wide doc sweep
- Ищем и переписываем все historical `Alert History` / `Apache 2.0` references по всему repo.
- Плюс: максимальная консистентность.
- Минус: это уже другой slice; в текущую задачу расползаться не стоит.

## Recommendation
- Для `/spec` брать `Option B`.
- Зафиксировать, что canonical scope остается ровно в четырех документах из bug entry, но cleanup в каждом файле разрешен до honest local contract, а не до одной-двух замен строк.
- Отдельно отметить, что найденные дополнительные drift-маркеры вне этих четырех файлов не чинятся сейчас автоматически и остаются вне scope, если не нужны для связности текущего doc rewrite.

## Next-Step Implication
- `/spec` должен разрешить:
  - narrow rewrite `go-app/pkg/core/README.md` до factual package description;
  - cleanup `go-app/internal/infrastructure/llm/README.md` до verified BYOK/OpenAI-compatible contract без overclaims;
  - cleanup `examples/README.md` от stale branding/license/links;
  - correction contribution license clause в `CONTRIBUTING.md`.
- `/spec` не должен расширять задачу в runtime changes, API parity restoration или repo-wide docs migration.

## Verification Path
- `rg -n "Apache 2\\.0|Production-Ready|Alert History|yourusername|100% API v2 compatibility|docs/adrs|ARCHITECTURE.md" CONTRIBUTING.md examples/README.md go-app/pkg/core/README.md go-app/internal/infrastructure/llm/README.md`
- manual review against `README.md`, `LICENSE`, `docs/06-planning/DECISIONS.md`
- `git diff --check`
