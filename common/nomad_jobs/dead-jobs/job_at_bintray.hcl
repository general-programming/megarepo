job "at_bintray" {
    type = "system"
    datacenters = ["dc1"]

    group "bintray" {
        restart {
            mode = "delay"
        }

        meta {
            deploy_nonce = 1
        }

        task "warrior-bintray" {
            driver = "docker"
            resources {
                memory = 128
            }

            config {
                args = [
                    "--concurrent",
                    "1",
                    "nepeat"
                ]
                labels = {
                    "com.centurylinklabs.watchtower.enable" = "true"
                }
                image = "atdr.meo.ws/archiveteam/bintray-grab"
                force_pull = true
                // worst case scenario, 256 gives enough room for disaster
                memory_hard_limit = 256
            }
        }
    }
}
