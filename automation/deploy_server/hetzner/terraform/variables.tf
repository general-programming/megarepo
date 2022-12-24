variable "public_key" {
  type = string
  default = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIMVk9i7FG7dc9r4ixwAJT7uPLH3UuqbwIgeZ7Ytmnpvv"
}

variable "app-scaling" {
  default = 32
}

variable "platform_config_instance_shape" {
  default = "VM.Standard.E4.Flex"
}

variable region {
  default = "us-ashburn-1"
}

variable "hcloud_token" {
  # sensitive = true # Requires terraform >= 0.14
}