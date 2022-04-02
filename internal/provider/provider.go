package provider

import (
	"context"
	"strings"

	pxapi "github.com/Telmate/proxmox-api-go/proxmox"
	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

func init() {
	// Set descriptions to support markdown syntax, this will be used in document generation
	// and the language server.
	schema.DescriptionKind = schema.StringMarkdown

	// Customize the content of descriptions when output. For example you can add defaults on
	// to the exported descriptions if present.
	// schema.SchemaDescriptionBuilder = func(s *schema.Schema) string {
	// 	desc := s.Description
	// 	if s.Default != nil {
	// 		desc += fmt.Sprintf(" Defaults to `%v`.", s.Default)
	// 	}
	// 	return strings.TrimSpace(desc)
	// }
}

func New(version string) func() *schema.Provider {
	return func() *schema.Provider {
		p := &schema.Provider{
			Schema: map[string]*schema.Schema{
				"endpoint": {
					Type:        schema.TypeString,
					Required:    true,
					DefaultFunc: schema.EnvDefaultFunc("PVE_ENDPOINT", nil),
				},
				"username": {
					Type:        schema.TypeString,
					Required:    true,
					DefaultFunc: schema.EnvDefaultFunc("PVE_USERNAME", nil),
				},
				"password": {
					Type:        schema.TypeString,
					Sensitive:   true,
					Required:    true,
					DefaultFunc: schema.EnvDefaultFunc("PVE_PASSWORD", nil),
				},
			},
			ResourcesMap: map[string]*schema.Resource{
				"pve_vm": resourceVM(),
			},
		}

		p.ConfigureContextFunc = configure(version, p)

		return p
	}
}

type apiClient struct {
	*pxapi.Client
}

func configure(version string, p *schema.Provider) func(context.Context, *schema.ResourceData) (interface{}, diag.Diagnostics) {
	return func(ctx context.Context, d *schema.ResourceData) (interface{}, diag.Diagnostics) {
		endpoint := d.Get("endpoint").(string)
		username := d.Get("username").(string)
		password := d.Get("password").(string)

		client, err := pxapi.NewClient(
			strings.TrimRight(endpoint, "/")+"/api2/json",
			nil,
			nil,
			"",
			300,
		)
		if err != nil {
			return nil, diag.FromErr(err)
		}
		if err := client.Login(username, password, ""); err != nil {
			return nil, diag.FromErr(err)
		}

		return &apiClient{client}, nil
	}
}
