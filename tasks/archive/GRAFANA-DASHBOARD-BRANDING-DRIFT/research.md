# Research: GRAFANA-DASHBOARD-BRANDING-DRIFT

## Context

После cleanup secondary docs/examples в репозитории остался отдельный Grafana residual: `grafana/dashboards/alert-history-service.json` все еще держит historical branding.

На входе уже были подтверждены два top-level drift-маркера:

- dashboard title: `AMP - Alert History Service`
- dashboard uid: `amp-alert-history`

Цель research — понять, какой следующий slice вообще честен:

- visible-title-only cleanup;
- title + identity-field cleanup;
- или более широкий dashboard/provisioning split.

---

## Files Reviewed

- `grafana/dashboards/alert-history-service.json`
- `docs/06-planning/BUGS.md`
- `docs/06-planning/DECISIONS.md`
- `docs/CONFIGURATION_GUIDE.md`
- `README.md`
- `tasks/archive/SECONDARY-REPO-DOC-HISTORICAL-DRIFT/research.md`

Searches:

- `rg -n "amp-alert-history|Alert History Service|alert-history-service.json|AMP - Alert History Service" -S .`
- `rg -n 'grafana|dashboard|uid' helm go-app docs README.md CONTRIBUTING.md -S`
- `find . -path '*/provisioning/*' -type f`

---

## Findings

### 1. Confirmed branding drift inside the JSON is currently narrow

В самом `grafana/dashboards/alert-history-service.json` confirmed historical markers сейчас узкие и top-level:

- `"title": "AMP - Alert History Service"`
- `"uid": "amp-alert-history"`

Targeted search по самому JSON не нашел дополнительных `AMP` / `Alert History` / `alert-history` markers глубже в panels, tags или templating.

Это важно, потому что bug звучит как “Grafana dashboard branding drift”, но фактический in-file cleanup на текущий момент уже не тянет broad text sweep.

### 2. `title` и `uid` относятся к разным классам риска

`title`:

- operator-facing visible label;
- меняется как wording cleanup;
- не тянет автоматически behavioral implications внутри самого JSON.

`uid`:

- identity-shaped field;
- может использоваться для stable import/update semantics в Grafana;
- даже если in-repo references не найдены, external operators могут ссылаться на dashboard именно по `uid`.

Вывод: эти два поля нельзя считать равнозначными “branding strings”.

### 3. In-repo owner for provisioning/import assumptions is not present

Research не нашел в текущем repo активного provisioning path для этого dashboard:

- в `grafana/` лежит только `grafana/dashboards/alert-history-service.json`;
- файлов под `grafana/provisioning/**` в рабочем дереве сейчас нет;
- Helm/chart paths не показывают прямого wiring этого JSON;
- in-repo search не нашел ссылок на `amp-alert-history` вне самого JSON и planning artifacts.

Это снижает уверенность, что `uid` уже жестко завязан на текущую ветку, но не делает его “безопасным по определению”:

- отсутствие in-repo references не доказывает отсутствие external import/provisioning usage;
- именно поэтому `uid` change все равно требует отдельного conscious scope decision.

### 4. Filename itself is also historical-shaped, but не лучший первый target

Файл называется `alert-history-service.json`, что тоже несет historical identity.

Однако rename файла относится к тому же risk class, что и `uid`:

- это уже filesystem/import identity;
- even without in-repo references, external automation может читать именно этот path.

Следовательно, rename файла не выглядит честным первым slice для narrow branding cleanup.

### 5. Repo truth today does not require dashboard identity churn

Current planning truth уже сдвинут к:

- `AMP` branding;
- active-runtime-first policy;
- partial/honest dashboard/runtime surface.

Но ни `README.md`, ни `DECISIONS.md`, ни текущие Grafana-related docs не требуют обязательной смены dashboard `uid` для того, чтобы сделать репозиторий честнее.

То есть visible title cleanup выглядит достаточным для первого mergeable slice, а `uid`/filename churn не является явно demanded by current source of truth.

### 6. There is no realistic code/test gate for this file

Для standalone dashboard JSON в текущем repo нет meaningful code-level test harness.

Честный verification path для следующего slice будет состоять из:

- targeted marker scan;
- JSON sanity parse (`jq`);
- manual review против planning/docs truth;
- `git diff --check`.

---

## Option Assessment

### Option A — visible-title-only cleanup

Что меняется:

- только top-level `"title"` в `grafana/dashboards/alert-history-service.json`

Плюсы:

- самый узкий и mergeable slice;
- закрывает operator-facing historical branding;
- не трогает identity/provisioning semantics.

Минусы:

- `uid` и filename останутся historical-shaped;
- bug придется либо явно закрывать как narrow slice с residual, либо заранее split'ить остаток.

### Option B — title + `uid` cleanup в одном проходе

Что меняется:

- `"title"`
- `"uid"`

Плюсы:

- более полная branding cleanup внутри JSON.

Минусы:

- `uid` — уже identity field, а не просто wording;
- в текущем repo нет подтвержденного provisioning owner, который дал бы безопасный green-light;
- есть риск скрытого изменения import/update behavior без достаточно сильного verification path.

### Option C — title + `uid` + filename cleanup

Что меняется:

- `"title"`
- `"uid"`
- `grafana/dashboards/alert-history-service.json` path

Плюсы:

- максимальная historical-brand removal внутри grafana artifact.

Минусы:

- это уже точно identity/provisioning work;
- verification path в текущем repo для такого изменения слабый;
- слишком широкий first slice для bug, который еще не исследован до конца.

---

## Recommendation

Самый честный следующий `/spec` — **Option A: visible-title-only cleanup**.

То есть:

- ограничить scope только `grafana/dashboards/alert-history-service.json`;
- поменять только top-level visible dashboard title;
- явно оставить `uid` и filename вне первого implementation slice;
- не открывать provisioning/import behavior без отдельного решения.

Важно зафиксировать это в будущем spec явно:

- `uid = amp-alert-history` не считается “безопасным брендинг-текстом”;
- rename файла тоже out of scope;
- если после visible-title cleanup останется желание почистить `uid`/path, это отдельный follow-up slice, а не скрытый бонус к текущему.

---

## Proposed `/spec` Scope

Recommended in-scope:

- `grafana/dashboards/alert-history-service.json`
- только top-level visible dashboard title

Recommended out of scope:

- `uid`
- rename `alert-history-service.json`
- any Grafana provisioning/import work
- PromQL, panels, datasource wiring, thresholds, layout

---

## Verification Notes For The Next Slice

Если `/spec` пойдет по recommendation выше, следующий verification path может быть таким:

- `rg -n "AMP - Alert History Service|amp-alert-history|alert-history-service" grafana/dashboards/alert-history-service.json`
- `jq '{title,uid,version}' grafana/dashboards/alert-history-service.json`
- manual review against:
  - `docs/06-planning/BUGS.md`
  - `docs/06-planning/DECISIONS.md`
  - `README.md`
- `git diff --check`

Ожидаемая интерпретация:

- `AMP - Alert History Service` должен исчезнуть;
- `uid = amp-alert-history` может остаться, если spec честно фиксирует его как out of scope;
- JSON должен остаться syntactically valid.

---

## Next-Step Implication

Research подтверждает, что bug уже, чем звучит по названию.

Следующий честный `/spec`:

- не должен обещать full dashboard identity cleanup;
- должен выбирать narrow visible-title slice;
- должен заранее зафиксировать, что `uid` и filename сознательно остаются вне первого pass.
