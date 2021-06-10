package main

import (
	"context"
	"fmt"
	"github.com/hashicorp/hcl/v2/hcldec"
	"github.com/hashicorp/packer/common"
	"github.com/hashicorp/packer/helper/config"
	"github.com/hashicorp/packer/packer"
	"github.com/hashicorp/packer/packer/plugin"
	"github.com/vmware/govmomi"
	"github.com/vmware/govmomi/find"
	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/property"
	"github.com/vmware/govmomi/session"
	"github.com/vmware/govmomi/vim25"
	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/soap"
	"log"
	"net/url"
	"regexp"
	"sort"
	"time"

	"github.com/vmware/govmomi/vim25/types"
)

// Use this string before error messages due to packer don't do it himself
const pluginString = "plugin post-processor-vsphere-cleanup"

type VM interface {
	Name() string
	Destroy(ctx context.Context) (*object.Task, error)
	MarkAsVirtualMachine(ctx context.Context, pool object.ResourcePool, host *object.HostSystem) error
	HostSystem(ctx context.Context) (*object.HostSystem, error)
	Reference() types.ManagedObjectReference
}

type Config struct {
	common.PackerConfig `mapstructure:",squash"`

	VsphereServer          string         `mapstructure:"vcenter_server" required:"true"`
	VsphereDC              string         `mapstructure:"vcenter_dc" required:"true"`
	VsphereUsername        string         `mapstructure:"username" required:"true"`
	VspherePassword        string         `mapstructure:"password" required:"true"`
	VsphereAllowSelfSigned config.Trilean `mapstructure:"insecure_connection" required:"false"`

	ImageNameRegex string `mapstructure:"image_name_regex" required:"true"`
	KeepImages     int    `mapstructure:"keep_images" required:"false"`
	DryRun         bool   `mapstructure:"dry_run" required:"false"`
}

type Cleaner struct {
	config    Config
	finder    *find.Finder
	client    *govmomi.Client
	Ctx       context.Context
	collector *property.Collector
	ui        packer.Ui
}

func (c *Cleaner) Configure(raws ...interface{}) error {
	log.Printf("%s: configure plugin", pluginString)

	err := config.Decode(&c.config, nil, raws...)
	if err != nil {
		return err
	}

	errs := new(packer.MultiError)
	if c.config.VsphereServer == "" {
		errs = packer.MultiErrorAppend(errs, fmt.Errorf("%s: vsphere_url is required", pluginString))
	}
	if c.config.VsphereUsername == "" {
		errs = packer.MultiErrorAppend(errs, fmt.Errorf("%s: vsphere_username is required", pluginString))
	}
	if c.config.VspherePassword == "" {
		errs = packer.MultiErrorAppend(errs, fmt.Errorf("%s: vsphere_password is required", pluginString))
	}
	if c.config.VsphereDC == "" {
		errs = packer.MultiErrorAppend(errs, fmt.Errorf("%s: vsphere_dc is required", pluginString))
	}
	if c.config.VsphereAllowSelfSigned == config.TriUnset {
		c.config.VsphereAllowSelfSigned = config.TriTrue
	}
	if c.config.ImageNameRegex == "" {
		errs = packer.MultiErrorAppend(errs, fmt.Errorf("%s: image_name_regex is required", pluginString))
	}
	if c.config.KeepImages == 0 {
		c.config.KeepImages = 2
	}

	if len(errs.Errors) > 0 {
		return errs
	}

	c.Ctx = context.Background()

	return c.Init()
}

func NewClient(ctx context.Context, host string, username string, password string, insecureFlag bool) (*govmomi.Client, error) {
	urlString := fmt.Sprintf("https://%s%s", host, vim25.Path)
	u, err := url.Parse(urlString)
	if err != nil {
		return nil, packer.MultiErrorAppend(fmt.Errorf("unable to parse url '%s'", urlString), err)
	}
	credentials := url.UserPassword(username, password)
	u.User = credentials

	soapClient := soap.NewClient(u, insecureFlag)
	vimClient, err := vim25.NewClient(ctx, soapClient)
	if err != nil {
		return nil, err
	}

	vimClient.RoundTripper = session.KeepAlive(vimClient.RoundTripper, 1*time.Minute)

	client := &govmomi.Client{
		Client:         vimClient,
		SessionManager: session.NewManager(vimClient),
	}

	err = client.SessionManager.Login(ctx, credentials)
	if err != nil {
		return nil, err
	}
	return client, nil
}

func (c *Cleaner) Init() error {
	log.Printf("Init")
	var err error
	c.client, err = NewClient(c.Ctx, c.config.VsphereServer, c.config.VsphereUsername, c.config.VspherePassword,
		c.config.VsphereAllowSelfSigned.True())
	if err != nil {
		return packer.MultiErrorAppend(fmt.Errorf(
			"%s: unable to create vsphere client with: vcenter_server='%s', username='%s', insecure_connection='%s'",
			pluginString,
			c.config.VsphereServer,
			c.config.VsphereUsername,
			c.config.VsphereAllowSelfSigned.ToString()), err)
	}
	c.finder = find.NewFinder(c.client.Client, false)
	datacenter, err := c.finder.DatacenterOrDefault(c.Ctx, c.config.VsphereDC)
	if err != nil {
		return packer.MultiErrorAppend(fmt.Errorf(
			"%s: unable to find vsphere datacenter '%s'",
			pluginString,
			c.config.VsphereDC), err)
	}
	c.finder.SetDatacenter(datacenter)
	c.collector = property.DefaultCollector(c.client.Client)
	return nil
}

func (c *Cleaner) PostProcess(ctx context.Context, ui packer.Ui, artifact packer.Artifact) (packer.Artifact, bool, bool, error) {
	defer func() {
		err := c.client.Logout(c.Ctx)
		if err != nil {
			log.Fatal(err)
		}
	}()
	c.ui = ui

	var templates = make(templateList, 0)
	var newTemplate *template

	re := regexp.MustCompile(c.config.ImageNameRegex)
	c.ui.Message(fmt.Sprintf("Using image name regexp: %s", re.String()))

	items, err := c.finder.VirtualMachineList(ctx, "*")
	if err != nil {
		c.ui.Error(fmt.Sprintf("Unable to retrieve virtual machines: %s", err))
		return artifact, true, true, err
	}

	var temp *template
	for _, t := range items {
		temp = getTemplate(re, t)
		if temp != nil {
			if temp.name == artifact.Id() {
				newTemplate = temp
			} else {
				templates = append(templates, temp)
			}
		}
	}

	sort.Sort(byVersion(templates))
	var deleted templateList
	var kept templateList
	extra := len(templates) - c.config.KeepImages
	if extra > 0 {
		deleted = templates[:extra]
		kept = templates[extra:]
	} else {
		deleted = make(templateList, 0)
		kept = templates
	}
	if newTemplate != nil {
		kept = append(kept, newTemplate)
	}

	c.ui.Message(fmt.Sprintf("Virtual machines selected for deletion: %s", deleted.toString()))
	c.ui.Message(fmt.Sprintf("Virtual machines will be kept: %s", kept.toString()))

	if !c.config.DryRun {
		c.deleteTemplate(ctx, deleted)
	}
	return artifact, true, true, nil
}

func (c *Cleaner) ConfigSpec() hcldec.ObjectSpec {
	return nil
}

func (c *Cleaner) deleteTemplate(ctx context.Context, list templateList) {
	pools, err := c.finder.ResourcePoolList(ctx, "*")
	if err != nil {
		c.ui.Error(fmt.Sprintf("Unable to retrieve resource pools: %s", err))
		return
	}

	vmInfo := &mo.VirtualMachine{}
	for _, d := range list {
		c.ui.Message(fmt.Sprintf("Deleting virtual machine '%s'", d.name))

		err := c.collector.RetrieveOne(ctx, d.ref.Reference(), nil, vmInfo)
		if err != nil {
			c.ui.Error(fmt.Sprintf("Erorr occured during retrieving information about VM '%s', skiping deletion: %s", d.name, err))
			continue
		}

		if vmInfo.Config.Template == true {
			c.ui.Message(fmt.Sprintf("'%s' is a template, trying to convert it to virtual machine", d.name))
			host, err := d.ref.HostSystem(ctx)
			if err != nil {
				c.ui.Error(fmt.Sprintf("Error occured during retirieving host of '%s': %s", d.name, err))
				continue
			}

			hostSystem := mo.HostSystem{}
			err = c.collector.Retrieve(ctx, []types.ManagedObjectReference{host.Reference()}, []string{"name"}, &hostSystem)

			c.ui.Message(fmt.Sprintf("Template '%s' is registered on host '%s'", d.name, hostSystem.Name))

			var pool *object.ResourcePool
			for _, rp := range pools {
				if matchHost(hostSystem.Name, rp.InventoryPath) {
					pool = rp
					c.ui.Message(fmt.Sprintf("Using resource pool '%s' for conversion", rp.InventoryPath))
					if err := d.ref.MarkAsVirtualMachine(ctx, *pool, nil); err != nil {
						c.ui.Error(fmt.Sprintf("Error occurred during template '%s' conversion: %s", d.name, err))
						continue
					} else {
						break
					}
				}
			}
			if pool == nil {
				var x resourcePools
				x = pools
				c.ui.Error(fmt.Sprintf("Cannot find relevant resource pool on host '%s' for conversion, available pools: %s", hostSystem.Name, x.toString()))
				continue
			}
		}
		_, err = d.ref.Destroy(ctx)
		if err != nil {
			c.ui.Error(fmt.Sprintf("Error occurred during '%s' deletion: %s", d.name, err))
		}
	}
}

func main() {
	server, err := plugin.Server()
	if err != nil {
		panic(err)
	}

	err = server.RegisterPostProcessor(new(Cleaner))
	if err != nil {
		panic(err)
	}
	server.Serve()
}
