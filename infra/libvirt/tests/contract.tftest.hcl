# Mock only the provider transport: evaluate the actual module without libvirt.
mock_provider "libvirt" {}

variables {
  images_directory = "/srv/test-data/libvirt/images"
  image_file       = "/tmp/verified-image.qcow2"
  image_sha256     = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
  seed_file        = "/tmp/public-cloud-init.iso"
  seed_sha256      = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
  cpus             = 2
  memory_mib       = 2048
  os_gib           = 12
  data_gib         = 4
  network_cidr     = "192.168.235.0/24"
  guest_ip         = "192.168.235.10"
  mac              = "52:54:00:bf:23:50"
}

run "guest_contract" {
  command = plan

  assert {
    condition     = libvirt_domain.k3s.devices.channels[0].source.unix.mode == "bind"
    error_message = "QEMU guest-agent transport must be a UNIX socket, never a default PTY."
  }
  assert {
    condition     = libvirt_domain.k3s.devices.channels[0].target.virt_io.name == "org.qemu.guest_agent.0"
    error_message = "Guest-agent channel name must match the service."
  }
  assert {
    condition     = libvirt_domain.k3s.devices.disks[1].serial == "battlefield-k3s-data" && libvirt_domain.k3s.devices.disks[2].read_only
    error_message = "Guest data needs a stable serial and the seed must be read-only."
  }
  assert {
    condition     = libvirt_domain.k3s.autostart && libvirt_network.k3s.autostart && libvirt_pool.k3s.create.autostart
    error_message = "VM, network and pool must survive host reboot."
  }
  assert {
    condition     = !libvirt_pool.k3s.create.build && !libvirt_pool.k3s.destroy.delete
    error_message = "Provisioning must never build or delete the pre-existing data directory."
  }
}

run "connection_identity" {
  command = apply

  assert {
    condition     = output.connection.domain_uuid == libvirt_domain.k3s.uuid
    error_message = "Ansible must receive the domain UUID, not its numeric runtime ID."
  }
}
