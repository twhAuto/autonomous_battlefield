variable "images_directory" { type = string }
variable "image_file" { type = string }
variable "image_sha256" { type = string }
variable "seed_file" { type = string }
variable "seed_sha256" { type = string }
variable "cpus" { type = number }
variable "memory_mib" { type = number }
variable "os_gib" { type = number }
variable "data_gib" { type = number }
variable "network_cidr" { type = string }
variable "guest_ip" { type = string }
variable "mac" { type = string }

locals {
  name = "battlefield-k3s"
}
