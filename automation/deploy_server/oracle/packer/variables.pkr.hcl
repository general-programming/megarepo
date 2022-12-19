variable "tenancy_ocid" {
  type    = string
  default = "${env("OCI_TENANCY")}"
}

variable "subnet_ocid" {
  type    = string
  default = "${env("OCI_SUBNET")}"
}

variable "user_ocid" {
  type    = string
  default = "${env("OCI_USER")}"
}

variable "fingerprint" {
  type    = string
  default = "${env("OCI_FINGERPRINT")}"
}

variable "private_key_path" {
  type    = string
  default = "${env("OCI_PRIVKEY_PATH")}"
}

variable "public_key_path" {
  type    = string
  default = "${env("OCI_PUBKEY_PATH")}"
}
