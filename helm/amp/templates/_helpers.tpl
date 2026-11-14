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
Secret holding the password of the chart's Valkey (<fullname>-redis): the
operator's valkey.password.existingSecret, or the one templates/redis-secret.yaml
creates. Valkey and its exporter both read it, so the two never disagree.
*/}}
{{- define "amp.valkey.secretName" -}}
{{- .Values.valkey.password.existingSecret | default (printf "%s-redis-secret" (include "amp.fullname" .)) }}
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

{{/*
The AMP pods (the StatefulSet): selectorLabels plus component=application.
The postgres and Valkey pods share selectorLabels with their own component,
so everything that means "AMP" selects by this, and the postgres/Valkey
NetworkPolicies let these pods in by component=application.
*/}}
{{- define "amp.appSelectorLabels" -}}
{{ include "amp.selectorLabels" . }}
app.kubernetes.io/component: application
{{- end }}

{{/* Headless Service: stable pod DNS names for --cluster.peer and Prometheus. */}}
{{- define "amp.headlessName" -}}
{{- printf "%s-headless" (include "amp.fullname" .) | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/* Namespace of the AMP objects. */}}
{{- define "amp.namespace" -}}
{{- .Values.namespace | default .Release.Namespace }}
{{- end }}

{{/* --web.external-url. */}}
{{- define "amp.externalURL" -}}
{{- .Values.alertmanager.externalURL | default (printf "http://%s.%s.svc:%v/" (include "amp.fullname" .) (include "amp.namespace" .) .Values.service.port) }}
{{- end }}

{{/* Path prefix of every endpoint, probes included: the external URL's path. */}}
{{- define "amp.pathPrefix" -}}
{{- (urlParse (include "amp.externalURL" .)).path | trimSuffix "/" }}
{{- end }}

{{/* "true" when the pods need the Kubernetes API: target discovery (templates/rbac.yaml). */}}
{{- define "amp.discoveryEnabled" -}}
{{- if and (eq .Values.profile "standard") .Values.publishing.enabled }}true{{ end }}
{{- end }}

{{/*
"true" when prometheus-operator objects (ServiceMonitor, PrometheusRule) can be
applied: the cluster serves monitoring.coreos.com/v1, or monitoring.assumeCRDs
says so for a render without a cluster (helm template without --api-versions).
Without it a default install on a cluster without the CRDs fails with
`no matches for kind "ServiceMonitor"` (HELM-MONITORING-CRD-GATE).
*/}}
{{- define "amp.monitoringCRDs" -}}
{{- if or (.Capabilities.APIVersions.Has "monitoring.coreos.com/v1") (dig "assumeCRDs" false (.Values.monitoring | default dict)) }}true{{ end }}
{{- end }}
