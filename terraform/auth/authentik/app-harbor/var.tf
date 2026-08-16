variable "group_uuids" {
  type = object({
    users  = string
    admins = string
  })
}

variable "domain" {
  type    = string
  default = "https://hub.generalprogramming.org"
}
