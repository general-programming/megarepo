source "hcloud" "test" {
  token        = "${var.token}"
  image        = "ubuntu-20.04"
  location     = "ash"
  server_type  = "cpx11"
  ssh_username = "root"
}

build {
  sources = ["source.hcloud.test"]

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
