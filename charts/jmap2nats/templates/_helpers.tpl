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
Container image reference. Tag defaults to .Chart.AppVersion.
Explicit image.tag overrides pass through.
*/}}
{{- define "jmap2nats.image" -}}
{{- $tag := default .Chart.AppVersion .Values.image.tag -}}
{{- printf "%s:%s" .Values.image.repository $tag -}}
{{- end -}}

{{/*
The Secret name backing the JMAP token, also the default Secret for the NATS
auth methods. Falls back to the chart fullname when secrets.jmap.secret.name is
left empty, so the chart renders without overrides.
*/}}
{{- define "jmap2nats.jmapSecretName" -}}
{{- .Values.secrets.jmap.secret.name | default (printf "%s-secrets" (include "jmap2nats.fullname" .)) -}}
{{- end -}}

{{/*
The app's NATS auth method: token | user | creds | nkey | none. Determined by
which secrets.nats method block is populated (the connection is anonymous when
none is).
*/}}
{{- define "jmap2nats.natsMethod" -}}
{{- $n := .Values.secrets.nats -}}
{{- if $n.token.token -}}token
{{- else if or $n.user.user $n.user.password -}}user
{{- else if $n.creds.file -}}creds
{{- else if $n.nkey.seed -}}nkey
{{- else -}}none
{{- end -}}
{{- end -}}

{{/*
Count of populated secrets.nats method blocks (for validation).
*/}}
{{- define "jmap2nats.natsMethodCount" -}}
{{- $n := .Values.secrets.nats -}}
{{- $c := 0 -}}
{{- if $n.token.token -}}{{- $c = add1 $c -}}{{- end -}}
{{- if or $n.user.user $n.user.password -}}{{- $c = add1 $c -}}{{- end -}}
{{- if $n.creds.file -}}{{- $c = add1 $c -}}{{- end -}}
{{- if $n.nkey.seed -}}{{- $c = add1 $c -}}{{- end -}}
{{- $c -}}
{{- end -}}

{{/*
Count of populated secrets.nack method blocks (for validation / mirror logic).
*/}}
{{- define "jmap2nats.nackMethodCount" -}}
{{- $n := .Values.secrets.nack -}}
{{- $c := 0 -}}
{{- if $n.token.token -}}{{- $c = add1 $c -}}{{- end -}}
{{- if or $n.user.user $n.user.password -}}{{- $c = add1 $c -}}{{- end -}}
{{- if $n.creds.file -}}{{- $c = add1 $c -}}{{- end -}}
{{- if $n.nkey.seed -}}{{- $c = add1 $c -}}{{- end -}}
{{- $c -}}
{{- end -}}

{{/*
The effective Secret name backing the app's NATS auth (the active method's
secret.name, defaulting to the JMAP token Secret).
*/}}
{{- define "jmap2nats.natsSecretName" -}}
{{- $method := include "jmap2nats.natsMethod" . | trim -}}
{{- if ne $method "none" -}}
{{- $b := index .Values.secrets.nats $method -}}
{{- $b.secret.name | default (include "jmap2nats.jmapSecretName" .) -}}
{{- end -}}
{{- end -}}

{{/*
The NACK Account CRD auth method: a populated secrets.nack block, otherwise it
mirrors the app's NATS method.
*/}}
{{- define "jmap2nats.accountMethod" -}}
{{- $a := .Values.secrets.nack -}}
{{- if $a.token.token -}}token
{{- else if or $a.user.user $a.user.password -}}user
{{- else if $a.creds.file -}}creds
{{- else if $a.nkey.seed -}}nkey
{{- else -}}{{- include "jmap2nats.natsMethod" . | trim -}}
{{- end -}}
{{- end -}}

{{/*
The Account CRD auth block (spec.token / spec.user / spec.creds / spec.nkey).
Uses secrets.nack when populated, otherwise mirrors secrets.nats verbatim.
Emits nothing for an anonymous (none) method. Maps value field names to the
CRD's field names (creds.file, nkey.seed, ...).
*/}}
{{- define "jmap2nats.accountUser" -}}
{{- $jmapName := include "jmap2nats.jmapSecretName" . -}}
{{- $method := include "jmap2nats.accountMethod" . | trim -}}
{{- $nackCount := include "jmap2nats.nackMethodCount" . | int -}}
{{- if ne $method "none" -}}
{{- $b := dict -}}
{{- $name := "" -}}
{{- if gt $nackCount 0 -}}
{{- $b = index .Values.secrets.nack $method -}}
{{- $name = $b.secret.name | default (index .Values.secrets.nats $method).secret.name | default $jmapName -}}
{{- else -}}
{{- $b = index .Values.secrets.nats $method -}}
{{- $name = $b.secret.name | default $jmapName -}}
{{- end -}}
{{- if eq $method "token" -}}
token:
  secret:
    name: {{ $name | quote }}
  token: {{ $b.token | quote }}
{{- else if eq $method "user" -}}
user:
  secret:
    name: {{ $name | quote }}
  user: {{ $b.user | quote }}
  password: {{ $b.password | quote }}
{{- else if eq $method "creds" -}}
creds:
  secret:
    name: {{ $name | quote }}
  file: {{ $b.file | quote }}
{{- else if eq $method "nkey" -}}
nkey:
  secret:
    name: {{ $name | quote }}
  seed: {{ $b.seed | quote }}
{{- end -}}
{{- end -}}
{{- end -}}

{{/*
Fail-fast validation of the secrets / nack configuration. Included from
configmap.yaml so any render path triggers it.
*/}}
{{- define "jmap2nats.validate" -}}
{{- if not .Values.secrets.jmap.token -}}
{{- fail "secrets.jmap.token is required (the key in the Secret holding the bearer token)" -}}
{{- end -}}
{{- if gt (include "jmap2nats.natsMethodCount" . | int) 1 -}}
{{- fail "secrets.nats: set at most one auth method (token, user, creds, or nkey)" -}}
{{- end -}}
{{- $u := .Values.secrets.nats.user -}}
{{- if and (or $u.user $u.password) (not (and $u.user $u.password)) -}}
{{- fail "secrets.nats.user: set both the user and password keys" -}}
{{- end -}}
{{- $nackCount := include "jmap2nats.nackMethodCount" . | int -}}
{{- if gt $nackCount 1 -}}
{{- fail "secrets.nack: set at most one auth method (token, user, creds, or nkey)" -}}
{{- end -}}
{{- $nu := .Values.secrets.nack.user -}}
{{- if and (or $nu.user $nu.password) (not (and $nu.user $nu.password)) -}}
{{- fail "secrets.nack.user: set both the user and password keys" -}}
{{- end -}}
{{- if and .Values.nack.account.name (not .Values.nack.enabled) -}}
{{- fail "nack.account.name is set but nack.enabled is false; enable NACK or clear the account name" -}}
{{- end -}}
{{- if and (gt $nackCount 0) (not .Values.nack.account.name) -}}
{{- fail "secrets.nack is populated but nack.account.name is empty; set nack.account.name or clear secrets.nack" -}}
{{- end -}}
{{- if .Values.networkPolicy.egress.enabled -}}
{{- $extra := .Values.networkPolicy.egress.extraRules -}}
{{- if not (or .Values.networkPolicy.egress.nats.to $extra) -}}
{{- fail "networkPolicy.egress.enabled needs peers reaching config.nats.url; set networkPolicy.egress.nats.to" -}}
{{- end -}}
{{- if not (or .Values.networkPolicy.egress.jmap.to $extra) -}}
{{- fail "networkPolicy.egress.enabled needs peers reaching the JMAP server; set networkPolicy.egress.jmap.to" -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{/*
Rendered config.json content. Merges the chart-owned auth file paths into the
user's .Values.config block based on the selected secrets.nats method. When
nack.enabled is true (and the user hasn't overridden it), also injects
stream.externally_managed: true so the binary won't fight NACK over the
stream's spec on startup.
*/}}
{{- define "jmap2nats.configJson" -}}
{{- $cfg := deepCopy .Values.config -}}
{{- $_ := set $cfg.jmap "token_file" "/etc/jmap2nats/secrets/token" -}}
{{- $method := include "jmap2nats.natsMethod" . | trim -}}
{{- if eq $method "token" -}}
{{- $_ := set $cfg.nats "token_file" "/etc/jmap2nats/secrets/nats.token" -}}
{{- else if eq $method "user" -}}
{{- $_ := set $cfg.nats "user_file" "/etc/jmap2nats/secrets/nats.user" -}}
{{- $_ := set $cfg.nats "password_file" "/etc/jmap2nats/secrets/nats.password" -}}
{{- else if eq $method "creds" -}}
{{- $_ := set $cfg.nats "creds_file" "/etc/jmap2nats/secrets/nats.creds" -}}
{{- else if eq $method "nkey" -}}
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
