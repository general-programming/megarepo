variable "token" {
  type    = string
  default = "${env("LINODE_TOKEN")}"
}
