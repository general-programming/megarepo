variable "public_key" {
  type = string
  default = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIMVk9i7FG7dc9r4ixwAJT7uPLH3UuqbwIgeZ7Ytmnpvv erin"
}

variable "image" {
  type = string
  default = "linode/ubuntu22.04"
}

variable "app-scaling" {
  default = 16
}

variable "zones" {
  type = list(string)
  default = ["us-west", "us-east", "us-central"]
}
