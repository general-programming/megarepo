job "filebeat" {
    type = "system"
    datacenters = ["dc1"]

    // logs are expensive to egress on ec2
    constraint {
        attribute = "${node.class}"
        operator  = "!="
        value = "ec2"
    }

    group "beats_filebeat" {
        restart {
            attempts = 4
            mode = "delay"
        }

        meta {
            deploy_nonce = 1
        }

        task "filebeat" {
            driver = "docker"
            user = "root"

            resources {
                memory = 192
            }

            vault {
                policies = ["access-cluster-secrets"]
            }

            template {
            data = <<EOH
filebeat.inputs:
  - type: docker
    containers.ids: '*'
  - type: log
    paths:
      - /var/log/*.log
      - /var/log/*/*.log
      - /var/log/syslog
      - /var/log/messages
      - /var/log/dmesg

output.redis:
  hosts: ["{{with secret "cluster-secrets/data/filebeat_redis"}}{{.Data.data.host}}{{end}}"]
  password: "{{with secret "cluster-secrets/data/filebeat_redis"}}{{.Data.data.password}}{{end}}"
  key: "filebeat"
  db: 0
  datatype: "channel"

processors:
  - add_cloud_metadata: ~
  - add_docker_metadata: ~
  - add_kubernetes_metadata: ~
            EOH
                destination = "local/filebeat.yml"
                change_mode = "restart"
            }

            config {
                command = "filebeat"
                args = [
                    "-e",
                    "-strict.perms=false"
                ]
                image = "docker.elastic.co/beats/filebeat:7.14.1"
                network_mode = "host"
                volumes = [
                    "local/filebeat.yml:/usr/share/filebeat/filebeat.yml:ro",
                    "/var/lib/filebeat:/usr/share/filebeat/data"
                ]

                mount {
                    type = "bind"
                    target = "/var/lib/docker/containers"
                    source = "/var/lib/docker/containers"
                    readonly = true
                    bind_options {
                        propagation = "rslave"
                    }
                }

                mount {
                    type = "bind"
                    target = "/var/log"
                    source = "/var/log"
                    readonly = true
                    bind_options {
                        propagation = "rprivate"
                    }
                }

                mount {
                    type = "bind"
                    target = "/var/run/docker.sock"
                    source = "/var/run/docker.sock"
                    readonly = true
                    bind_options {
                        propagation = "rprivate"
                    }
                }
            }
        }
    }
}
