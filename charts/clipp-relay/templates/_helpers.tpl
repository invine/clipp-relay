{{- define "clipp.name" -}}
{{- printf "%s-clipp-relay" .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- define "clipp.watchTCP" -}}
{{- if and .Values.transports.tcp.enabled (eq (len .Values.transports.tcp.addresses) 0) }}true{{- else }}false{{- end -}}
{{- end -}}
{{- define "clipp.watchUDP" -}}
{{- if and .Values.transports.udp.enabled (eq (len .Values.transports.udp.addresses) 0) }}true{{- else }}false{{- end -}}
{{- end -}}
