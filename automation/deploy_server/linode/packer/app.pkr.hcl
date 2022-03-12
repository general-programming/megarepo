locals { timestamp = regex_replace(timestamp(), "[- TZ:]", "") }

source "linode" "image" {
  image             = "linode/ubuntu20.04"
  image_description = "zuscale image"
  image_label       = "zuscale-image-${local.timestamp}"
  instance_label    = "temporary-linode-${local.timestamp}"
  instance_type     = "g6-nanode-1"
  linode_token      = "${var.token}"
  region            = "us-west"
  ssh_username      = "root"
}

build {
  sources = ["source.linode.image"]

  provisioner "file" {
    source      = "common/authorized_keys"
    destination = "/tmp/authorized_keys"
  }

  provisioner "shell" {
    inline = [
      "cat /tmp/authorized_keys | sudo tee -a /root/.ssh/authorized_keys",
    ]
  }

  provisioner "shell" {
    scripts = [
      "common/00-install-packages.sh",
      "common/01-setup-swap.sh",
      "common/10-sysctl.sh",
      "common/99-finish.sh",
    ]
  }
}
