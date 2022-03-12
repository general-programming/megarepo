variable "public_key" {
  type = string
  default = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIMVk9i7FG7dc9r4ixwAJT7uPLH3UuqbwIgeZ7Ytmnpvv"
}

variable "template" {
  type = string
  default = "Ubuntu Server 20.04 LTS (Focal Fossa)"
}

variable "app-scaling" {
  default = 1
}

variable "zone" {
  type = string
  default = "us-sjo1"
}

variable "zones" {
  type = list(string)
  default = ["us-sjo1", "us-nyc1"]
}
