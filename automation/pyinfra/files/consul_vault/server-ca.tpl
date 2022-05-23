{{ with secret "pki_internal/issue/consul-fmt2-vault" "common_name=server.fmt2-vault.consul" "ttl=168h"}}
{{ .Data.issuing_ca }}
{{ end }}
