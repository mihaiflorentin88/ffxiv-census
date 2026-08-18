{{/* Expand the name of the chart. */}}
{{- define "manifest.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/* Create a default fully qualified app name. */}}
{{- define "manifest.fullname" -}}
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

{{/* Chart name and version label */}}
{{- define "manifest.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/* Common labels */}}
{{- define "manifest.labels" -}}
helm.sh/chart: {{ include "manifest.chart" . }}
{{ include "manifest.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/* Selector labels */}}
{{- define "manifest.selectorLabels" -}}
app.kubernetes.io/name: {{ include "manifest.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/* Service account name */}}
{{- define "manifest.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "manifest.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/* Full image reference (with fallback) */}}
{{- define "manifest.image" -}}
{{- $global := .Values.image }}
{{- $img := .image | default dict }}
{{- $repo := $img.repository | default $global.repository }}
{{- $tag := $img.tag | default $global.tag | default $.Chart.AppVersion }}
{{- printf "%s:%s" $repo $tag }}
{{- end }}

{{- define "manifest.pullPolicy" -}}
{{- $img := .image | default dict }}
{{- $global := .Values.image | default dict }}
{{- $pullPolicy := $img.pullPolicy | default $global.pullPolicy | default "IfNotPresent" }}
{{- $pullPolicy }}
{{- end }}

{{/* Optional defaults */}}
{{- define "manifest.resources" -}}
{{- .resources | default dict | toYaml | nindent 12 -}}
{{- end }}

{{- define "manifest.podAnnotations" -}}
{{- .podAnnotations | default dict | toYaml | nindent 8 -}}
{{- end }}

{{- define "manifest.podLabels" -}}
{{- .podLabels | default dict | toYaml | nindent 8 -}}
{{- end }}

{{- define "manifest.volumes" -}}
{{- .volumes | default list | toYaml | nindent 8 -}}
{{- end }}

{{- define "manifest.volumeMounts" -}}
{{- .volumeMounts | default list | toYaml | nindent 12 -}}
{{- end }}

{{- define "manifest.tolerations" -}}
{{- .tolerations | default list | toYaml | nindent 8 -}}
{{- end }}

{{- define "manifest.affinity" -}}
{{- .affinity | default dict | toYaml | nindent 8 -}}
{{- end }}

{{- define "manifest.nodeSelector" -}}
{{- .nodeSelector | default dict | toYaml | nindent 8 -}}
{{- end }}
