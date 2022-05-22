job "at_chrome_store" {
    type = "system"
    datacenters = ["dc1"]

    group "answers" {
        restart {
            attempts = 4
        }

        meta {
            deploy_nonce = 1
        }

        task "chromestorewarrior" {
            driver = "docker"
            resources {
                // yahoo answers workers use on average 32-80 from observation.
                memory = 128
                memory_max = 384
            }

            config {
                args = [
                    "--concurrent",
                    "1",
                    "nepeat"
                ]
                image = "atdr.meo.ws/archiveteam/chrome-web-store-grab"
                force_pull = true
            }
        }
    }
}
