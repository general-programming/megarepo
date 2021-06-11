{{ with secret "pki_internal/issue/consul-fmt2-vault" "common_name=server.fmt2-vault.consul" "ttl=72h" "alt_names=localhost" "ip_sans=127.0.0.1"}}
{{ .Data.private_key }}
{{ end }}
