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
Return the proper Valkey/Redis URL
*/}}
{{- define "amp.cacheUrl" -}}
{{- if .Values.cache.enabled }}
{{- if .Values.cache.auth.enabled }}
{{- printf "redis://:%s@%s:%g/0" .Values.cache.auth.password .Values.cache.host .Values.cache.port }}
{{- else }}
{{- printf "redis://%s:%g/0" .Values.cache.host .Values.cache.port }}
{{- end }}
{{- else }}
{{- printf "redis://localhost:6379/0" }}
{{- end }}
{{- end }}

{{/*
Return the proper Redis URL (legacy compatibility)
*/}}
{{- define "amp.redisUrl" -}}
{{- include "amp.cacheUrl" . }}
{{- end }}
