{{/*
Expand the name of the chart.
*/}}
{{- define "jmap2nats.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Fully qualified app name (release name + chart name unless overridden).
*/}}
{{- define "jmap2nats.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- $name := default .Chart.Name .Values.nameOverride -}}
{{- if contains $name .Release.Name -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{/*
Chart name and version, used as the helm.sh/chart label.
*/}}
{{- define "jmap2nats.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Selector labels (stable across upgrades — used in Deployment selector & Pod labels).
*/}}
{{- define "jmap2nats.selectorLabels" -}}
app.kubernetes.io/name: {{ include "jmap2nats.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{/*
Common labels.
*/}}
{{- define "jmap2nats.labels" -}}
helm.sh/chart: {{ include "jmap2nats.chart" . }}
{{ include "jmap2nats.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{/*
ServiceAccount name.
*/}}
{{- define "jmap2nats.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
{{- default (include "jmap2nats.fullname" .) .Values.serviceAccount.name -}}
{{- else -}}
{{- default "default" .Values.serviceAccount.name -}}
{{- end -}}
{{- end -}}

{{/*
Container image reference. Tag defaults to "v" + .Chart.AppVersion.
Explicit image.tag overrides pass through.
*/}}
{{- define "jmap2nats.image" -}}
{{- $tag := default (printf "v%s" .Chart.AppVersion) .Values.image.tag -}}
{{- printf "%s:%s" .Values.image.repository $tag -}}
{{- end -}}

{{/*
Rendered config.json content. Merges the chart-owned token_file path into the
user's .Values.config block. When nack.enabled is true (and the user hasn't
overridden it), also injects stream.externally_managed: true so the binary
won't fight NACK over the stream's spec on startup.
*/}}
{{- define "jmap2nats.configJson" -}}
{{- $cfg := deepCopy .Values.config -}}
{{- $_ := set $cfg.jmap "token_file" "/etc/jmap2nats/secrets/token" -}}
{{- if .Values.auth.nats.tokenKey -}}
{{- $_ := set $cfg.nats "token_file" "/etc/jmap2nats/secrets/nats.token" -}}
{{- end -}}
{{- if .Values.auth.nats.passwordKey -}}
{{- $_ := set $cfg.nats "password_file" "/etc/jmap2nats/secrets/nats.password" -}}
{{- end -}}
{{- if .Values.auth.nats.credsKey -}}
{{- $_ := set $cfg.nats "creds_file" "/etc/jmap2nats/secrets/nats.creds" -}}
{{- end -}}
{{- if .Values.auth.nats.secretKey -}}
{{- $_ := set $cfg.nats "nkey_seed_file" "/etc/jmap2nats/secrets/nats.nk" -}}
{{- end -}}
{{- if and .Values.nack.enabled (not (hasKey $cfg.stream "externally_managed")) -}}
{{- $_ := set $cfg.stream "externally_managed" true -}}
{{- end -}}
{{- $cfg | toJson -}}
{{- end -}}

{{/*
Parse a byte-size string into int64 bytes. Accepts the same suffixes as
jmap2nats's Bytes type: B, KiB/MiB/GiB (binary), KB/MB/GB (decimal). A bare
integer (string or number) passes through.
*/}}
{{- define "jmap2nats.parseBytes" -}}
{{- $s := . | toString -}}
{{- if hasSuffix "GiB" $s -}}
{{- mul (trimSuffix "GiB" $s | int64) 1073741824 -}}
{{- else if hasSuffix "MiB" $s -}}
{{- mul (trimSuffix "MiB" $s | int64) 1048576 -}}
{{- else if hasSuffix "KiB" $s -}}
{{- mul (trimSuffix "KiB" $s | int64) 1024 -}}
{{- else if hasSuffix "GB" $s -}}
{{- mul (trimSuffix "GB" $s | int64) 1000000000 -}}
{{- else if hasSuffix "MB" $s -}}
{{- mul (trimSuffix "MB" $s | int64) 1000000 -}}
{{- else if hasSuffix "KB" $s -}}
{{- mul (trimSuffix "KB" $s | int64) 1000 -}}
{{- else if hasSuffix "B" $s -}}
{{- trimSuffix "B" $s | int64 -}}
{{- else -}}
{{- $s | int64 -}}
{{- end -}}
{{- end -}}
