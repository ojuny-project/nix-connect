package service

import (
	"reflect"
	"testing"
)

const blockDeviceListing = `{
  "blockdevices": [
    {
      "name": "nvme0n1",
      "path": "/dev/nvme0n1",
      "size": 256060514304,
      "type": "disk",
      "fstype": null,
      "tran": "nvme",
      "rm": false,
      "ro": false,
      "mountpoints": [],
      "model": "INTEL SSDPEKKF256G7H",
      "children": [
        {
          "name": "nvme0n1p1",
          "path": "/dev/nvme0n1p1",
          "size": 1073741824,
          "type": "part",
          "fstype": "vfat",
          "tran": "nvme",
          "rm": false,
          "ro": false,
          "mountpoints": ["/boot"],
          "model": null
        }
      ]
    }
  ]
}`

func TestTargetReportsBlockDevices(t *testing.T) {
	service, store := newTestService(t)
	store.Registration.Apply("https://example.invalid/flake", "main", "host", true, "", "")
	service.EngineRunner = &engineFakeRunner{disks: blockDeviceListing}
	info, err := service.Target(localContext(), store.Identity.Fingerprint())
	if err != nil {
		t.Fatal(err)
	}
	if info.Firmware != "efi" && info.Firmware != "bios" {
		t.Fatalf("unexpected firmware %q", info.Firmware)
	}
	if !reflect.DeepEqual(info.Registration, store.Registration) {
		t.Fatalf("unexpected registration %+v", info.Registration)
	}
	if len(info.Disks) != 1 || info.Disks[0].Path != "/dev/nvme0n1" {
		t.Fatalf("unexpected disks %+v", info.Disks)
	}
	children := info.Disks[0].Children
	if len(children) != 1 || children[0].FileSystem != "vfat" || len(children[0].Mountpoints) != 1 {
		t.Fatalf("unexpected children %+v", children)
	}
}

func TestTargetRejectsMalformedBlockDevices(t *testing.T) {
	service, store := newTestService(t)
	service.EngineRunner = &engineFakeRunner{disks: "not json"}
	if _, err := service.Target(localContext(), store.Identity.Fingerprint()); err == nil {
		t.Fatal("malformed block device output was accepted")
	}
}

func TestTargetIsLocalOnlyForUnknownCallers(t *testing.T) {
	service, _ := newTestService(t)
	if _, err := service.Target(peerContext("unpaired"), "peer"); err == nil || err.Error() != "service: peer is not paired" {
		t.Fatalf("unexpected error %v", err)
	}
}
