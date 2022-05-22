job "promtail" {
    type = "system"
    datacenters = ["dc1"]

    group "promtail" {
        restart {
            attempts = 4
            mode = "delay"
        }

        meta {
            deploy_nonce = 1
        }

        task "promtail" {
            driver = "docker"
            user = "root"

            resources {
                memory = 128
            }

            template {
            data = <<EOH
server:
  disable: true

positions:
  filename: /var/lib/promtail/positions.yml

clients:
  - url: http://loki.service.fmt2.consul:3100/loki/api/v1/push

scrape_configs:
  - job_name: system
    pipeline_stages:
      - docker: {}
    static_configs:
      - labels:
          job: docker
          __path__: /var/lib/docker/containers/*/*-json.log
            EOH
                destination = "local/promtail.yml"
                change_mode = "restart"
            }

            config {
                args = [
                    "-config.file=/config.yml"
                ]
                image = "grafana/promtail"
                network_mode = "host"
                volumes = [
                    "local/promtail.yml:/config.yml:ro",
                    "/var/lib/promtail:/var/lib/promtail"
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
