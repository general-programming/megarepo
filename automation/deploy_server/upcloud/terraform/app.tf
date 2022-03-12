resource "upcloud_server" "zuscale" {
    count    = var.app-scaling
    zone     = var.zone
    hostname = "zuscale${count.index + 1}.upcloud.catgirls.dev"
    plan     = "1xCPU-1GB"
    firewall = false
    metadata = true

    login {
        user = "root"
        keys = [
            var.public_key,
        ]
        create_password   = false
        password_delivery = "none"
    }

    template {
        size    = "25" # GB
        storage = var.template
    }

    network_interface {
        type = "public"
    }

    # cloud-init script
    connection {
        # The server public IP address
        host        = self.network_interface[0].ip_address
        type        = "ssh"
        user        = "root"
        agent       = true
    }

    provisioner "file" {
        when = create
        source      = "common/cloud-init.yml"
        destination = "/etc/cloud/cloud.cfg.d/99.cfg"
    }

    provisioner "remote-exec" {
        when = create
        inline = [
            "hostnamectl set-hostname ${self.hostname}",
            "/usr/bin/cloud-init clean; /usr/bin/cloud-init -d init; /usr/bin/cloud-init -d modules; /usr/bin/cloud-init -d modules --mode final"
        ]
    }
}
