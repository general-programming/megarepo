source "oracle-oci" "test" {
  availability_domain = "xtuH:US-ASHBURN-AD-1"
  base_image_filter {
    operating_system =  "Canonical Ubuntu"
    operating_system_version = "22.04"
    display_name_search =  "^Canonical-Ubuntu-22.04-([\\.0-9-]+)$"
  }

  image_name      = "zuscale"
  ssh_username    = "ubuntu"
  subnet_ocid     = var.subnet_ocid

  shape = "VM.Standard.E4.Flex"
  shape_config {
    ocpus = 1
  }
}

build {
  sources = ["source.oracle-oci.test"]

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
    execute_command = "chmod +x {{ .Path }}; sudo {{ .Vars }} {{ .Path }}"
    scripts = [
      "common/00-install-packages.sh",
      "common/01-setup-swap.sh",
      "common/10-sysctl.sh",
      "common/99-finish.sh",
    ]
  }
}
