{{/*
Expand the name of the chart.
*/}}
{{- define "kubeftpd.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "kubeftpd.fullname" -}}
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
{{- define "kubeftpd.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "kubeftpd.labels" -}}
helm.sh/chart: {{ include "kubeftpd.chart" . }}
{{ include "kubeftpd.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- with .Values.commonLabels }}
{{ toYaml . }}
{{- end }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "kubeftpd.selectorLabels" -}}
app.kubernetes.io/name: {{ include "kubeftpd.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "kubeftpd.serviceAccountName" -}}
{{- if .Values.controller.serviceAccount.create }}
{{- default (include "kubeftpd.fullname" .) .Values.controller.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.controller.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Controller image name
*/}}
{{- define "kubeftpd.controller.image" -}}
{{- $registry := .Values.global.imageRegistry | default .Values.controller.image.registry }}
{{- printf "%s/%s:%s" $registry .Values.controller.image.repository (.Values.controller.image.tag | default .Chart.AppVersion) }}
{{- end }}

{{/*
Common annotations
*/}}
{{- define "kubeftpd.annotations" -}}
{{- with .Values.commonAnnotations }}
{{ toYaml . }}
{{- end }}
{{- end }}

{{/*
Generate certificates for admission webhooks
*/}}
{{- define "kubeftpd.webhook.certs" -}}
{{- $altNames := list ( printf "%s-webhook-service" (include "kubeftpd.fullname" .) ) ( printf "%s-webhook-service.%s" (include "kubeftpd.fullname" .) .Release.Namespace ) ( printf "%s-webhook-service.%s.svc" (include "kubeftpd.fullname" .) .Release.Namespace ) -}}
{{- $ca := genCA "kubeftpd-ca" 365 -}}
{{- $cert := genSignedCert ( include "kubeftpd.fullname" . ) nil $altNames 365 $ca -}}
tls.crt: {{ $cert.Cert | b64enc }}
tls.key: {{ $cert.Key | b64enc }}
ca.crt: {{ $ca.Cert | b64enc }}
{{- end }}