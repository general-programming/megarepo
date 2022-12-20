# Gets a list of all images that support a given Instance shape
data "hcloud_image" "packer_snapshot" {
  with_selector = "app=zuscale"
  most_recent = true
}

data "hcloud_ssh_keys" "all_keys" {
}

variable "zones" {
  default = ["nbg1", "hel1", "fsn1", "ash", "hil"]
}

resource "hcloud_server" "zuscale" {
  count               = var.app-scaling

  image       = data.hcloud_image.packer_snapshot.id
  location = "${var.zones[ count.index % length(var.zones) ]}"
  name        = "hetzner-${var.zones[ count.index % length(var.zones) ]}-${count.index + 1}"
  server_type = "cpx11"

  ssh_keys = data.hcloud_ssh_keys.all_keys.ssh_keys.*.name
  user_data           = base64encode(file("./common/cloud-init-autogen-packer.yml"))

  public_net {
    ipv4_enabled = true
    ipv6_enabled = true
  }
}
