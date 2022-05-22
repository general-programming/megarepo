{{ with secret "pki_nomad/issue/nomad-cluster" "common_name=client.global.nomad" "ttl=24h"}}
{{ .Data.issuing_ca }}
{{ end }}
