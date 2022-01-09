{{ with secret "pki_nomad/issue/nomad-cluster" "common_name=server.global.nomad" "ttl=24h"}}
{{ .Data.issuing_ca }}
{{ end }}
