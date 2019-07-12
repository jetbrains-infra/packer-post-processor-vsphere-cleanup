package main

import (
	"context"
	"fmt"
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
	"strconv"
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
	common.PackerConfig    `mapstructure:",squash"`
	VsphereServer          string `mapstructure:"vcenter_server"`
	VsphereDC              string `mapstructure:"vcenter_dc"`
	VsphereUsername        string `mapstructure:"username"`
	VspherePassword        string `mapstructure:"password"`
	VsphereAllowSelfSigned string `mapstructure:"insecure_connection"`
	ImageNameRegex         string `mapstructure:"image_name_regex"`
	KeepImages             string `mapstructure:"keep_images"`
	DryRun                 string `mapstructure:"dry_run"`
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
	if c.config.VsphereAllowSelfSigned == "" {
		c.config.VsphereAllowSelfSigned = "true"
	}
	if c.config.ImageNameRegex == "" {
		errs = packer.MultiErrorAppend(errs, fmt.Errorf("%s: image_name_regex is required", pluginString))
	}
	if c.config.KeepImages == "" {
		c.config.KeepImages = "2"
	}
	if c.config.DryRun == "" {
		c.config.DryRun = "false"
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
		return nil, packer.MultiErrorAppend(fmt.Errorf("unable to parse url %s, %s", urlString, err))
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
		c.config.VsphereAllowSelfSigned == "true")
	if err != nil {
		return packer.MultiErrorAppend(fmt.Errorf(
			"%s: unable to create vsphere client with: vsphere_url='%s' vsphere_username='%s', vsphere_allow_selfsigned='%s'",
			pluginString,
			c.config.VsphereServer,
			c.config.VsphereUsername,
			c.config.VsphereAllowSelfSigned), err)
	}
	c.finder = find.NewFinder(c.client.Client, false)
	datacenter, err := c.finder.DatacenterOrDefault(c.Ctx, c.config.VsphereDC)
	if err != nil {
		return err
	}
	c.finder.SetDatacenter(datacenter)
	c.collector = property.DefaultCollector(c.client.Client)
	return nil
}

func (c *Cleaner) PostProcess(ui packer.Ui, p packer.Artifact) (a packer.Artifact, keep bool, err error) {
	defer func() {
		err := c.client.Logout(c.Ctx)
		if err != nil {
			log.Fatal(err)
		}
	}()
	c.ui = ui

	var templates templateList

	re := regexp.MustCompile(c.config.ImageNameRegex)
	c.ui.Message(fmt.Sprintf("Using regexp: %s", re.String()))

	items, err := c.finder.VirtualMachineList(c.Ctx, "*")
	if err != nil {
		log.Fatal(err)
	}

	var temp *template
	for _, t := range items {
		temp = getTemplate(re, t)
		if temp != nil {
			templates = append(templates, temp)
		}
	}

	keepImages, err := strconv.Atoi(c.config.KeepImages)
	if err != nil {
		return a, true, packer.MultiErrorAppend(
			fmt.Errorf("unable to use %s as KeepImages", c.config.KeepImages), err)
	}

	sort.Sort(byVersion(templates))
	var deleted templateList
	var kept templateList
	if len(templates) > keepImages {
		deleted = templates[:len(templates)-keepImages]
		kept = templates[len(templates)-keepImages:]
	} else {
		kept = templates
	}

	c.ui.Message(fmt.Sprintf("Next machines selected for deletion: %s", deleted.toString()))
	c.ui.Message(fmt.Sprintf("Next machines will be kept: %s", kept.toString()))

	if !parseBool(c.config.DryRun, false) {
		c.deleteTemplate(deleted)
	}
	return nil, false, nil
}

func (c *Cleaner) deleteTemplate(list templateList) {
	rplist, err := c.finder.ResourcePoolList(c.Ctx, "*")
	if err != nil {
		c.ui.Error(fmt.Sprintf("Unable to retrieve resource pools: %s", err))
		return
	}

	vmInfo := &mo.VirtualMachine{}
	for _, d := range list {
		c.ui.Message(fmt.Sprintf("Deleting virtual machine '%s'", d.name))

		err := c.collector.RetrieveOne(c.Ctx, d.ref.Reference(), nil, vmInfo)
		if err != nil {
			c.ui.Error(fmt.Sprintf("During retrieving of information about vm %s, error occured %s, skip deletion", d.name, err))
			continue
		}

		if vmInfo.Config.Template == true {
			c.ui.Message(fmt.Sprintf("'%s' is template, try to conevert it to virtual machine", d.name))
			host, err := d.ref.HostSystem(c.Ctx)
			if err != nil {
				c.ui.Error(fmt.Sprintf("During getting host of %s, error occurred, %s", d.name, err))
				continue
			}

			hostSystem := mo.HostSystem{}
			err = c.collector.Retrieve(c.Ctx, []types.ManagedObjectReference{host.Reference()}, []string{"name"}, &hostSystem)

			pool := &object.ResourcePool{}
			for _, rp := range rplist {
				if matchHost(hostSystem.Name, rp.InventoryPath) {
					pool = rp
					c.ui.Message(fmt.Sprintf("Select resource pool %s for convertation before deletion", rp.InventoryPath))
					break
				}
			}

			c.ui.Message(fmt.Sprintf("Convertation info: pool '%s', host '%s'", pool.InventoryPath, hostSystem.Name))
			err = d.ref.MarkAsVirtualMachine(c.Ctx, *pool, host)
			if err != nil {
				c.ui.Error(fmt.Sprintf("During converation of template %s, error occurred, %s", d.name, err))
				continue
			}
		}
		_, err = d.ref.Destroy(c.Ctx)
		if err != nil {
			c.ui.Error(fmt.Sprintf("During deleting of %s, error occured, %s", d.name, err))
		}
	}
}

func main() {
	server, err := plugin.Server()
	if err != nil {
		panic(err)
	}

	server.RegisterPostProcessor(new(Cleaner))
	server.Serve()
}
