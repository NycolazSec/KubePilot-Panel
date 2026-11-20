{{- define "kubepilot.fullname" -}}
{{- if contains .Chart.Name .Release.Name -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name .Chart.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}

{{- define "kubepilot.selectorLabels" -}}
app.kubernetes.io/name: {{ .Chart.Name }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "kubepilot.labels" -}}
{{ include "kubepilot.selectorLabels" . }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" }}
{{- end -}}

{{- define "kubepilot.dataClaim" -}}
{{- .Values.persistence.existingClaim | default (printf "%s-data" (include "kubepilot.fullname" .)) -}}
{{- end -}}

{{/* Folder of the game volumes on the node, the only one the panel mounts */}}
{{- define "kubepilot.volumesPath" -}}
{{- if .Values.storage.isolated -}}
{{- printf "%s/%s" (trimSuffix "/" .Values.storage.hostPath) .Values.serversNamespace.name -}}
{{- else -}}
{{- .Values.storage.hostPath -}}
{{- end -}}
{{- end -}}

{{- define "kubepilot.sourceRanges" -}}
{{- $r := .Values.ingress.sourceRanges -}}
{{- if .Values.ingress.cloudflareOnly -}}{{- $r = concat $r .Values.ingress.cloudflareRanges -}}{{- end -}}
{{- toJson $r -}}
{{- end -}}
