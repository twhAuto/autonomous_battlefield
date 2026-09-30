output "connection" {
  value = {
    domain_uuid = libvirt_domain.k3s.uuid
    hostname    = libvirt_domain.k3s.name
    address     = var.guest_ip
    user        = "battlefield"
    data_device = "/dev/disk/by-id/virtio-battlefield-k3s-data"
    data_volume = libvirt_volume.data.path
  }
}
