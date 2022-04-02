package provider

import (
	"context"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"

	pxapi "github.com/Telmate/proxmox-api-go/proxmox"

	"github.com/hashicorp/terraform-plugin-log/tflog"
	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/validation"
)

var (
	waitStoppedTimeout = 5 * time.Minute
	waitBootUpTimeout  = 5 * time.Minute
	pollDuration       = 2 * time.Second
)

func resourceVM() *schema.Resource {
	return &schema.Resource{
		Description: "VM",

		CreateContext: resourceVMCreate,
		ReadContext:   resourceVMRead,
		UpdateContext: resourceVMUpdate,
		DeleteContext: resourceVMDelete,

		Schema: map[string]*schema.Schema{
			"name": {
				Description: "VM name.",
				Type:        schema.TypeString,
				Required:    true,
				ForceNew:    true,
				ValidateFunc: validation.All(
					validation.StringIsNotEmpty,
					validation.StringMatch(regexp.MustCompile(`(?m)^[a-zA-Z0-9-.]+$`), "not a valid DNS name"),
				),
			},
			"template_name": {
				Description: "VM template.",
				Type:        schema.TypeString,
				Required:    true,
				ForceNew:    true,
				ValidateFunc: validation.All(
					validation.StringIsNotEmpty,
					validation.StringMatch(regexp.MustCompile(`(?m)^[a-zA-Z0-9-.]+$`), "not a valid DNS name"),
				),
			},
			"target_node": {
				Description:  "Node where this vm sit.",
				Type:         schema.TypeString,
				Required:     true,
				ForceNew:     true,
				ValidateFunc: validation.StringIsNotEmpty,
			},
			"target_storage": {
				Description:  "Storage where this vm sit.",
				Type:         schema.TypeString,
				Required:     true,
				ForceNew:     true,
				ValidateFunc: validation.StringIsNotEmpty,
			},
			"cores": {
				Description:  "Number of cpu core.",
				Type:         schema.TypeInt,
				Required:     true,
				ValidateFunc: validation.IntAtLeast(1),
			},
			"memory": {
				Description:  "Memory size in Megabyte",
				Type:         schema.TypeInt,
				Required:     true,
				ValidateFunc: validation.IntAtLeast(1),
			},
			"user_data": {
				Description: `
					cloud-init user data.

					Use this to provision vm, including ssh public key or password setup

					Learn more https://cloudinit.readthedocs.io/en/latest/topics/format.html
				`,
				Type:     schema.TypeString,
				Optional: true,
				ForceNew: true,
			},
			"ipv4_address": {
				Description: "IPv4 Address of this vm.",
				Type:        schema.TypeString,
				Computed:    true,
			},
		},
	}
}

func resourceVMCreate(ctx context.Context, d *schema.ResourceData, meta interface{}) diag.Diagnostics {
	client := meta.(*apiClient)

	tplrefs, err := client.GetVmRefsByName(d.Get("template_name").(string))
	if err != nil {
		return diag.FromErr(err)
	}

	if len(tplrefs) == 0 {
		return diag.Errorf("template not found")
	} else if len(tplrefs) > 1 {
		return diag.Errorf("found multiple template with same template_name")
	}

	tplref := tplrefs[0]
	if tplref.GetVmType() != "qemu" {
		return diag.Errorf("template is not for qemu vm")
	}

	newid, err := client.GetNextID(0)
	if err != nil {
		return diag.Errorf("failed to generate vmid: %s", err)
	}

	cloneParams := map[string]interface{}{
		"newid":  newid,
		"full":   true,
		"name":   d.Get("name").(string),
		"target": tplref.Node(),
	}

	_, err = client.CloneQemuVm(tplref, cloneParams)
	if err != nil {
		return diag.Errorf("failed to clone vm %d: %s", tplref.VmId(), err)
	}

	tflog.Trace(ctx, "vm cloned", map[string]interface{}{"vmid": newid})

	d.SetId(strconv.Itoa(newid))

	vmref := pxapi.NewVmRef(newid)

	updates := map[string]interface{}{}
	if cores, ok := d.GetOk("cores"); ok {
		updates["cores"] = cores
	}
	if memory, ok := d.GetOk("memory"); ok {
		updates["memory"] = memory
	}
	if len(updates) > 0 {
		if err := client.CheckVmRef(vmref); err != nil {
			return diag.Errorf("failed to check vm: %s", err)
		}
		if _, err := client.SetVmConfig(vmref, updates); err != nil {
			return diag.Errorf("failed to update cpu or memory: %s", err)
		}
	}

	_, err = client.StartVm(vmref)
	if err != nil {
		return diag.Errorf("failed to start vm %d: %s", vmref.VmId(), err)
	}

	vmConfig, err := client.GetVmConfig(vmref)
	if err != nil {
		return diag.Errorf("failed to get vm config: %s", err)
	}

	if agent, ok := vmConfig["agent"]; ok {
		if agentStr, ok := agent.(string); !ok {
			tflog.Warn(ctx, "agent parameter returned by pve is not a string, skip fetch ip address")
		} else if strings.Contains(agentStr, "enabled=1") {
			if diags := waitVMBootUpGetIP(ctx, client, vmref, d, waitBootUpTimeout); diags != nil {
				return diags
			}
		}
	}

	return resourceVMRead(ctx, d, meta)
}

func resourceVMRead(ctx context.Context, d *schema.ResourceData, meta interface{}) diag.Diagnostics {
	client := meta.(*apiClient)

	vmid, err := strconv.Atoi(d.Id())
	if err != nil {
		return diag.Errorf("faild to convert resource id to vmid: %s", err)
	}

	vmref := pxapi.NewVmRef(vmid)

	err = client.CheckVmRef(vmref)
	if err != nil {
		return diag.FromErr(err)
	}

	vmConfig, err := client.GetVmConfig(vmref)
	if err != nil {
		return diag.Errorf("failed to get vm config: %s", err)
	}

	d.Set("cores", int(vmConfig["cores"].(float64)))
	d.Set("memory", int(vmConfig["memory"].(float64)))

	if agent, ok := vmConfig["agent"]; ok {
		if agentStr, ok := agent.(string); !ok {
			tflog.Warn(ctx, "agent parameter returned by pve is not a string, skip fetch ip address")
		} else if strings.Contains(agentStr, "enabled=1") {
			if diags := waitVMBootUpGetIP(ctx, client, vmref, d, 1*time.Second); diags != nil {
				return diags
			}
		}
	}

	return nil
}

func resourceVMUpdate(ctx context.Context, d *schema.ResourceData, meta interface{}) diag.Diagnostics {
	client := meta.(*apiClient)

	vmid, err := strconv.Atoi(d.Id())
	if err != nil {
		return diag.Errorf("faild to convert resource id to vmid: %s", err)
	}

	vmref := pxapi.NewVmRef(vmid)

	updates := map[string]interface{}{}

	if d.HasChange("cores") {
		cores := d.Get("cores")
		updates["cores"] = cores

	}
	if d.HasChange("memory") {
		memory := d.Get("memory")
		updates["memory"] = memory
	}

	if len(updates) > 0 {
		if err := client.CheckVmRef(vmref); err != nil {
			return diag.Errorf("failed to check vm: %s", err)
		}
		if _, err := client.SetVmConfig(vmref, updates); err != nil {
			return diag.Errorf("failed to update cpu or memory: %s", err)
		}
		if _, err := client.ShutdownVm(vmref); err != nil {
			return diag.Errorf("failed to shutdown vm: %s", err)
		}
		if err := waitVMStopped(ctx, client, vmref, waitStoppedTimeout); err != nil {
			return diag.Errorf("wait vm stopped: %s", err)
		}
		if _, err := client.StartVm(vmref); err != nil {
			return diag.Errorf("failed to start vm: %s", err)
		}
		vmConfig, err := client.GetVmConfig(vmref)
		if err != nil {
			return diag.Errorf("failed to get vm config: %s", err)
		}
		if agent, ok := vmConfig["agent"]; ok {
			if agentStr, ok := agent.(string); !ok {
				tflog.Warn(ctx, "agent parameter returned by pve is not a string, skip fetch ip address")
			} else if strings.Contains(agentStr, "enabled=1") {
				if diags := waitVMBootUpGetIP(ctx, client, vmref, d, waitBootUpTimeout); diags != nil {
					return diags
				}

			}
		}
	}

	return resourceVMRead(ctx, d, meta)
}

func resourceVMDelete(ctx context.Context, d *schema.ResourceData, meta interface{}) diag.Diagnostics {
	client := meta.(*apiClient)

	vmid, err := strconv.Atoi(d.Id())
	if err != nil {
		return diag.Errorf("faild to convert resource id to vmid: %s", err)
	}

	vmref := pxapi.NewVmRef(vmid)

	_, err = client.StopVm(vmref)
	if err != nil {
		return diag.Errorf("failed to stop vm %d: %s", vmid, err)
	}

	if err := waitVMStopped(ctx, client, vmref, waitStoppedTimeout); err != nil {
		return diag.Errorf("wait vm stopped: %s", err)
	}
	tflog.Trace(ctx, "vm stopped")

	_, err = client.DeleteVm(vmref)
	if err != nil {
		return diag.FromErr(err)
	}
	tflog.Trace(ctx, "vm deleted")

	return nil
}

func waitVMStopped(ctx context.Context, client *apiClient, vmref *pxapi.VmRef, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	for {

		state, err := client.GetVmState(vmref)
		if err != nil {
			return err
		}
		if state["status"] == "stopped" {
			break
		}

		tflog.Trace(ctx, "vm state", state)

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(pollDuration):
		}
	}

	return nil
}

func waitVMBootUpGetIP(ctx context.Context, client *apiClient, vmref *pxapi.VmRef, d *schema.ResourceData, timeout time.Duration) diag.Diagnostics {
	tflog.Trace(ctx, "wait vm boot up")
	deadline, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	for {
		select {
		case <-deadline.Done():
			return diag.Errorf("timeout when waiting guest agent up and get ip address for that vm")
		default:
		}
		tflog.Trace(ctx, "check vm agent network interfaces")
		ifaces, err := client.GetVmAgentNetworkInterfaces(vmref)
		if err != nil {
			if strings.Contains(err.Error(), "guest agent is not running") {
				time.Sleep(pollDuration)
				continue
			}
			return diag.Errorf("failed to get agent network interfaces: %s", err)
		}
		tflog.Trace(ctx, "got vm agent network interfaces")
		for _, iface := range ifaces {
			if iface.Name == "eth0" {
				for _, ip := range iface.IPAddresses {
					if ip4 := ip.To4(); len(ip4) == net.IPv4len {
						d.Set("ipv4_address", ip.String())
					}
				}
			}
		}
		break
	}
	return nil
}
