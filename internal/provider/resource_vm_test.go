package provider

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/resource"
)

func TestAccResourceVM(t *testing.T) {
	resource.UnitTest(t, resource.TestCase{
		PreCheck:          func() { testAccPreCheck(t) },
		ProviderFactories: providerFactories,
		Steps: []resource.TestStep{
			{
				Config: `
				resource "pve_vm" "vm1" {
					name = "test-vm1"
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
					name = "test-vm1"
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
