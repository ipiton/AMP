# Requirements: RUNTIME-SURFACE-RESTORATION

## Context
Для того чтобы AMP мог полноценно заменять Prometheus Alertmanager, он должен поддерживать стандартные API-эндпоинты, которые ожидают внешние инструменты (например, Grafana, amtool, скрипты автоматизации). В текущем рантайме часть этих эндпоинтов отсутствует или возвращает заглушки.

## Goals
Восстановить работу следующих API в активном рантайме:
- [x] `GET /api/v2/status` — информация о версии, конфигурации и аптайме.
- [x] `GET /api/v2/receivers` — список настроенных получателей уведомлений.
- [x] `GET /api/v2/alerts/groups` — сгруппированные активные алерты.
- [x] `POST /-/reload` — горячая перезагрузка конфигурации.
- [x] Синхронизировать planning/public truth под этот restored surface.
- [x] Выделить оставшийся active-contract refresh в отдельный follow-up bug.

## Functional Requirements
- [x] Эндпоинт `/api/v2/status` должен возвращать актуальную конфигурацию в формате YAML и метаданные системы.
- [x] Эндпоинт `/api/v2/receivers` должен возвращать список ресиверов из загруженного конфига.
- [x] Эндпоинт `/api/v2/alerts/groups` должен возвращать структуру алертов, совместимую с ожиданиями Grafana.
- [x] Эндпоинт `/-/reload` должен инициировать процесс перезагрузки конфигурации без остановки сервера.
- [x] Данные должны извлекаться из активного состояния `ServiceRegistry` и `Application`.

## Non-Functional Requirements
- Совместимость с форматами ответов Alertmanager v0.27+.
- Отсутствие деградации производительности на критическом пути приема алертов.
- Корректная обработка ошибок (например, если конфиг невалиден при перезагрузке).

## Acceptance Criteria
- [x] Handler-level tests для восстановленных эндпоинтов проходят.
- [x] Active application contract drift вынесен в отдельный follow-up bug вместо сокрытия внутри этой задачи.
- [x] Более широкий `futureparity`/historical harness остается отдельным explicit residual gap и не маскирует active contract.

## Verified Outcome (2026-03-09)
- `go-app/internal/application/router.go` теперь монтирует `/api/v2/status`, `/api/v2/receivers`, `/api/v2/alerts/groups` и `/-/reload`.
- `go test ./internal/application/handlers -count=1` green.
- `go test ./internal/application -run TestActiveRuntimeContract_HistoricalWideSurfaceIsAbsent -count=1` red, потому что old contract suite все еще ожидает отсутствие этих route-ов и не инициализирует reload coordinator в test registry.
- Значит runtime restoration по коду и docs уже landed; остаток вынесен отдельно в `APPLICATION-ROUTER-CONTRACT-DRIFT`, поэтому эта задача закрывается как restoration slice.
