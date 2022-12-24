{{ with secret "pki_nomad/issue/nomad-cluster" "ttl=24h" }}
{{ .Data.certificate }}
{{ end }}
