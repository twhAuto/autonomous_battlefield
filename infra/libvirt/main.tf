# The wrapper verifies the AB-134 mount contract before every plan/apply.
resource "libvirt_pool" "k3s" {
  name    = local.name
  type    = "dir"
  target  = { path = var.images_directory }
  create  = { build = false, start = true, autostart = true }
  destroy = { delete = false }
  lifecycle { prevent_destroy = true }
}

resource "libvirt_network" "k3s" {
  name      = local.name
  autostart = true
  bridge    = { name = "bf-k3s0" }
  forward   = { mode = "nat" }
  ips = [{
    address = cidrhost(var.network_cidr, 1)
    netmask = cidrnetmask(var.network_cidr)
    dhcp = {
      hosts  = [{ mac = var.mac, ip = var.guest_ip, name = local.name }]
      ranges = [{ start = cidrhost(var.network_cidr, 10), end = cidrhost(var.network_cidr, 200) }]
    }
  }]
  lifecycle { prevent_destroy = true }
}

resource "libvirt_volume" "base" {
  name   = "ubuntu-${substr(var.image_sha256, 0, 16)}.qcow2"
  pool   = libvirt_pool.k3s.name
  target = { format = { type = "qcow2" } }
  create = { content = { url = var.image_file } }
  lifecycle { prevent_destroy = true }
}

resource "libvirt_volume" "os" {
  name       = "battlefield-k3s-os.qcow2"
  pool       = libvirt_pool.k3s.name
  capacity   = var.os_gib * 1024 * 1024 * 1024
  allocation = 0
  target     = { format = { type = "qcow2" } }
  backing_store = {
    path   = libvirt_volume.base.path
    format = { type = "qcow2" }
  }
}

resource "libvirt_volume" "data" {
  name       = "battlefield-k3s-data.qcow2"
  pool       = libvirt_pool.k3s.name
  capacity   = var.data_gib * 1024 * 1024 * 1024
  allocation = 0
  target     = { format = { type = "qcow2" } }
  # Retention is separate from OS/domain replacement. The wrapper also rejects
  # data deletes and recreation after a missing-volume refresh.
  lifecycle { prevent_destroy = true }
}

resource "libvirt_volume" "seed" {
  name = "battlefield-k3s-seed-${substr(var.seed_sha256, 0, 16)}.iso"
  pool = libvirt_pool.k3s.name
  # Libvirt probes the uploaded ISO. Do not force raw and then request an
  # unsupported format update when refresh identifies it as iso.
  create = { content = { url = var.seed_file } }
}

resource "libvirt_domain" "k3s" {
  name        = local.name
  description = "autonomous-battlefield AB-126 managed K3s guest"
  type        = "kvm"
  memory      = var.memory_mib
  memory_unit = "MiB"
  vcpu        = var.cpus
  running     = true
  autostart   = true
  cpu         = { mode = "host-passthrough" }
  os          = { type = "hvm", type_arch = "x86_64", type_machine = "q35", boot_devices = [{ dev = "hd" }] }
  features    = { acpi = true, apic = {} }
  on_reboot   = "restart"
  devices = {
    disks = [
      {
        device = "disk"
        driver = { name = "qemu", type = "qcow2" }
        source = { file = { file = libvirt_volume.os.path } }
        target = { dev = "vda", bus = "virtio" }
      },
      {
        device = "disk"
        driver = { name = "qemu", type = "qcow2" }
        source = { file = { file = libvirt_volume.data.path } }
        target = { dev = "vdb", bus = "virtio" }
        serial = "battlefield-k3s-data"
      },
      {
        device    = "cdrom"
        driver    = { name = "qemu", type = "raw" }
        source    = { file = { file = libvirt_volume.seed.path } }
        target    = { dev = "sda", bus = "sata" }
        read_only = true
      }
    ]
    interfaces = [{
      mac    = { address = var.mac }
      source = { network = { network = libvirt_network.k3s.name } }
      model  = { type = "virtio" }
    }]
    channels = [{
      source = { unix = { mode = "bind" } }
      target = { virt_io = { name = "org.qemu.guest_agent.0" } }
    }]
    consoles = [{ target = { type = "serial", port = 0 } }]
  }
  lifecycle { replace_triggered_by = [libvirt_volume.os, libvirt_volume.seed] }
}
