terraform {
  required_providers {
    upcloud = {
      source  = "UpCloudLtd/upcloud"
      version = "~> 2.12.0"
    }
  }
}

provider "upcloud" {
  # Credentials should be stored in the environmental variables
  # export UPCLOUD_USERNAME="Username for UpCloud API user"
  # export UPCLOUD_PASSWORD="Password for UpCloud API user"
  # Optional configuration options
}
