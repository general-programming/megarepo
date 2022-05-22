job "at_reddit" {
    type = "system"
    datacenters = ["dc1"]

    group "reddit" {
        restart {
            mode = "delay"
        }

        meta {
            deploy_nonce = 1
        }

        task "warrior-reddit" {
            driver = "docker"
            resources {
                memory = 384
            }

            config {
                args = [
                    "--concurrent",
                    "15",
                    "nepeat"
                ]
                labels = {
                    com.centurylinklabs.watchtower.enable = "true"
                }
                image = "atdr.meo.ws/archiveteam/reddit-grab"
                force_pull = true
                // worst case scenario, 512 gives enough room for disaster
                memory_hard_limit = 512
            }
        }
    }
}
