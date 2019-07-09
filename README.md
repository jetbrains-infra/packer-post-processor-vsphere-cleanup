# packer-post-processor-vsphere-cleanup
Packer plugin for cleanup templates in vsphere
# How to use

```json
{
  ...
  "post-processors":[
    {
      "type": "vsphere-cleanup",
      "vcenter_server": "<required, vcenter server hostname>",
      "vcenter_dc": "<required, datacenter name>",
      "username": "<required, vcenter user login>",
      "password": "<required, vcenter user password>",
      "insecure_connection": "<true by default>",
      "image_name_regex": "ubuntu-18.04-image-([0-9]+)",
      "keep_images": "<2 by default, keep 2 last images>",
      "dry_run": "<false by default>"
    }
  ]
}
```
Result:
```bash
...
=> vsphere-clone: Running post-processor: vsphere-cleanup
   vsphere-clone (vsphere-cleanup): Using regexp: ubuntu-18.04-image-([0-9]+)
   vsphere-clone (vsphere-cleanup): Next machines selected for deletion: [ubuntu-18.04-image-38]
   vsphere-clone (vsphere-cleanup): Next machines will be kept: [ubuntu-18.04-image-39, ubuntu-18.04-image-40]
   vsphere-clone (vsphere-cleanup): Deleting virtual machine 'ubuntu-18.04-image-38'
   vsphere-clone (vsphere-cleanup): 'ubuntu-18.04-image-38' is template, try to conevert it to virtual machine
   vsphere-clone (vsphere-cleanup): Select resource pool /DC/host/Pool/server-1.local.net/Resources/ResourcePool1 for convertation before deletion
   vsphere-clone (vsphere-cleanup): Convertation info: pool '/DC/host/Pool/server-1.local.net/Resources/ResourcePool1', host 'server-1.local.net'
Build 'vsphere-clone' finished.

```
