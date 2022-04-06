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
			// {
			// 	Config: `
			// 	locals {
			// 		password = "secret0001"
			// 	}
			// 	resource "pve_vm" "vm1" {
			// 		name = "test-vm1-user-data"
			// 		template_name = "ubuntu-20.04-20220315"
			// 		target_node = "pve"
			// 		target_storage = "local"
			// 		cores = 1
			// 		memory = 512
			// 		user_data = <<-EOF
			// 		#cloud-config
			// 		password: ${local.password}
			// 		chpasswd:
			// 		  expire: false
			// 		EOF

			// 		provisioner "remote-exec" {
			// 			inline = [
			// 				"echo user_data works! > /tmp/tf-pve-test.txt",
			// 			]
			// 			connection {
			// 				type     = "ssh"
			// 				user     = "debian"
			// 				password = local.password
			// 				host     = self.ipv4_address
			// 			}
			// 		}
			// 	}
			// 	`,
			// },
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
