# Мутации (write-tests, шаг 3.6) — 2026-10-13

Код @ `1383013` (`bugfix/prod-hardcoded-filter`). Каждая мутация — временная правка `go-app/internal/core/services/filter_engine.go`, прогон `go test ./internal/core/services/ -run 'SimpleFilterEngine|Dedup|AlertProcessor_Filter' -count=1`, откат `git checkout`.

| # | Мутация | Упавшие тесты |
|---|---|---|
| M1 | Вернуть правило `test*` по имени (`return true, "test_alert"` в начале `shouldBlockInternal`) | `TestAlertProcessor_Filter_DeliversTestLookingAlerts`, `_RepeatWithinWindowPublishedOnce`, `_FlapPublishedOnEveryStatusChange`, `TestSimpleFilterEngine_ShouldBlock_TestAlerts`, `_CombinedRules` |
| M2 | Ключ Rule 7 `fingerprint\|status` вместо fingerprint (обе точки: чтение и запись) | `TestSimpleFilterEngine_Dedup_FlapWithinWindowPasses`, `TestAlertProcessor_Filter_FlapPublishedOnEveryStatusChange` |
| M3 | Убрать сравнение статуса (`last.status == alert.Status &&`) | `TestSimpleFilterEngine_Dedup_FlapWithinWindowPasses`, `_StatusChangePasses`, `_ConcurrentStatusChanges`, `TestAlertProcessor_Filter_FlapPublishedOnEveryStatusChange` |

Все три мутации пойманы. После отката `git status` — только тестовые файлы задачи.
