variable "access_key" {
  type    = string
  default = "${env("SCALEWAY_ACCESS_KEY")}"
}

variable "secret_key" {
  type    = string
  default = "${env("SCALEWAY_SECRET_KEY")}"
}

variable "project" {
  type    = string
  default = "${env("SCALEWAY_PROJECT")}"
}

variable "zone" {
  type    = string
  default = "fr-par-1"
}

variable "image" {
  type    = string
  default = "ubuntu_focal"
}

variable "instance_type" {
  type    = string
  default = "DEV1-S"
}
