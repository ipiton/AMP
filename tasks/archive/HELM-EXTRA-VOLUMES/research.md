---
id: HELM-EXTRA-VOLUMES
slug: helm-extra-volumes
stream: operations
type: feature
artifact: research-pack
status: final
created_at: 2026-10-20
updated_at: 2026-10-20
---

# Research Pack - extraVolumes / extraVolumeMounts

## 0) TL;DR

- Solving: смонтировать пользовательский Secret/ConfigMap в контейнер `amp`, чтобы креды receiver'ов шли через `*_file`.
- Found: `*_file`-поля секретов интеграций (`bot_token_file`, `api_url_file`, `slack_api_url_file`, …) читаются `os.ReadFile(path)` как есть — **относительный путь резолвится от CWD процесса, не от каталога конфига**. Rebase от каталога конфига есть только у `http_config`-путей (`ca_file`, `password_file`, …). Upstream Alertmanager v0.32 ведёт себя так же. Значит в примерах — только абсолютные пути (mountPath Secret'а).
- Chose: стандартная пара `extraVolumes`/`extraVolumeMounts` через `toYaml`, только контейнер `amp`; `fail` на совпадение имени/mountPath с volume'ами чарта.
- Risks: пользователь пишет относительный путь (`secrets/bot_token`) → ошибка загрузки конфига «failed to read secret file» (fail-closed, не утечка); ротация Secret не подхватывается до `/-/reload`.
- Next step: -> spec

## 1) Questions

1. Какие `*_file`-поля поддерживает AMP и как резолвятся пути?
2. Какие volume'ы/mountPath уже есть у контейнера `amp`, где возможен конфликт?
3. Нужны ли extra volumes сайдкару config-reloader / initContainers?
4. Права на файлы под `runAsUser: 65534`, `readOnlyRootFilesystem`?
5. Что с ротацией секрета?

## 2) Findings

- **`*_file` (FU7-B):** `go-app/internal/infrastructure/routing/file_secrets.go` — `global.slack_api_url_file`, `global.smtp_auth_password_file`, `webhook_configs[].url_file`, `pagerduty_configs[].routing_key_file`/`service_key_file`, `slack_configs[].api_url_file`, `telegram_configs[].bot_token_file`. `resolveFileSecret` → `os.ReadFile(file)` без rebase; inline XOR file (оба — ошибка загрузки), хвостовые `\r\n\t ` срезаются. Вызывается из `Parse()` до `resolveGlobalFallbacks`.
- **Rebase путей:** `go-app/internal/config/http_config_filepaths.go` (`resolveHTTPConfigFilepaths`, `config.go:808`) — только `http_config` (tls/basic_auth/authorization/oauth2). Upstream `alertmanager@v0.32.0/config/config.go:157 resolveFilepaths` — тоже только templates + `HTTPConfig.SetDirectory`; `bot_token_file` и `api_url_file` upstream не rebase'ит. Запись в BACKLOG («относительные пути резолвятся от каталога конфига») для этих полей неверна — поправлено в доках задачи.
- **Ротация:** секреты из файлов читаются при загрузке и на каждом `/-/reload`/SIGHUP, не на каждую отправку (`docs/ALERTMANAGER_COMPATIBILITY.md` § `*_file` secret variants). config-reloader хеширует только config-файл → смена Secret сама reload не вызывает.
- **Статус API:** путь `*_file` виден в `/api/v2/status` (`fileSecretRefKeys`), резолвнутое значение редактируется.
- **Volume'ы `amp` сейчас (`templates/deployment.yaml`):** `data` → `liteProfile.persistence.mountPath` (`/data`, lite), `config-file` → `configFile.mountPath` (`/etc/amp`, ConfigMap), `web-config` → `webConfig.mountPath` (`/etc/amp/web`, Secret). Вложенный mount внутрь ConfigMap-volume уже используется (`/etc/amp/web` в `/etc/amp`) — значит `/etc/amp/secrets` рабочий путь. Блоки `volumeMounts:`/`volumes:` сейчас под условием `or (lite) configFile webConfig`.
- **Kubernetes:** API-сервер отклоняет повтор `volumes[].name` и повтор `mountPath` в одном контейнере — но только на apply; `helm template` это не ловит.
- **Права:** `podSecurityContext.fsGroup: 65534`, `runAsUser: 65534`; Secret-volume по умолчанию `defaultMode: 0644` — файл читаем. Со строгим `defaultMode` (0400) kubelet при заданном `fsGroup` сам выставляет группу 65534 и добавляет `g+r` (`SetVolumeOwnership`) — проверяется в k3d (testing), в примере `defaultMode: 0440`. `readOnlyRootFilesystem` не мешает: mount — отдельный volume.
- **Сайдкар/initContainers:** config-reloader читает только config-файл (хеш) и ходит на `/-/reload`; секреты ему не нужны. У `amp` initContainers нет. → extra volumes только для `amp`. Если позже понадобятся — отдельные ключи (`configReloader.extraVolumeMounts`), не расширение этих.

## 3) Options

`Generation:` single-pass

### Option A - `extraVolumes`/`extraVolumeMounts`, сырой toYaml (де-факто стандарт чартов)

- **Pros:** любой тип volume (Secret, ConfigMap, projected, CSI secrets-store), привычно операторам, минимальный шаблон.
- **Cons:** нет схемы; ошибки структуры ловит только API.
- **Cost/Risk:** low

### Option B - типизированный `receiverSecrets.existingSecret` + фиксированный mountPath

- **Pros:** одна строка values, путь известен.
- **Cons:** только Secret, не решает runbooks, второй слой поверх того же; противоречит формулировке задачи.
- **Cost/Risk:** low, но узко

## 4) Decision

- **Chosen:** A.
- **Why:** покрывает обе мотивации (Secret кредов и ConfigMap runbooks); повторяет принятый в экосистеме контракт; дефолт `[]` не меняет рендер; конфликты имён/путей с чартом дёшево ловятся `fail`.

## 5) Spec Inputs

- API/contracts: два новых ключа values, аддитивно.
- Data model/migrations: not applicable.
- Rollout/rollback: `helm upgrade` с новыми ключами; откат — убрать ключи (возврат секретов inline).
- Observability: not applicable (ошибка чтения файла уже логируется загрузчиком конфига с путём).
- Security/ownership/RBAC: Secret создаёт пользователь; чарт его не читает; пример — абсолютные пути, `readOnly: true`, `defaultMode: 0440`.

## 6) References

- Files: `go-app/internal/infrastructure/routing/file_secrets.go`, `go-app/internal/config/http_config_filepaths.go`, `helm/amp/templates/deployment.yaml`, `helm/amp/values.yaml`
- Docs: `docs/ALERTMANAGER_COMPATIBILITY.md` § `*_file` secret variants; § http_config relative paths
- Upstream: `~/go/pkg/mod/github.com/prometheus/alertmanager@v0.32.0/config/config.go:157`
