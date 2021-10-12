data_dir = "/var/lib/consul"
log_level = "INFO"
server = false
ui = false
enable_local_script_checks = true
datacenter = "fmt2"
retry_join = ["consul.service.fmt2.consul"]
bind_addr = "{{ GetPrivateInterfaces | include \"network\" \"10.0.0.0/8\" | attr \"address\" }}"
