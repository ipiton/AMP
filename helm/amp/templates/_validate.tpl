{{- /*
amp.validate fails the render on values of the chart before
UPSTREAM-CORE-REMOVAL. Included by every template that reads
.Values.alertmanager, so the first one rendered reports it instead of a nil
pointer. The removed keys are not in values.yaml: when they are set, they
came from the operator, who would otherwise get a core that delivers nothing
they configured (routes in configFile.content, upstreamCore.config).
*/}}
{{- define "amp.validate" -}}
{{- $am := .Values.alertmanager }}
{{- if not (and (kindIs "map" $am) (kindIs "map" $am.persistence)) }}
{{- fail "values of a chart before UPSTREAM-CORE-REMOVAL (no alertmanager.* section): with helm upgrade use --reset-then-reuse-values (Helm >= 3.14) or a values file for this chart, not --reuse-values. See CHANGELOG.md (UPSTREAM-CORE-REMOVAL)." }}
{{- end }}
{{- $removed := dict
  "configFile" "routes and receivers go to alertmanager.config (upstream alertmanager.yml); AMP settings to alertmanager.ampConfig"
  "upstreamCore" "renamed to alertmanager.*"
  "replicaCount" "use alertmanager.replicas"
  "autoscaling" "no HPA: use alertmanager.replicas"
  "strategy" "the StatefulSet rolls pods one by one"
  "configReloader" "removed: POST /-/reload or a rollout restart"
  "cache" "removed: amp-upstream does not use a cache (UPSTREAM-AI-HA-DEDUP)"
  "grouping" "group_by and timers go to alertmanager.config route"
  "silencing" "silences are upstream's, kept on alertmanager.persistence"
  "storage" "use alertmanager.persistence"
  "persistence" "use alertmanager.persistence"
  "liteProfile" "use alertmanager.persistence"
  "server" "use alertmanager.externalURL and alertmanager.ampConfig.server"
  "logLevel" "removed: amp-upstream has no log level flag"
  "alerts" "removed"
  "migration" "removed" }}
{{- range $key := keys $removed | sortAlpha }}
{{- if hasKey $.Values $key }}
{{- fail (printf "%s is no longer read since UPSTREAM-CORE-REMOVAL: %s. Remove it from your values (see CHANGELOG.md)." $key (get $removed $key)) }}
{{- end }}
{{- end }}
{{- end }}
