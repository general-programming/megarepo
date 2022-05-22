job "at_warrior" {
    type = "system"
    datacenters = ["dc1"]

    constraint {
        operator = "distinct_hosts"
        value = "true"
    }

    group "warriors" {
        constraint {
            operator = "distinct_hosts"
            value = "true"
        }

        restart {
            attempts = 100
        }

        network {
            port "webui" {
                static = "8001"
            }
        }

        task "warrior" {
            driver = "docker"
            config {
                image = "archiveteam/warrior-dockerfile"
                network_mode = "host"
                volumes = [
                    "warrior-data:/data/data"
                ]
            }

            env {
                DOWNLOADER = "nepeat"
                HTTP_USERNAME = "cum"
                HTTP_PASSWORD = "balls"
                SELECTED_PROJECT = "auto"
            }
        }
    }
}
