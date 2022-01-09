job "at_halo" {
    type = "system"
    datacenters = ["dc1"]

    constraint {
        operator = "distinct_hosts"
        value = "true"
    }

    group "halos" {
        restart {
            attempts = 10
        }

        task "halo" {
            driver = "docker"
            resources {
                memory = 64
            }
            config {
                args = [
                    "--concurrent",
                    "20",
                    "bigbiglarge_com"
                ]
                image = "atdr.meo.ws/archiveteam/halo-new-grab:latest"
            }
        }

        task "halo2" {
            driver = "docker"
            resources {
                memory = 64
            }
            config {
                args = [
                    "--concurrent",
                    "20",
                    "bigbiglarge_com"
                ]
                image = "atdr.meo.ws/archiveteam/halo-new-grab:latest"
            }
        }

        task "halo3" {
            driver = "docker"
            resources {
                memory = 64
            }
            config {
                args = [
                    "--concurrent",
                    "20",
                    "bigbiglarge_com"
                ]
                image = "atdr.meo.ws/archiveteam/halo-new-grab:latest"
            }
        }

        task "halo4" {
            driver = "docker"
            resources {
                memory = 64
            }
            config {
                args = [
                    "--concurrent",
                    "20",
                    "bigbiglarge_com"
                ]
                image = "atdr.meo.ws/archiveteam/halo-new-grab:latest"
            }
        }
    }
}
