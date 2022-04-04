resource "pve_vm" "vm1" {
  name           = "test-vm1-user-data"
  template_name  = "debian-10.11.4-20220312"
  target_node    = "pve"
  target_storage = "local"
  cores          = 1
  memory         = 512
  user_data      = <<-EOF
    #cloud-config
    password: secret0001
    chpasswd:
      expire: false
  EOF
}
