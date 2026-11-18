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
{{- if include "amp.operatorResources" . }}
{{- $op := $am.operator | default dict }}
{{- if not (and (dig "reloader" "image" "repository" "" $op) (dig "reloader" "image" "tag" "" $op)) }}
{{- fail "alertmanager.configSource=operator needs alertmanager.operator.reloader.image.repository and .tag: with helm upgrade use --reset-then-reuse-values (Helm >= 3.14) or a values file for this chart, not --reuse-values." }}
{{- end }}
{{- if not (or (.Capabilities.APIVersions.Has "monitoring.coreos.com/v1/Alertmanager") (dig "assumeCRDs" false (.Values.monitoring | default dict))) }}
{{- fail "alertmanager.configSource=operator needs prometheus-operator: the cluster does not serve monitoring.coreos.com/v1 Alertmanager. Install it, or for a render without a cluster pass --api-versions monitoring.coreos.com/v1/Alertmanager or set monitoring.assumeCRDs=true." }}
{{- end }}
{{- /* The generated Secret holds AlertmanagerConfig credentials. RBAC cannot filter `list` by label, and a token mounted at pod level would also reach the reloader container (ADR-012, ADR-020). */}}
{{- if include "amp.discoveryEnabled" . }}
{{- fail "alertmanager.configSource=operator (or alertmanager.operator.prepare) cannot be combined with publishing target discovery: the generated Secret holds AlertmanagerConfig credentials, and the pod must not carry a Kubernetes API token. Set publishing.enabled=false." }}
{{- end }}
{{- if include "amp.operatorConfigSource" . }}
{{- /* The reloader holds no credential ($(VAR) in an AlertmanagerConfig would read it), so with authentication /-/reload has to be open. */}}
{{- $ampConfig := $am.ampConfig | default dict }}
{{- $auth := or .Values.webConfig.existingSecret (dig "server" "web_config_file" "" $ampConfig) }}
{{- range $am.extraArgs | default list }}
{{- if hasPrefix "--web.config.file" (toString .) }}{{- $auth = true }}{{- end }}
{{- end }}
{{- range .Values.extraEnv | default list }}
{{- if eq (toString .name) "SERVER_WEB_CONFIG_FILE" }}{{- $auth = true }}{{- end }}
{{- end }}
{{- if and $auth (not (has "/-/reload" (dig "server" "auth" "unauthenticated_paths" list $ampConfig))) }}
{{- fail "HTTP authentication is on, and the config reloader carries no credentials: add /-/reload to alertmanager.ampConfig.server.auth.unauthenticated_paths, keeping /-/healthy and /-/ready. See \"AlertmanagerConfig via prometheus-operator\" in the chart README." }}
{{- end }}
{{- end }}
{{- end }}
{{- end }}
