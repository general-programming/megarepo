variable "username" {
  type    = string
  default = "${env("UPCLOUD_USERNAME")}"
}

variable "password" {
  type    = string
  default = "${env("UPCLOUD_PASSWORD")}"
}
