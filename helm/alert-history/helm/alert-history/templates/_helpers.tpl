{{/* vim: set filetype=mustache: */}}
{{/*
Expand the name of the chart.
*/}}
{{- define "alert-history.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
We truncate at 63 chars because some Kubernetes name fields are limited to this (by the DNS naming spec).
If release name contains chart name it will be used as a full name.
*/}}
{{- define "alert-history.fullname" -}}
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
{{- define "alert-history.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "alert-history.labels" -}}
helm.sh/chart: {{ include "alert-history.chart" . }}
{{ include "alert-history.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "alert-history.selectorLabels" -}}
app.kubernetes.io/name: {{ include "alert-history.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "alert-history.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "alert-history.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Create CloudNativePG cluster fullname
*/}}
{{- define "alert-history.postgresql.fullname" -}}
{{- if .Values.postgresql.enabled }}
{{- .Values.postgresql.cluster.name | default (printf "%s-postgres" (include "alert-history.fullname" .)) }}
{{- else }}
{{- printf "%s-%s" (include "alert-history.fullname" .) "postgresql" | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}

{{/*
Create PostgreSQL secret name (CloudNativePG format)
*/}}
{{- define "alert-history.postgresql.secretName" -}}
{{- if .Values.postgresql.enabled }}
{{- printf "postgres.%s.credentials.postgresql.acid.zalan.do" (include "alert-history.postgresql.fullname" .) }}
{{- else }}
{{- printf "%s-%s" (include "alert-history.fullname" .) "postgresql-secret" }}
{{- end }}
{{- end }}

{{/*
Create KeyDB fullname
*/}}
{{- define "alert-history.keydb.fullname" -}}
{{- if .Values.cache.enabled }}
{{- printf "%s-%s" (include "alert-history.fullname" .) "keydb" | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" (include "alert-history.fullname" .) "cache" | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}

{{/*
Create KeyDB secret name
*/}}
{{- define "alert-history.keydb.secretName" -}}
{{- printf "%s-%s" (include "alert-history.fullname" .) "keydb-secret" }}
{{- end }}

{{/*
Return the proper Database URL (CloudNativePG)
*/}}
{{- define "alert-history.databaseUrl" -}}
{{- if .Values.postgresql.enabled }}
{{- printf "postgresql://%s:$(POSTGRES_PASSWORD)@%s-rw:5432/%s" .Values.postgresql.cluster.bootstrap.initdb.owner (include "alert-history.postgresql.fullname" .) .Values.postgresql.cluster.bootstrap.initdb.database }}
{{- else }}
{{- printf "sqlite:///data/alert_history.sqlite3" }}
{{- end }}
{{- end }}

{{/*
Return the proper KeyDB URL
*/}}
{{- define "alert-history.keydbUrl" -}}
{{- if .Values.cache.enabled }}
{{- if .Values.keydb.auth.enabled }}
{{- printf "redis://:%s@%s:%g/0" .Values.keydb.auth.password (include "alert-history.keydb.fullname" .) .Values.keydb.keydb.port }}
{{- else }}
{{- printf "redis://%s:%g/0" (include "alert-history.keydb.fullname" .) .Values.keydb.keydb.port }}
{{- end }}
{{- else }}
{{- printf "redis://localhost:6379/0" }}
{{- end }}
{{- end }}

{{/*
Return the proper Redis URL (legacy compatibility)
*/}}
{{- define "alert-history.redisUrl" -}}
{{- include "alert-history.keydbUrl" . }}
{{- end }}
