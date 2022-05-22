job "at_liveleak" {
    type = "system"
    datacenters = ["dc1"]

    group "liveleak" {
        restart {
            mode = "delay"
        }

        meta {
            deploy_nonce = 4
        }

        task "warrior-liveleak" {
            driver = "docker"
            kill_signal = "SIGINT"
            kill_timeout = "180s"

            resources {
                memory = 128
            }

            config {
                args = [
                    "--concurrent",
                    "1",
                    "nepeat"
                ]
                image = "atdr.meo.ws/archiveteam/liveleak-grab"
                force_pull = true
                // worst case scenario, 256 gives enough room for disaster
                memory_hard_limit = 256
            }
        }
    }
}
