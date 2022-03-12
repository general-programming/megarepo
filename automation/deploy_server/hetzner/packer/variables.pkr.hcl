variable "token" {
  type    = string
  default = "${env("HETZNER_TOKEN")}"
}
