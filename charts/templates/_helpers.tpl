{{/*
Expand the name of the chart.
*/}}
{{- define "hyperfleet-adapter.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
We truncate at 63 chars because some Kubernetes name fields are limited to this (by the DNS naming spec).
If release name contains chart name it will be used as a full name.
*/}}
{{- define "hyperfleet-adapter.fullname" -}}
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
{{- define "hyperfleet-adapter.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "hyperfleet-adapter.labels" -}}
helm.sh/chart: {{ include "hyperfleet-adapter.chart" . }}
{{ include "hyperfleet-adapter.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "hyperfleet-adapter.selectorLabels" -}}
app.kubernetes.io/name: {{ include "hyperfleet-adapter.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "hyperfleet-adapter.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "hyperfleet-adapter.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Create the name of the adapter+task ConfigMap to use
*/}}
{{- define "hyperfleet-adapter.adapterConfigMapName" -}}
{{- if .Values.adapterConfig.configMapName }}
{{- .Values.adapterConfig.configMapName }}
{{- else }}
{{- printf "%s-config" (include "hyperfleet-adapter.fullname" .) }}
{{- end }}
{{- end }}

{{/*
Create the name of the broker ConfigMap to use
*/}}
{{/*
Determine if broker config is enabled: either broker.create is true,
or a non-blank broker.configMapName is provided.
Fail if neither is set, or if broker.configMapName is defined but blank.
*/}}
{{- define "hyperfleet-adapter.helmValidateBrokerIsConfigured" -}}
{{- if .Values.broker.create -}}
true
{{- else if (hasKey .Values.broker "configMapName") -}}
  {{- if and .Values.broker.configMapName (ne .Values.broker.configMapName "") -}}
true
  {{- else -}}
{{- fail "If .Values.broker.configMapName is specified, it must have a non-blank value." -}}
  {{- end -}}
{{- else -}}
{{- fail "Either .Values.broker.create must be true or a non-blank .Values.broker.configMapName must be provided." -}}
{{- end -}}
{{- end }}



{{- define "hyperfleet-adapter.brokerConfigMapName" -}}
{{- if .Values.broker.configMapName }}
{{- .Values.broker.configMapName }}
{{- else }}
{{- printf "%s-broker" (include "hyperfleet-adapter.fullname" .) }}
{{- end }}
{{- end }}

{{- define "hyperfleet-adapter.helmValidateAdapterIsConfigured" -}}
{{- if .Values.adapterConfig.create -}}
  {{- $hasYaml := not (empty .Values.adapterConfig.yaml) -}}
  {{- $hasFiles := not (empty .Values.adapterConfig.files) -}}
  {{- if or $hasYaml $hasFiles -}}
true
  {{- else -}}
{{- fail "When .Values.adapterConfig.create is true, either .Values.adapterConfig.yaml or .Values.adapterConfig.files must be provided." -}}
  {{- end -}}
{{- else if (hasKey .Values.adapterConfig "configMapName") -}}
  {{- if and .Values.adapterConfig.configMapName (ne .Values.adapterConfig.configMapName "") -}}
true
  {{- else -}}
{{- fail "If .Values.adapterConfig.configMapName is specified, it must have a non-blank value." -}}
  {{- end -}}
{{- else -}}
{{- fail "Either .Values.adapterConfig.create must be true or a non-blank .Values.adapterConfig.configMapName must be provided." -}}
{{- end -}}
{{- end }}

{{/*
Render a probe with defaults for httpGet.
*/}}
{{- define "hyperfleet-adapter.renderProbe" -}}
{{- $probe := .probe -}}
{{- $defaultPath := .defaultPath -}}
{{- $defaultPort := .defaultPort | default "http" -}}
{{- if $probe.httpGet }}
httpGet:
  path: {{ $probe.httpGet.path | default $defaultPath }}
  port: {{ $probe.httpGet.port | default $defaultPort }}
  {{- if $probe.httpGet.scheme }}
  scheme: {{ $probe.httpGet.scheme }}
  {{- end }}
{{- else if $probe.tcpSocket }}
tcpSocket:
  port: {{ $probe.tcpSocket.port }}
{{- else if $probe.exec }}
exec:
  command:
    {{- toYaml $probe.exec.command | nindent 4 }}
{{- else }}
httpGet:
  path: {{ $defaultPath }}
  port: {{ $defaultPort }}
{{- end }}
{{- with $probe.initialDelaySeconds }}
initialDelaySeconds: {{ . }}
{{- end }}
{{- with $probe.periodSeconds }}
periodSeconds: {{ . }}
{{- end }}
{{- with $probe.timeoutSeconds }}
timeoutSeconds: {{ . }}
{{- end }}
{{- with $probe.failureThreshold }}
failureThreshold: {{ . }}
{{- end }}
{{- with $probe.successThreshold }}
successThreshold: {{ . }}
{{- end }}
{{- end }}

{{/*
Get the apiGroup for a Kubernetes resource.
Maps resource names to their correct API group for RBAC rules.
Returns empty string for core API resources, otherwise the appropriate apiGroup.
*/}}
{{- define "hyperfleet-adapter.apiGroup" -}}
{{- $resource := . -}}
{{- $appsResources := list "deployments" "statefulsets" "daemonsets" "replicasets" -}}
{{- $batchResources := list "jobs" "cronjobs" "jobs/status" -}}
{{- $rbacResources := list "roles" "rolebindings" "clusterroles" "clusterrolebindings" -}}
{{- if has $resource $appsResources -}}
apps
{{- else if has $resource $batchResources -}}
batch
{{- else if has $resource $rbacResources -}}
rbac.authorization.k8s.io
{{- end -}}
{{- end }}

{{/*
Determine the broker type.
Returns broker.type if explicitly set, otherwise infers from broker config objects.
*/}}
{{- define "hyperfleet-adapter.brokerType" -}}
{{- if .Values.broker.type -}}
{{- .Values.broker.type -}}
{{- else if .Values.broker.googlepubsub -}}
googlepubsub
{{- else if .Values.broker.rabbitmq -}}
rabbitmq
{{- end -}}
{{- end }}
