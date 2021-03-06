package provider

import (
	"os"
	"strings"
	"testing"

	pxapi "github.com/Telmate/proxmox-api-go/proxmox"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/resource"
)

func TestAccResourceVMCPUMemory(t *testing.T) {
	resource.UnitTest(t, resource.TestCase{
		PreCheck:          func() { testAccPreCheck(t) },
		ProviderFactories: providerFactories,
		Steps: []resource.TestStep{
			{
				Config: `
				resource "pve_vm" "vm1" {
					name = "test-vm1-cpu-memory"
					template_name = "debian-10.11.4-20220312"
					target_node = "pve"
					target_storage = "local"
					cores = 1
					memory = 512
				}
				`,
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttrSet("pve_vm.vm1", "ipv4_address"),
					resource.TestCheckResourceAttr("pve_vm.vm1", "cores", "1"),
					resource.TestCheckResourceAttr("pve_vm.vm1", "memory", "512"),
				),
			},
			{
				Config: `
				# update cores and memory
				resource "pve_vm" "vm1" {
					name = "test-vm1-cpu-memory"
					template_name = "debian-10.11.4-20220312"
					target_node = "pve"
					target_storage = "local"
					cores = 2
					memory = 1024
				}
				`,
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttrSet("pve_vm.vm1", "ipv4_address"),
					resource.TestCheckResourceAttr("pve_vm.vm1", "cores", "2"),
					resource.TestCheckResourceAttr("pve_vm.vm1", "memory", "1024"),
				),
			},
		},
	})
}

func TestAccResourceVMUserData(t *testing.T) {
	resource.UnitTest(t, resource.TestCase{
		PreCheck:          func() { testAccPreCheck(t) },
		ProviderFactories: providerFactories,
		Steps: []resource.TestStep{
			{
				Config: `
				locals {
					password = "secret0001"
				}
				resource "pve_vm" "vm1" {
					name = "test-vm1-user-data"
					template_name = "debian-10.11.4-20220312"
					target_node = "pve"
					target_storage = "local"
					cores = 1
					memory = 512
					user_data = <<-EOF
					#cloud-config
					password: ${local.password}
					chpasswd:
					  expire: false
					EOF

					provisioner "remote-exec" {
						inline = [
							"echo user_data works! > /tmp/tf-pve-test.txt",
						]
						connection {
							type     = "ssh"
							user     = "debian"
							password = local.password
							host     = self.ipv4_address
						}
					}
				}
				`,
			},
		},
	})
}

func TestAccResourceVMSwitchTemplate(t *testing.T) {
	resource.UnitTest(t, resource.TestCase{
		PreCheck:          func() { testAccPreCheck(t) },
		ProviderFactories: providerFactories,
		Steps: []resource.TestStep{
			{
				Config: `
				locals {
					password = "secret0001"
				}
				resource "pve_vm" "vm1" {
					name = "test-vm1-user-data"
					template_name = "debian-10.11.4-20220312"
					target_node = "pve"
					target_storage = "local"
					cores = 1
					memory = 512
					user_data = <<-EOF
					#cloud-config
					password: ${local.password}
					chpasswd:
					  expire: false
					EOF

					provisioner "remote-exec" {
						inline = [
							"echo user_data works! > /tmp/tf-pve-test.txt",
						]
						connection {
							type     = "ssh"
							user     = "debian"
							password = local.password
							host     = self.ipv4_address
						}
					}
				}
				`,
			},
			// Upgrade to newer distro (no hardware change)
			{
				Config: `
				locals {
					password = "secret0001"
				}
				resource "pve_vm" "vm1" {
					name = "test-vm1-user-data"
					template_name = "debian-10.12.1-20220403"
					target_node = "pve"
					target_storage = "local"
					cores = 1
					memory = 512
					user_data = <<-EOF
					#cloud-config
					password: ${local.password}
					chpasswd:
					  expire: false
					EOF

					provisioner "remote-exec" {
						inline = [
							// since the template replaced, we expected this file written before must not exists
							"[ -f /tmp/tf-pve-test.txt ] && exit 1",
						]
						connection {
							type     = "ssh"
							user     = "debian"
							password = local.password
							host     = self.ipv4_address
						}
					}
				}
				`,
			},
			// Switch to other distro (hardware changed)
			{
				Config: `
				locals {
					password = "secret0001"
				}
				resource "pve_vm" "vm1" {
					name = "test-vm1-user-data"
					template_name = "ubuntu-20.04-20220315"
					target_node = "pve"
					target_storage = "local"
					cores = 1
					memory = 512
					user_data = <<-EOF
					#cloud-config
					password: ${local.password}
					chpasswd:
					  expire: false
					EOF

					provisioner "remote-exec" {
						inline = [
							"echo user_data works! > /tmp/tf-pve-test.txt",
						]
						connection {
							type     = "ssh"
							user     = "debian"
							password = local.password
							host     = self.ipv4_address
						}
					}
				}
				`,
			},
		},
	})
}

func TestAccResourceVMWithDisks(t *testing.T) {
	resource.UnitTest(t, resource.TestCase{
		PreCheck:          func() { testAccPreCheck(t) },
		ProviderFactories: providerFactories,
		ExternalProviders: map[string]resource.ExternalProvider{
			"null": {
				Source:            "hashicorp/null",
				VersionConstraint: "3.1.1",
			},
		},
		Steps: []resource.TestStep{
			{
				Config: `
				locals {
					password = "secret0001"
				}

				resource "pve_vm" "vm1" {
					name = "test-vm1-disks"
					template_name = "debian-10.11.4-20220312"
					target_node = "pve"
					target_storage = "local"
					cores = 1
					memory = 512
					user_data = <<-EOF
					#cloud-config
					password: ${local.password}
					chpasswd:
					  expire: false
					EOF

					disk {
						storage = "local"
						size = 8
					}
				}

				resource "null_resource" "check" {
					triggers = {
						# change this each step to trigger checking
						step = "1"
					}
					provisioner "remote-exec" {
						inline = [
							"[ $(lsblk /dev/sdb -o SIZE -n -r) = 8G ]",
						]
						connection {
							type     = "ssh"
							user     = "debian"
							password = local.password
							host     = pve_vm.vm1.ipv4_address
						}
					}
				}
				`,
			},
			// Add more disk
			{
				Config: `
				locals {
					password = "secret0001"
				}

				resource "pve_vm" "vm1" {
					name = "test-vm1-disks"
					template_name = "debian-10.11.4-20220312"
					target_node = "pve"
					target_storage = "local"
					cores = 1
					memory = 512
					user_data = <<-EOF
					#cloud-config
					password: ${local.password}
					chpasswd:
					  expire: false
					EOF

					disk {
						storage = "local"
						size = 8
					}
					disk {
						storage = "local"
						size = 8
					}
				}

				resource "null_resource" "check" {
					triggers = {
						# change this each step to trigger checking
						step = "2"
					}
					provisioner "remote-exec" {
						inline = [
							"[ $(lsblk /dev/sdb -o SIZE -n -r) = 8G ]",
							"[ $(lsblk /dev/sdc -o SIZE -n -r) = 8G ]",
						]
						connection {
							type     = "ssh"
							user     = "debian"
							password = local.password
							host     = pve_vm.vm1.ipv4_address
						}
					}
				}
				`,
			},
			// remove disk
			{
				Config: `
				locals {
					password = "secret0001"
				}

				resource "pve_vm" "vm1" {
					name = "test-vm1-disks"
					template_name = "debian-10.11.4-20220312"
					target_node = "pve"
					target_storage = "local"
					cores = 1
					memory = 512
					user_data = <<-EOF
					#cloud-config
					password: ${local.password}
					chpasswd:
					  expire: false
					EOF

					disk {
						storage = "local"
						size = 8
					}
				}

				resource "null_resource" "check" {
					triggers = {
						# change this each step to trigger checking
						step = "3"
					}
					provisioner "remote-exec" {
						inline = [
							"[ $(lsblk /dev/sdb -o SIZE -n -r) = 8G ]",
							# expect disk removed
							"lsblk /dev/sdc && exit 1 || exit 0",
						]
						connection {
							type     = "ssh"
							user     = "debian"
							password = local.password
							host     = pve_vm.vm1.ipv4_address
						}
					}
				}
				`,
			},
		},
	})
}

func TestAccResourceVMName(t *testing.T) {
	resource.UnitTest(t, resource.TestCase{
		PreCheck:          func() { testAccPreCheck(t) },
		ProviderFactories: providerFactories,
		Steps: []resource.TestStep{
			{
				Config: `
				resource "pve_vm" "vm1" {
					name = "test-vm1-name-1"
					template_name = "debian-10.11.4-20220312"
					target_node = "pve"
					target_storage = "local"
					cores = 1
					memory = 512
				}
				`,
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("pve_vm.vm1", "name", "test-vm1-name-1"),
				),
			},
			{
				Config: `
				resource "pve_vm" "vm1" {
					name = "test-vm1-name-2"
					template_name = "debian-10.11.4-20220312"
					target_node = "pve"
					target_storage = "local"
					cores = 1
					memory = 512
				}
				`,
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("pve_vm.vm1", "name", "test-vm1-name-2"),
				),
			},
		},
	})
}

func TestAccResourceVMOnBoot(t *testing.T) {
	resource.UnitTest(t, resource.TestCase{
		PreCheck:          func() { testAccPreCheck(t) },
		ProviderFactories: providerFactories,
		Steps: []resource.TestStep{
			{
				Config: `
				resource "pve_vm" "vm1" {
					name = "test-vm1-onboot"
					template_name = "debian-10.11.4-20220312"
					target_node = "pve"
					target_storage = "local"
					cores = 1
					memory = 512
				}
				`,
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("pve_vm.vm1", "onboot", "false"),
				),
			},
			{
				Config: `
				resource "pve_vm" "vm1" {
					name = "test-vm1-onboot"
					template_name = "debian-10.11.4-20220312"
					target_node = "pve"
					target_storage = "local"
					cores = 1
					memory = 512
					onboot = true
				}
				`,
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("pve_vm.vm1", "onboot", "true"),
				),
			},
			{
				Config: `
				resource "pve_vm" "vm1" {
					name = "test-vm1-onboot"
					template_name = "debian-10.11.4-20220312"
					target_node = "pve"
					target_storage = "local"
					cores = 1
					memory = 512
					onboot = false
				}
				`,
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("pve_vm.vm1", "onboot", "false"),
				),
			},
			{
				Config: `
				resource "pve_vm" "vm1" {
					name = "test-vm1-onboot"
					template_name = "debian-10.11.4-20220312"
					target_node = "pve"
					target_storage = "local"
					cores = 1
					memory = 512
				}
				`,
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("pve_vm.vm1", "onboot", "false"),
				),
			},
		},
	})
}

func TestAccResourceVMStatus(t *testing.T) {
	resource.UnitTest(t, resource.TestCase{
		PreCheck:          func() { testAccPreCheck(t) },
		ProviderFactories: providerFactories,
		Steps: []resource.TestStep{
			{
				Config: `
				resource "pve_vm" "vm1" {
					name = "test-vm1-status"
					template_name = "debian-10.11.4-20220312"
					target_node = "pve"
					target_storage = "local"
					cores = 1
					memory = 512
				}
				`,
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("pve_vm.vm1", "status", "running"),
				),
			},
			{
				Config: `
				resource "pve_vm" "vm1" {
					name = "test-vm1-status"
					template_name = "debian-10.11.4-20220312"
					target_node = "pve"
					target_storage = "local"
					cores = 1
					memory = 512
					status = "running"
				}
				`,
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("pve_vm.vm1", "status", "running"),
				),
			},
			{
				Config: `
				resource "pve_vm" "vm1" {
					name = "test-vm1-status"
					template_name = "debian-10.11.4-20220312"
					target_node = "pve"
					target_storage = "local"
					cores = 1
					memory = 512
					status = "stopped"
				}
				`,
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("pve_vm.vm1", "status", "stopped"),
				),
			},
			{
				Config: `
				resource "pve_vm" "vm1" {
					name = "test-vm1-status"
					template_name = "debian-10.11.4-20220312"
					target_node = "pve"
					target_storage = "local"
					cores = 1
					memory = 512
					status = "running"
				}
				`,
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("pve_vm.vm1", "status", "running"),
				),
			},
		},
	})
}

func TestExecuteCommandOnNode(t *testing.T) {
	endpoint := os.Getenv("PVE_ENDPOINT")
	username := os.Getenv("PVE_USERNAME")
	password := os.Getenv("PVE_PASSWORD")

	if endpoint == "" || username == "" || password == "" {
		t.Skip("PVE_ENDPOINT, PVE_USERNAME, PVE_PASSWORD no provided")
	}

	session, err := pxapi.NewSession(
		strings.TrimRight(endpoint, "/")+"/api2/json",
		nil,
		"",
		nil,
	)
	if err != nil {
		t.Error(err)
		return
	}
	if err := session.Login(username, password, ""); err != nil {
		t.Error(err)
		return
	}

	node := "pve"

	cmd := "date > /tmp/current-date.txt"

	if err := executeCommandOnNode(session, node, cmd); err != nil {
		t.Error(err)
		return
	}
}
