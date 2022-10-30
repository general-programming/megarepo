data_dir = "/opt/nomad/data"
bind_addr = "0.0.0.0"

{% raw %}
advertise = {
  http = "{{ GetPrivateInterfaces | include \"network\" \"10.0.0.0/8\" | attr \"address\" }}"
}
{% endraw %}

tls {
  http = true
  rpc  = true

  ca_file   = "/etc/nomad.d/ca.crt"
  cert_file = "/etc/nomad.d/server.crt"
  key_file  = "/etc/nomad.d/server.key"

  verify_server_hostname = true
  verify_https_client = true
}

plugin "docker" {
  config {
    volumes {
      enabled = true
    }
  }
}
