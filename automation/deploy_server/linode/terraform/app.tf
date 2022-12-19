resource "linode_stackscript" "zuscale" {
  label = "zuscale"
  description = "zuscale"
  script = <<EOF
#!/bin/bash
# <UDF name="hostname" label="Hostname" />
# <UDF name="userdata" label="User data" />

hostnamectl set-hostname "$HOSTNAME"
echo $USERDATA | base64 --decode > /etc/cloud/cloud.cfg.d/99.cfg

apt-get update
apt-get -y install cloud-init
cloud-init clean
cloud-init -d init
cloud-init -d modules --mode config
cloud-init -d modules --mode final
EOF
  images = ["linode/ubuntu22.04"]
  rev_note = "initial version"
}

resource "linode_instance" "zuscale" {
    image    = var.image
    count    = var.app-scaling
    label = "linode${count.index + 1}-${var.zones[ count.index % length(var.zones) ]}"
    group = "Terraform"
    region = "${var.zones[ count.index % length(var.zones) ]}"
    type = "g6-nanode-1"
    authorized_keys = [ var.public_key ]
    stackscript_id = linode_stackscript.zuscale.id
    stackscript_data = {
        "hostname" = "linode${count.index + 1}-${var.zones[ count.index % length(var.zones) ]}",
        "userdata" = base64encode(file("./common/cloud-init-autogen.yml"))
    }
}
