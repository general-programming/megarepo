variable "group_uuids" {
  type = object({
    users               = string
    admins              = string
  })
}

variable "domain" {
  type = string
}

variable "slug" {
  type = string
}

variable "cluster" {
  type = string
}
