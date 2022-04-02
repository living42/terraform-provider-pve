resource "pve_vm" "vm1" {
  name           = "vm1"
  template_name  = "debian-10.11.4-20220312"
  target_node    = "pve"
  target_storage = "local"
  cores          = 1
  memory         = 512
}
