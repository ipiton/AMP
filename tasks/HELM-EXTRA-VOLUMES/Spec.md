---
id: HELM-EXTRA-VOLUMES
slug: helm-extra-volumes
stream: operations
type: feature
status: approved
created_at: 2026-10-20
updated_at: 2026-10-20
based_on:
  - requirements.md
  - research.md
---

# Specification: extraVolumes / extraVolumeMounts в чарте helm/amp

**Version:** 1.1 (Scope Change: валидатор `*_file`, review F1)  
**Status:** Approved (автор; задача из Queue с готовой формулировкой владельца)

## Summary

Два новых ключа values — `extraVolumes` (→ `spec.template.spec.volumes`) и `extraVolumeMounts` (→ `volumeMounts` контейнера `amp`). Основной сценарий — Secret с bot_token Telegram и Slack webhook URL, на который `configFile.content` ссылается абсолютными путями через `bot_token_file`/`api_url_file`, так что в ConfigMap остаются только пути.

## Requirements Coverage

| Requirement / Criterion | Covered by |
|---|---|
| ключи в values.yaml с примером | Component Architecture: values.yaml |
| рендер в Pod spec, только `amp` | Target Design |
| пустые значения = прежний рендер | Invariants I1, тест T1 |
| fail на конфликт имени/mountPath | Target Design §Guard, тест T3 |
| render-тест в helm-tests | `helm/amp/tests/render-extra-volumes.sh` |
| k3d: старт с `*_file` из Secret | testing, `evidence/k3d-extra-volumes.md` |
| пример Telegram + Slack | README § Receiver Credentials, CONFIGURATION_GUIDE |

## Current State

- **Code:** `helm/amp/templates/deployment.yaml` — volumeMounts/volumes фиксированы: `data` (lite), `config-file`, `web-config`; блоки под `or (eq profile "lite") configFile.enabled webConfig.existingSecret`.
- **Data:** not applicable.
- **Tests:** `helm/amp/tests/*.sh` (шаг `helm-tests` в `scripts/release-gate.sh`).
- **Docs:** `helm/amp/README.md` § HTTP Authentication (паттерн existingSecret), `docs/CONFIGURATION_GUIDE.md`, `docs/ALERTMANAGER_COMPATIBILITY.md` § `*_file`.

## Design Premises

| Premise | Confirmed by | Class | If wrong |
|---|---|---|---|
| `bot_token_file`/`api_url_file`/`slack_api_url_file` читаются как есть, без rebase от каталога конфига | `go-app/internal/infrastructure/routing/file_secrets.go:207` (`os.ReadFile(file)`); `resolveHTTPConfigFilepaths` трогает только `http_config` (`http_config_filepaths.go`) | call-path-traced | относительный путь в примере не найдётся; мы требуем абсолютный — работает в обоих случаях |
| До `routing.Parse` конфиг проходит `pkg/configvalidator` (`validateAlertmanagerSubset`, `internal/config/config.go:793`) на типах `internal/alertmanager/config`; сейчас он не знает `*_file` и требует inline-значение (E113/E115/E122) | `alertmanager_validation.go:65`, `validators/receiver.go`; k3d `evidence/k3d-extra-volumes.md` §1 | call-path-traced + measured | (premise добавлена по review F2) Slack/webhook/PagerDuty через `*_file` не загружаются — исправляется в Validator Fix |
| upstream ведёт себя так же | `alertmanager@v0.32.0/config/config.go:157` | code-read | только формулировка в доках |
| Вложенный mount в каталог ConfigMap-volume (`/etc/amp/secrets` внутри `/etc/amp`) работает | уже используется `webConfig` (`/etc/amp/web`); проверка k3d | code-read → measured в testing | Secret не виден; пример сменить на `/etc/amp-secrets` |
| Файл Secret читаем `runAsUser: 65534` при `fsGroup: 65534` и `defaultMode: 0440`/`0400` | k3d в testing | assumed → measured | AMP не стартует с «failed to read secret file»; в доке — `defaultMode` по умолчанию |
| Сайдкару config-reloader секреты не нужны | его args: только `--config-file`, URL'ы `/-/reload`, `/health/reload` (`deployment.yaml`) | code-read | reloader упал бы на отсутствующем файле — не так, он его не читает |
| Секреты из файлов перечитываются только на загрузке и `/-/reload` | `file_secrets.go` ROTATION CAVEAT, `ALERTMANAGER_COMPATIBILITY.md` | code-read | только док-ограничение |

## Target Design

В `values.yaml` — `extraVolumes: []`, `extraVolumeMounts: []` рядом с `configFile` (раздел файлов конфигурации), с комментарием: что монтируется только в `amp`, пути в `*_file` — абсолютные, конфликт имён и путей с volume'ами чарта падает на рендере, ротация требует `/-/reload`/рестарта.

В `deployment.yaml`: `$extraVolumes := .Values.extraVolumes | default list`, `$extraVolumeMounts := …` (`default` — для `--reuse-values` со старого чарта). Условия блоков `volumeMounts:` и `volumes:` дополняются этими списками; элементы дописываются в конец через `toYaml | nindent`.

**Guard:** перед рендером `volumeMounts` контейнера `amp` собрать активные имена (`data` при lite, `config-file` при configFile, `web-config` при webConfig) и пути (их mountPath + путь config-файла `<configFile.mountPath>/<fileName>`), сравнить с каждым `extraVolumes[].name` / `extraVolumeMounts[].mountPath` (через `clean`, чтобы `/etc/amp/` == `/etc/amp`). Совпадение → `fail` с указанием конфликтующего ключа чарта. Вложенные пути (`/etc/amp/secrets`) допустимы.

## Validator Fix (Scope Change)

- `internal/alertmanager/config/config.go`: поля-двойники `global.slack_api_url_file`, `global.smtp_auth_password_file`, `slack_configs[].api_url_file`, `webhook_configs[].url_file`, `pagerduty_configs[].routing_key_file`/`service_key_file`, `telegram_configs[].bot_token_file` — полный список FU7-B (`routing/file_secrets.go`). Нужны и для strict-парсера пакета (`KnownFields`).
- `pkg/configvalidator/validators/receiver.go`: E113/E115/E122 считают `*_file` заданным; порядок Slack — inline, file, `global.slack_api_url`, `global.slack_api_url_file`. Значение из файла валидатор не читает и не проверяет формат/HTTPS (E114/E116/E117) — содержимое читает `routing.Parse`.
- Взаимоисключение inline + file и нечитаемый файл остаются за `routing.Parse` (ошибка загрузки с путём, как раньше).
- `ERROR_CODES.md` (E113/E115/E122), `docs/ALERTMANAGER_COMPATIBILITY.md` § `*_file`.
- Тесты: валидатор (каждый `*_file`, «ни значения, ни файла», strict-парсер), `config.LoadConfig` с реальными файлами.

## Component Architecture

- `helm/amp/values.yaml` — два ключа + пример (Secret `amp-receiver-secrets` c ключами `telegram-bot-token`, `slack-webhook-url`, mountPath `/etc/amp/secrets`, `configFile.content` с `bot_token_file`/`api_url_file`).
- `helm/amp/templates/deployment.yaml` — guard + рендер.
- `helm/amp/tests/render-extra-volumes.sh` — render-тест.
- `helm/amp/README.md` — раздел «Receiver Credentials From A Secret» + строки таблицы параметров.
- `docs/CONFIGURATION_GUIDE.md` — тот же пример в разделе про `alertmanager.yaml`/Kubernetes.

## Security Design

- [x] Ownership validation — not applicable (Secret создаёт и контролирует пользователь, чарт его не читает: никакого `lookup`).
- [x] Input validation — guard на конфликты; структура volume'ов валидируется API-сервером.
- [x] Sensitive data not logged — AMP логирует путь, не содержимое (FU7-B); пример использует пути.
- [x] Rate limiting — not applicable.
- [x] Auth/RBAC — RBAC чарта не меняется; доступ к Secret — у kubelet, не у SA AMP.
- Пример — `readOnly: true`, `defaultMode: 0440`, абсолютные пути; явно сказать, что inline `bot_token` вместе с `bot_token_file` — ошибка загрузки.

## Invariants

- [ ] I1: при `extraVolumes: []`/`extraVolumeMounts: []` (и при их отсутствии) рендер всех манифестов идентичен `main` для default / lite / configFile+webConfig / production.
- [ ] I2: extra mounts не попадают в контейнер `config-reloader`.
- [ ] I3: существующие volume'ы чарта рендерятся первыми и без изменений.

## Edge Cases

1. `extraVolumes` задан, `extraVolumeMounts` нет → volume в Pod без монтирования (законно, напр. для будущих сайдкаров); блок `volumes:` появляется даже при standard без configFile.
2. `extraVolumeMounts` без `extraVolumes` → рендер проходит, API отклоняет («not found») — не ловим: volume может прийти из… никуда; оставить API.
3. `name: config-file` при `configFile.enabled` → `fail`; при выключенном configFile — разрешено.
4. `mountPath: /etc/amp/` при `configFile.enabled` → `fail` (через `clean`).
5. `mountPath: /etc/amp/config.yaml` (subPath) при configFile → `fail` (затенил бы конфиг).
6. Профиль lite, `mountPath: /data` → `fail`.
7. Релятивный путь в `bot_token_file` → AMP fail-closed при загрузке конфига («failed to read secret file "<path>"»); документируем «только абсолютные».

## Impact Analysis

- **Affected modules:** чарт helm/amp, доки.
- **Breaking changes:** none.
- **New dependencies:** none.
- **Risks:** вложенный mount в ConfigMap-каталог — подтверждается k3d; ротация Secret без reload — документируем.

## Rollout / Rollback

- **Rollout:** создать Secret, `helm upgrade` с `extraVolumes`/`extraVolumeMounts`, заменить inline-секреты на `*_file`. Ключи приходят с релизом чарта (CHANGELOG [Unreleased] Added).
- **Rollback:** вернуть inline-значения и убрать ключи.
- **Feature flag:** not applicable (пустой список = выключено).

## Observability

- **Logs:** без изменений (ошибка чтения файла уже логируется загрузчиком с путём).
- **Metrics/Alerts:** not applicable.

## Deep Review

- **Mandatory triggers present:** `S`.
- **Discretionary triggers present:** none.
- **Decision:** required.

## Open Questions

- [ ] none
