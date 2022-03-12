# Gets a list of all images that support a given Instance shape
data "oci_core_images" "supported_platform_config_shape_images" {
  compartment_id   = var.tenancy_ocid
  shape            = var.platform_config_instance_shape
  operating_system = "Canonical Ubuntu"
  filter {
    name   = "display_name"
    values = ["^Canonical-Ubuntu-20.04-([\\.0-9-]+)$"]
    regex  = true
  }
}

resource "oci_identity_compartment" "tf-compartment" {
    # Required
    compartment_id = "${var.tenancy_ocid}"
    description = "Compartment for Terraform resources."
    name = "zuscale-terraform"
}

data "oci_identity_availability_domain" "ad1" {
  compartment_id = "${var.tenancy_ocid}"
  ad_number      = 1
}

data "oci_identity_availability_domain" "ad2" {
  compartment_id = "${var.tenancy_ocid}"
  ad_number      = 2
}

data "oci_identity_availability_domain" "ad3" {
  compartment_id = "${var.tenancy_ocid}"
  ad_number      = 3
}

variable "zones" {
  default = ["xtuH:US-ASHBURN-AD-1", "xtuH:US-ASHBURN-AD-2", "xtuH:US-ASHBURN-AD-3"]
}

resource "oci_core_instance" "zuscale" {
  count               = var.app-scaling
  availability_domain = "${var.zones[ count.index % length(var.zones) ]}"
  compartment_id      = oci_identity_compartment.tf-compartment.id
  display_name        = "oracle-${count.index + 1}"
  shape               = var.platform_config_instance_shape

  shape_config {
    memory_in_gbs = "2"
    ocpus = "1"
  }

  source_details {
    source_type = "image"
    source_id   = data.oci_core_images.supported_platform_config_shape_images.images[0]["id"]
  }

  create_vnic_details {
      assign_public_ip = true
      subnet_id = "ocid1.subnet.oc1.iad.aaaaaaaatfygybwsegaeahuunfjvk4q2pwxubpfir4kwo62qzuyxu2lwglwq"
  }

  metadata = {
    ssh_authorized_keys = var.public_key
    user_data           = base64encode(file("./userdata/cloud-init-autogen.yml"))
  }

  preemptible_instance_config {
      preemption_action {
          type = "TERMINATE"
          preserve_boot_volume = false
      }
  }
}
