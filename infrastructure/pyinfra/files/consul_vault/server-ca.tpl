{{ with secret "pki_internal/issue/consul-fmt2-vault" "common_name=server.fmt2-vault.consul" "ttl=72h"}}
{{ .Data.issuing_ca }}
{{ end }}
