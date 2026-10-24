# Расхождения: контрактные сьюты AMP на upstream-ядре v0.34.1

**Прогон:** `go test -tags futureparity,upstreamcore -count=10 ./cmd/server/` (2026-10-24), харнесс `upstreamcore_compat.go`.
**Итог:** 51 листовое падение из ~200 проверок; 47 стабильны (10/10), 4 — асинхронность групп (8–10/10). Machine-readable baseline — `go-app/cmd/server/testdata/upstreamcore-divergences.tsv`.

## Классы

| Класс | Смысл | Что делаем | Кол-во |
|---|---|---|---|
| **a** | AMP-only поверхность, у upstream её нет | срез 6 `UPSTREAM-API-UI` возвращает её поверх upstream или удаляет | 17 |
| **b** | тест закрепил поведение своего ядра; upstream — эталон (ADR-019) | ожидаемое расхождение; тест переписывается или удаляется вместе со своим ядром в срезе 5 | 31 |
| **c** | пробел нашего wiring upstream-ядра | закрыть в срезе 5 | 2 |
| **d** | upstream ведёт себя иначе, чем AMP решил намеренно | решение владельца | 1 |

## Расхождение вне сьютов

- **POST без `Content-Type` → 415** (b). AMP принимал JSON без заголовка, upstream (go-swagger) требует `application/json` — 31 падение одной причины до нормализации. Prometheus и amtool заголовок шлют; клиенты вроде `curl -d` без `-H` получат 415, как и на upstream Alertmanager. Харнесс проставляет заголовок, если его нет. В migration notes среза 7.

## a — AMP-only поверхность (17)

| Тест | Что |
|---|---|
| `TestPhase0Contracts_HealthAndReady`; `TestPhase0RouteInventory/{health,healthz_alias,ready,readyz_alias}` | `/health`, `/healthz`, `/ready`, `/readyz` — у upstream только `/-/healthy`, `/-/ready` (оба проходят) |
| `TestPhase0RouteInventory/{root,dashboard,dashboard_alerts,dashboard_llm,dashboard_routing,dashboard_silences,static_asset}` | UI AMP (`/`, `/dashboard*`, `/static/*`) |
| `TestPhase0RouteInventory/inhibitions_get` | `/api/v2/inhibitions` — AMP-расширение |
| `TestPhase0RouteInventory/{alerts_v1_get_alias,alerts_v1_post_alias}`, `TestPhase0Contracts_CoreAPI/alerts_v1_ingest_compatibility_contract` | `/api/v1/alerts`: upstream с 0.27 отвечает 410 Gone. AMP держал v1 ради старых Prometheus; решение — срез 6 |
| `TestPhase0AlertsStateSemantics/resolved_closes_firing_and_appears_via_resolved_filter` | `?status=resolved` — параметр AMP; upstream не отдаёт resolved-алерты в API |

## b — тест закрепил поведение своего ядра (31)

**Коды и тела ошибок (23):**
- 422 вместо 400 на невалидный/пустой payload и матчеры: `CoreAPI/{alerts_post_missing_labels,silences_post_invalid_payload,silences_post_no_matchers,silence_by_id_invalid_id}_contract`, `TestUpstreamParity_{PostSilenceErrorPayloadContracts,SilenceByIDInvalidUUIDReturnsNotFound}`.
- `generatorURL` валидируется (`format: uri`) → 422: `CoreAPI/alerts_post_invalid_generatorURL_contract`.
- ошибка — JSON-строка, а не объект `{"error": ...}`: `CoreAPI/{alerts_get_invalid_filter_matcher,alerts_post_empty_labels,silences_get_invalid_filter,silences_post_invalid_matcher_name}_contract`, `TestUpstreamParity_SilencesInvalidFilterErrorPayloadIsJSONObject`.
- текст ошибки не `invalid alert payload`: `CoreAPI/alerts_post_invalid_payload_contract`, `TestUpstreamParity_PostAlertsErrorPayloadContracts`.
- update silence с неизвестным/невалидным id → 404, а не 400: `CoreAPI/silences_post_update_{unknown,invalid}_id_contract`.
- невалидные `active=`/`filter=` у `/alerts` и `/alerts/groups`: upstream игнорирует невалидный bool (200) и отклоняет невалидный `filter` у groups (400) — AMP наоборот: `CoreAPI/{alerts_get_invalid_state_flag,alert_groups_invalid_query_filters}_contract`, `TestUpstreamParity_AlertsAndGroupsInvalidQueryContract`.
- `/-/reload` отвечает пустым телом, а не `OK`; `POST /-/healthy|ready` — `Method Not Allowed\n`, а не пустое тело: `CoreAPI/alertmanager_compatibility_probes_contract/{reload_post,healthy_post_not_allowed,ready_post_not_allowed}`, `TestUpstreamParity_ReloadSuccessReturnsOKBody`.

**Поведение (8):**
- **Старт с битым конфигом** (2): AMP стартует и отдаёт 500 на reload, upstream не стартует вовсе (`config.LoadFile` в `New`). Reload битого конфига после старта на upstream — 500, прежний конфиг в силе (`internal/upstreamcore/core_test.go`). `TestPhase0ReloadInvalidConfigReturns500`, `TestUpstreamParity_ReloadReturns500OnInvalidConfig`.
- **Update silence со `startsAt` в прошлом** (1) создаёт новый silence и экспайрит старый (`silence.canUpdate`: начало активного/ожидающего silence нельзя сдвинуть в прошлое на месте). `TestPhase0SilencePostUpdateSemantics`.
- **Фильтр silences** (1): upstream не матчит silence с матчером `service!="api"` фильтром `service="api"` (2 из 4, а не 3). Комментарий теста ссылается на «upstream semantics» — настоящий upstream с ним не согласен. `TestPhase0SilencesFilterMatcherSemantics`.
- **Группы появляются асинхронно** (4, `flaky`): dispatcher получает алерты через подписку на провайдер (`dispatch/dispatch.go:183`), тест читает `/alerts/groups` сразу после POST. Через ~1ms группа есть, состав и labels совпадают с ожиданием теста (проба с ожиданием 100ms). `TestPhase0AlertGroupsNestedAlertShape`, `TestUpstreamParity_{AlertGroupsShapeAndFilters,AlertGroupsWithoutGroupByHaveEmptyLabels,InvalidStatusAndResolvedAreIgnored}`. Клиентам API это видно как eventual consistency в пределах миллисекунд — так же ведёт себя upstream Alertmanager.

## c — пробел wiring (2)

- `versionInfo.version` пустая: `prometheus/common/version` заполняется `-ldflags` при сборке, `cmd/amp-upstream` собирается без них. `CoreAPI/status_contract`, `TestUpstreamParity_StatusRequiredShape`. Закрыть в срезе 5 (сборка бинаря/образа).

## d — решение владельца (1)

- **`/debug/pprof/*`** открыт: upstream `httpserver.Register` вешает `/debug/` на `http.DefaultServeMux`, AMP его намеренно убрал (`TestPhase0RouteInventory/debug_removed`). Варианты: (1) как upstream — pprof доступен всем, кто достаёт до порта; (2) вырезать в нашем wiring (не вызывать `/debug` маршрут — свой `Register` из трёх строк вместо `httpserver.Register`); (3) за флагом. Ответ нужен к срезу 5.

## Verdict (ADR-019)

**PoC проходит.** Ни одно падение не показывает разрыва в ядре доставки: ingest, состояние алертов, группировка (с поправкой на асинхронность), silences, inhibition, reload и amtool работают на upstream-ядре. Расхождения — AMP-only поверхность (срез 6), формат ошибок и детали, которые свои тесты закрепили за своим ядром (upstream — эталон по ADR-019), один пробел сборки (срез 5) и один вопрос владельцу (`/debug/pprof`). Breaking для клиентов (в migration notes среза 7): 415 без `Content-Type`, 422 и формат ошибок, `/api/v1` → 410, нет `?status=resolved`, старт с битым конфигом невозможен.
