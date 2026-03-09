# Task: PHASE-2-BOOTSTRAP-CONSOLIDATION

## Context
`go-app/cmd/server/main.go` вырос до ~3900 строк и превратился в God Object. Он смешивает инициализацию инфраструктуры, бизнес-логику хранилищ, конфигурацию HTTP-сервера и роутинг. Это затрудняет тестирование и нарушает SRP (Single Responsibility Principle).

## Goals
- Разделить `main.go` на логические компоненты.
- Вынести bootstrap-логику (инициализацию сервисов) в `ServiceRegistry`.
- Вынести конфигурацию роутинга в отдельный пакет/файл.
- Получить чистый `main.go` с минимальным кодом запуска.

## Requirements
- [ ] Инкапсулировать создание хранилищ (`alert_state_store.go`, `silence_state_store.go`) внутри `ServiceRegistry`.
- [ ] Перенести регистрацию HTTP-маршрутов из `registerRoutes` в структурированный `Router`.
- [ ] Сохранить работоспособность всех существующих контрактных тестов (`make test-upstream-parity`).
- [ ] Устранить прямую зависимость HTTP-хендлеров от глобальных переменных (если они есть).

## Technical Debt Addressed
- **GOD-OBJECT-MAIN**: Прямое разделение гигантского файла.
- **STATE-STORE-LEAK**: Хранилища перемещаются из `cmd/server` в `internal/application` или `internal/storage`.

## Implementation Plan
- **Step 1: Code Audit & Mapping**: Определить границы ответственности `ServiceRegistry` и `Router`.
- **Step 2: ServiceRegistry Refactoring**: Добавить поддержку `alertStore` и `silenceStore` в `ServiceRegistry`.
- **Step 3: Route Extraction**: Создать `internal/application/router.go` и перенести туда логику из `registerRoutes`.
- **Step 4: Handler Relocation**: Вынести хендлеры из `main.go` в подпакет `handlers`.
- **Step 5: Main Cleanup**: Сократить `main.go` до < 500 строк.

## Definition of Done (DoD)
- [ ] Проект успешно собирается (`go build ./cmd/server`).
- [ ] Все тесты проходят (`make test`).
- [ ] `main.go` содержит только код запуска и настройки верхнего уровня.
- [ ] Отсутствуют циклические зависимости после рефакторинга.
