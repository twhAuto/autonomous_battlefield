terraform {
  required_version = "= 1.14.7"
  required_providers {
    libvirt = {
      source  = "dmacvicar/libvirt"
      version = "= 0.9.9"
    }
  }
  backend "local" {}
}
provider "libvirt" {
  uri = "qemu:///system"
}
