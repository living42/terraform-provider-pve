package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"math/rand"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	pxapi "github.com/Telmate/proxmox-api-go/proxmox"
	"golang.org/x/net/websocket"

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

	tflog.Debug(ctx, "vm cloned", map[string]interface{}{"vmid": newid})

	d.SetId(strconv.Itoa(newid))

	vmref := pxapi.NewVmRef(newid)

	updates := map[string]interface{}{}
	if cores, ok := d.GetOk("cores"); ok {
		updates["cores"] = cores
	}
	if memory, ok := d.GetOk("memory"); ok {
		updates["memory"] = memory
	}
	if userData, ok := d.GetOk("user_data"); ok {
		if err := client.CheckVmRef(vmref); err != nil {
			return diag.Errorf("failed to check vm: %s", err)
		}

		snippetName := fmt.Sprintf("vm-%d-cloudinit-user-data", vmref.VmId())

		tflog.Debug(ctx, "upload snippets to local:"+snippetName)

		encoded := base64.StdEncoding.EncodeToString([]byte(userData.(string)))

		command := fmt.Sprintf("echo %q | base64 -d > /var/lib/vz/snippets/%s", encoded, snippetName)

		if err := executeCommandOnNode(client.session, vmref.Node(), command); err != nil {
			return diag.Errorf("failed to configure user_data: %s", err)
		}
		updates["cicustom"] = "user=local:snippets/" + snippetName
	}
	if len(updates) > 0 {
		if err := client.CheckVmRef(vmref); err != nil {
			return diag.Errorf("failed to check vm: %s", err)
		}
		if _, err := client.SetVmConfig(vmref, updates); err != nil {
			return diag.Errorf("failed to update cpu or memory: %s", err)
		}
	}

	tflog.Debug(ctx, "start vm", map[string]interface{}{"vmid": vmref.VmId()})
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

	restartNeeded := false

	vmref := pxapi.NewVmRef(vmid)

	updates := map[string]interface{}{}

	if d.HasChange("cores") {
		cores := d.Get("cores")
		updates["cores"] = cores
		restartNeeded = true
	}
	if d.HasChange("memory") {
		memory := d.Get("memory")
		updates["memory"] = memory
		restartNeeded = true
	}

	if len(updates) > 0 {
		if err := client.CheckVmRef(vmref); err != nil {
			return diag.Errorf("failed to check vm: %s", err)
		}
		if _, err := client.SetVmConfig(vmref, updates); err != nil {
			return diag.Errorf("failed to update cpu or memory: %s", err)
		}
	}

	if d.HasChange("template_name") {
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

		if _, err := client.ShutdownVm(vmref); err != nil {
			return diag.Errorf("failed to shutdown vm: %s", err)
		}
		if err := waitVMStopped(ctx, client, vmref, waitStoppedTimeout); err != nil {
			return diag.Errorf("wait vm stopped: %s", err)
		}

		if err := replaceTemplate(ctx, client, d.Get("name").(string), vmref, tplref); err != nil {
			return diag.Errorf("failed to replace template: %s", err)
		}

		restartNeeded = true
	}

	if restartNeeded {
		state, err := client.GetVmState(vmref)
		if err != nil {
			return diag.Errorf("failed to check vm state: %s", err)
		}
		if state["status"] != "stopped" {
			if _, err := client.ShutdownVm(vmref); err != nil {
				return diag.Errorf("failed to shutdown vm: %s", err)
			}
			if err := waitVMStopped(ctx, client, vmref, waitStoppedTimeout); err != nil {
				return diag.Errorf("wait vm stopped: %s", err)
			}
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

	vmConfig, err := client.GetVmConfig(vmref)
	if err != nil {
		return diag.Errorf("failed to get vm config: %s", err)
	}

	if cicustom, ok := vmConfig["cicustom"]; ok && strings.TrimSpace(cicustom.(string)) != "" {
		snippetName := fmt.Sprintf("vm-%d-cloudinit-user-data", vmref.VmId())
		tflog.Debug(ctx, "delete snippets to local:"+snippetName)
		command := "rm -f /var/lib/vz/snippets/" + snippetName
		if err := executeCommandOnNode(client.session, vmref.Node(), command); err != nil {
			tflog.Warn(ctx, "failed to delete snippets local:"+snippetName, map[string]interface{}{"err": err.Error()})
		}
	}

	tflog.Debug(ctx, "shutdown vm", map[string]interface{}{"vmid": vmid})

	_, err = client.ShutdownVm(vmref)
	if err != nil {
		return diag.Errorf("failed to stop vm %d: %s", vmid, err)
	}

	if err := waitVMStopped(ctx, client, vmref, waitStoppedTimeout); err != nil {
		return diag.Errorf("wait vm stopped: %s", err)
	}
	tflog.Debug(ctx, "vm stopped")

	_, err = client.DeleteVm(vmref)
	if err != nil {
		return diag.FromErr(err)
	}
	tflog.Debug(ctx, "vm deleted")

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

func executeCommandOnNode(session *pxapi.Session, node, command string) error {
	var respData struct {
		Data struct {
			Port   string `json:"port"`
			Ticket string `json:"ticket"`
			Upid   string `json:"upid"`
			User   string `json:"user"`
		} `json:"data"`
	}

	_, err := session.PostJSON(fmt.Sprintf("/nodes/%s/termproxy", node), nil, nil, nil, &respData)
	if err != nil {
		return fmt.Errorf("failed to acquire termproxy ticket: %s", err)
	}

	u, err := url.Parse(session.ApiUrl)
	if err != nil {
		return fmt.Errorf("failed to parse api url: %s", err)
	}
	origin := (&url.URL{
		Scheme: u.Scheme,
		Host:   u.Host,
	}).String()

	wsUrl := "ws" + strings.TrimPrefix(origin, "http") + fmt.Sprintf("/api2/json/nodes/%s/vncwebsocket", node)

	port := respData.Data.Port
	vncticket := respData.Data.Ticket

	query := url.Values{}
	query.Set("port", port)
	query.Set("vncticket", vncticket)

	wsUrl += "?" + query.Encode()

	wsConf, err := websocket.NewConfig(wsUrl, origin)
	if err != nil {
		return fmt.Errorf("failed to construct websocket config: %s", err)
	}
	wsConf.Protocol = []string{"binary"}
	if session.AuthToken != "" {
		wsConf.Header.Add("Authorization", "PVEAPIToken="+session.AuthToken)
	} else if session.AuthTicket != "" {
		wsConf.Header.Add("Cookie", "PVEAuthCookie="+session.AuthTicket)
		wsConf.Header.Add("CSRFPreventionToken", session.CsrfToken)
	}

	c, err := websocket.DialConfig(wsConf)
	if err != nil {
		return fmt.Errorf("failed to create websocket connection: %s", err)
	}
	defer c.Close()

	_, err = c.Write([]byte(respData.Data.User + ":" + respData.Data.Ticket + "\n"))
	if err != nil {
		return fmt.Errorf("failed to send ticket: %s", err)
	}

	b := make([]byte, 10)
	n, err := c.Read(b)
	if err != nil {
		return fmt.Errorf("failed to read response: %s", err)
	}
	if string(b[:n]) != "OK" {
		return fmt.Errorf("incorrect ticket: %s", err)
	}

	_, err = c.Write([]byte(`1:80:24:`))
	if err != nil {
		return fmt.Errorf("failed to send message: %s", err)
	}

	insertBegin := []byte{0x1b, '[', '2', '0', '0', '~'}
	insertEnd := []byte{0x1b, '[', '2', '0', '1', '~'}
	boundry := strconv.FormatInt(rand.Int63(), 36)
	cmd := base64.StdEncoding.EncodeToString([]byte(command))
	cmd = fmt.Sprintf(`%secho;echo CMD-BEGIN-%s;echo %q | base64 -d | bash; exit_status=$?; echo CMD-FINISH-%s; echo exit_status=$exit_status; echo CMD-END-%s%s`, insertBegin, boundry, cmd, boundry, boundry, insertEnd)
	cmd = fmt.Sprintf("0:%d:%s", len(cmd), cmd)

	_, err = c.Write([]byte(cmd))
	if err != nil {
		return fmt.Errorf("failed to send message: %s", err)
	}
	_, err = c.Write([]byte("0:1:\n"))
	if err != nil {
		return fmt.Errorf("failed to send message: %s", err)
	}

	lr := bufio.NewReader(c)
	// output := bytes.NewBuffer(nil)
	footer := bytes.NewBuffer(nil)
	state := "none"

loop:
	for {
		c.SetReadDeadline(time.Now().Add(30 * time.Second))
		line, err := lr.ReadString('\n')
		if err != nil {
			return fmt.Errorf("failed to read message")
		}
		switch state {
		case "none":
			switch line {
			case "CMD-BEGIN-" + boundry + "\r\n":
				state = "begin"
			}
		case "begin":
			switch line {
			case "CMD-FINISH-" + boundry + "\r\n":
				state = "finish"
				// default:
				// 	output.WriteString(line)
			}
		case "finish":
			switch line {
			case "CMD-END-" + boundry + "\r\n":
				state = "end"
				break loop
			default:
				footer.WriteString(line)
			}
		}
	}

	var exitStatus int
	_, err = fmt.Sscanf(footer.String(), "exit_status=%d", &exitStatus)
	if err != nil {
		return fmt.Errorf("exit_status not found in footer")
	}

	if exitStatus != 0 {
		return fmt.Errorf("command failed with exit status %d", exitStatus)
	}

	return nil
}

func replaceTemplate(ctx context.Context, client *apiClient, vmName string, vmref, tplref *pxapi.VmRef) error {
	newid, err := client.GetNextID(0)
	if err != nil {
		return fmt.Errorf("failed to generate vmid: %s", err)
	}

	cloneParams := map[string]interface{}{
		"newid":  newid,
		"full":   true,
		"name":   vmName + "-upgrade",
		"target": tplref.Node(),
	}

	_, err = client.CloneQemuVm(tplref, cloneParams)
	if err != nil {
		return fmt.Errorf("failed to clone vm %d: %s", tplref.VmId(), err)
	}
	newvmref := pxapi.NewVmRef(newid)
	defer func() {
		_, err = client.DeleteVm(newvmref)
		if err != nil {
			tflog.Warn(ctx, "failed to delete aux vm", map[string]interface{}{"vmid": newid})
		}
	}()

	tflog.Debug(ctx, "aux vm cloned", map[string]interface{}{"vmid": newid})

	if err := client.CheckVmRef(newvmref); err != nil {
		return fmt.Errorf("failed to refresh newly cloned vm: %s", err)
	}

	_, err = client.SetVmConfig(vmref, map[string]interface{}{"delete": "scsi0"})
	if err != nil {
		return fmt.Errorf("failed to detach disk: %s", err)
	}

	_, err = client.moveQemuDisk(newvmref, map[string]interface{}{
		"disk":        "scsi0",
		"delete":      true,
		"target-vmid": vmref.VmId(),
	})
	if err != nil {
		return fmt.Errorf("failed to replace disk: %s", err)
	}

	_, err = client.SetVmConfig(vmref, map[string]interface{}{"delete": "unused0"})
	if err != nil {
		return fmt.Errorf("failed to remove unused disk: %s", err)
	}

	return nil
}
