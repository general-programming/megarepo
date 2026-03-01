variable "group_uuids" {
  type = object({
    users               = string
    admins              = string
  })
}
