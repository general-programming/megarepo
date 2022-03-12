source "scaleway" "test" {
  access_key      = "${var.access_key}"
  secret_key      = "${var.secret_key}"
  image           = "${var.image}"
  zone            = "${var.zone}"
  commercial_type = "${var.instance_type}"
  project_id      = "${var.project}"
  ssh_username    = "root"
}

build {
  sources = ["source.scaleway.test"]

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
