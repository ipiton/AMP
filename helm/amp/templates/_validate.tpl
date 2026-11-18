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
{{- /* Keys removed inside sections that still exist: section -> key -> hint. */}}
{{- $removedNested := dict
  "gracefulShutdown" (dict "timeoutSeconds" "use gracefulShutdown.terminationGracePeriodSeconds")
  "llm" (dict
    "batchSize" "removed: each alert is classified on its own"
    "cacheTtl" "removed: amp-upstream has no classification cache"
    "retryDelay" "removed: retries back off on their own and honour Retry-After; use llm.maxRetries")
  "publishing" (dict
    "queue" "removed: delivery, retries and its queue are upstream's notify pipeline"
    "templates" "notification templates go to alertmanager.config templates"
    "health" "removed: upstream reports delivery failures in alertmanager_notifications_failed_total")
  "service" (dict
    "metricsPort" "metrics are served on service.port (9093) at /metrics"
    "loadBalancerPolicy" "removed: the Service has no session affinity"
    "sessionAffinityTimeout" "removed: the Service has no session affinity") }}
{{- range $section := keys $removedNested | sortAlpha }}
{{- $vals := get $.Values $section }}
{{- if kindIs "map" $vals }}
{{- $hints := get $removedNested $section }}
{{- range $key := keys $hints | sortAlpha }}
{{- if hasKey $vals $key }}
{{- fail (printf "%s.%s is no longer read since UPSTREAM-CORE-REMOVAL: %s. Remove it from your values (see CHANGELOG.md)." $section $key (get $hints $key)) }}
{{- end }}
{{- end }}
{{- end }}
{{- end }}
{{- $source := $am.configSource | default "values" }}
{{- if not (has $source (list "values" "operator")) }}
{{- fail (printf "alertmanager.configSource must be values or operator, got %q." $source) }}
{{- end }}
{{- if eq $source "operator" }}
{{- $op := $am.operator | default dict }}
{{- if not (kindIs "map" (dig "reloader" "image" nil $op)) }}
{{- fail "alertmanager.configSource=operator needs alertmanager.operator.reloader.image: with helm upgrade use --reset-then-reuse-values (Helm >= 3.14) or a values file for this chart, not --reuse-values." }}
{{- end }}
{{- if not (include "amp.monitoringCRDs" .) }}
{{- fail "alertmanager.configSource=operator needs prometheus-operator: the cluster does not serve monitoring.coreos.com/v1. Install it, or for a render without a cluster pass --api-versions monitoring.coreos.com/v1 or set monitoring.assumeCRDs=true." }}
{{- end }}
{{- /* RBAC cannot filter `list` by label: the discovery Role would read the generated Secret with the tokens of every selected AlertmanagerConfig (ADR-012, ADR-020). */}}
{{- $releaseNamespace := include "amp.namespace" . }}
{{- if and (include "amp.discoveryEnabled" .) (eq (.Values.publishing.discovery.namespace | default $releaseNamespace) $releaseNamespace) }}
{{- fail (printf "alertmanager.configSource=operator puts AlertmanagerConfig credentials into a Secret of namespace %s, and publishing target discovery lists every Secret there. Set publishing.discovery.namespace to another namespace, or publishing.enabled=false." $releaseNamespace) }}
{{- end }}
{{- $reloadURL := dig "reloader" "reloadURLSecret" dict $op }}
{{- if and $reloadURL.name (not $reloadURL.key) }}
{{- fail "alertmanager.operator.reloader.reloadURLSecret.key is required with reloadURLSecret.name." }}
{{- end }}
{{- $webConfigFile := or .Values.webConfig.existingSecret (dig "server" "web_config_file" "" ($am.ampConfig | default dict)) }}
{{- if and $webConfigFile (not $reloadURL.name) }}
{{- fail "HTTP authentication is on, so the config reloader cannot call /-/reload: set alertmanager.operator.reloader.reloadURLSecret to a Secret key holding http://<user>:<url-encoded password>@127.0.0.1:9093<path prefix>/-/reload." }}
{{- end }}
{{- end }}
{{- end }}
