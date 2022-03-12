source "upcloud" "test" {
  username        = "${var.username}"
  password        = "${var.password}"
  zone            = "us-sjo1"
  storage_name    = "ubuntu server 20.04"
  template_prefix = "ubuntu-server"
}

build {
  sources = ["source.upcloud.test"]

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
