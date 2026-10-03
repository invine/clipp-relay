{{- define "clipp.name" -}}
{{- printf "%s-clipp-relay" .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- define "clipp.watchTCP" -}}
{{- if and .Values.transports.tcp.enabled (eq (len .Values.transports.tcp.addresses) 0) }}true{{- else }}false{{- end -}}
{{- end -}}
{{- define "clipp.watchUDP" -}}
{{- if and .Values.transports.udp.enabled (eq (len .Values.transports.udp.addresses) 0) }}true{{- else }}false{{- end -}}
{{- end -}}

{{- define "clipp.nlbServiceName" -}}
{{- $name := printf "%s-%s" (include "clipp.name" .root) .transport -}}
{{- with .root.Values.oci.nlbNameSuffix -}}
  {{- $name = printf "%s-%s" $name . -}}
{{- end -}}
{{- if gt (len $name) 63 -}}
  {{- fail "NLB Service name exceeds 63 characters; shorten release name or oci.nlbNameSuffix" -}}
{{- end -}}
{{- $name -}}
{{- end -}}
