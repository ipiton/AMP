{{/* vim: set filetype=mustache: */}}
{{/*
Expand the name of the chart.
*/}}
{{- define "amp.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
We truncate at 63 chars because some Kubernetes name fields are limited to this (by the DNS naming spec).
If release name contains chart name it will be used as a full name.
*/}}
{{- define "amp.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{/*
Create chart name and version as used by the chart label.
*/}}
{{- define "amp.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "amp.labels" -}}
helm.sh/chart: {{ include "amp.chart" . }}
{{ include "amp.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "amp.selectorLabels" -}}
app.kubernetes.io/name: {{ include "amp.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "amp.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "amp.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Create PostgreSQL fullname (groundhog2k/postgres)
*/}}
{{- define "amp.postgresql.fullname" -}}
{{- if .Values.postgresql.enabled }}
{{- printf "%s-%s" (include "amp.fullname" .) "postgres" | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" (include "amp.fullname" .) "postgresql" | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}

{{/*
Create PostgreSQL secret name (Custom format)
*/}}
{{- define "amp.postgresql.secretName" -}}
{{- printf "%s-%s" (include "amp.fullname" .) "postgresql-secret" }}
{{- end }}

{{/*
Create KeyDB fullname
*/}}
{{- define "amp.keydb.fullname" -}}
{{- if .Values.cache.enabled }}
{{- printf "%s-%s" (include "amp.fullname" .) "keydb" | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" (include "amp.fullname" .) "cache" | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}

{{/*
Create KeyDB secret name
*/}}
{{- define "amp.keydb.secretName" -}}
{{- printf "%s-%s" (include "amp.fullname" .) "keydb-secret" }}
{{- end }}

{{/*
Return the proper Database URL (groundhog2k/postgres)
*/}}
{{- define "amp.databaseUrl" -}}
{{- if .Values.postgresql.enabled }}
{{- printf "postgresql://%s:$(POSTGRES_PASSWORD)@%s:5432/%s" .Values.postgresql.username (include "amp.postgresql.fullname" .) .Values.postgresql.database }}
{{- else }}
{{- printf "sqlite:///data/alert_history.sqlite3" }}
{{- end }}
{{- end }}

{{/*
Secret holding the password of the chart's Valkey (<fullname>-redis): the
operator's valkey.password.existingSecret, or the one templates/redis-secret.yaml
creates. Valkey, its exporter and AMP all read it, so the three never disagree.
*/}}
{{- define "amp.valkey.secretName" -}}
{{- .Values.valkey.password.existingSecret | default (printf "%s-redis-secret" (include "amp.fullname" .)) }}
{{- end }}

{{/*
"true" when cache.host names the chart's Valkey Service: <fullname>-redis, or
a longer DNS name of it in the release namespace (<fullname>-redis.<ns>,
...svc.cluster.local). The same name in another namespace is someone else's.
*/}}
{{- define "amp.cache.hostIsValkey" -}}
{{- $svc := printf "%s-redis" (include "amp.fullname" .) }}
{{- $nsSvc := printf "%s.%s" $svc .Release.Namespace }}
{{- $host := tpl .Values.cache.host . }}
{{- if or (eq $host $svc) (eq $host $nsSvc) (hasPrefix (printf "%s." $nsSvc) $host) }}true{{ end }}
{{- end }}

{{/*
"true" when AMP talks to the chart's own Valkey: it runs (valkey.replicas > 0,
its only gate besides the profile) AND cache.host points at it. AMP then reads
Valkey's password; otherwise the cache is external and cache.auth.* applies,
also when the chart's Valkey still runs next to it unused.
*/}}
{{- define "amp.cache.bundled" -}}
{{- if and (gt (int .Values.valkey.replicas) 0) (include "amp.cache.hostIsValkey" .) }}true{{ end }}
{{- end }}

{{/*
Key of the password in amp.valkey.secretName: valkey.password.secretKey for an
existingSecret; the chart's own Secret always stores it under "password".
*/}}
{{- define "amp.valkey.secretKey" -}}
{{- if .Values.valkey.password.existingSecret }}
{{- .Values.valkey.password.secretKey | default "password" }}
{{- else -}}
password
{{- end }}
{{- end }}

{{/*
Build canonical publishing target config and return it base64-encoded.
Expected input: dict "target" <map>
*/}}
{{- define "amp.publishingTargetConfigB64" -}}
{{- $target := .target -}}
{{- $headers := dict -}}
{{- range $key, $value := (default (dict) $target.headers) }}
{{- $_ := set $headers $key $value -}}
{{- end }}
{{- with $target.secret }}
{{- if .apiKey }}
{{- if eq $target.type "webhook" }}
{{- $_ := set $headers "X-API-Key" .apiKey -}}
{{- else }}
{{- $_ := set $headers "Authorization" (printf "Bearer %s" .apiKey) -}}
{{- end }}
{{- end }}
{{- if .token }}
{{- $_ := set $headers "Authorization" (printf "Bearer %s" .token) -}}
{{- end }}
{{- if .authHeader }}
{{- $_ := set $headers "Authorization" .authHeader -}}
{{- end }}
{{- if .routingKey }}
{{- $_ := set $headers "routing_key" .routingKey -}}
{{- end }}
{{- range $key, $value := (default (dict) .customHeaders) }}
{{- $_ := set $headers $key $value -}}
{{- end }}
{{- end }}
{{- $enabled := true -}}
{{- if hasKey $target "enabled" -}}
{{- $enabled = $target.enabled -}}
{{- end -}}
{{- $config := dict
  "name" $target.name
  "type" $target.type
  "url" $target.url
  "enabled" $enabled
  "filter_config" (default (dict) $target.filterConfig)
  "headers" $headers
  "format" (default $target.type $target.format)
-}}
{{- toJson $config | b64enc -}}
{{- end }}
