package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/ojuny-project/nix-connect/internal/httpapi"
)

type blockDevice struct {
	Name        string        `json:"name"`
	Path        string        `json:"path"`
	Size        uint64        `json:"size"`
	Type        string        `json:"type"`
	FileSystem  string        `json:"fstype"`
	Transport   string        `json:"tran"`
	Removable   bool          `json:"rm"`
	ReadOnly    bool          `json:"ro"`
	Mountpoints []string      `json:"mountpoints"`
	Model       string        `json:"model"`
	Children    []blockDevice `json:"children"`
}

func (s *Service) Target(ctx context.Context, id string) (httpapi.TargetInfo, error) {
	if err := s.requireTrusted(ctx); err != nil {
		return httpapi.TargetInfo{}, err
	}
	if id != s.Store.Identity.Fingerprint() {
		client, err := s.remoteClient(id)
		if err != nil {
			return httpapi.TargetInfo{}, err
		}
		return client.Target(ctx, id)
	}
	disks, err := s.localDisks(ctx)
	if err != nil {
		return httpapi.TargetInfo{}, err
	}
	firmware := "bios"
	if _, err := os.Stat("/sys/firmware/efi"); err == nil {
		firmware = "efi"
	}
	return httpapi.TargetInfo{
		Registration: s.Store.Registration,
		Firmware:     firmware,
		Disks:        disks,
	}, nil
}

func (s *Service) localDisks(ctx context.Context) ([]httpapi.Disk, error) {
	output, err := s.runner().Run(ctx, "", nil, "lsblk", "-J", "-b", "-o",
		"NAME,PATH,SIZE,TYPE,FSTYPE,TRAN,RM,RO,MOUNTPOINTS,MODEL")
	if err != nil {
		return nil, fmt.Errorf("service: list block devices: %w: %s", err, output)
	}
	if index := strings.IndexByte(output, '{'); index > 0 {
		output = output[index:]
	}
	var listing struct {
		BlockDevices []blockDevice `json:"blockdevices"`
	}
	if err := json.Unmarshal([]byte(output), &listing); err != nil {
		return nil, fmt.Errorf("service: parse block devices: %w", err)
	}
	return convertBlockDevices(listing.BlockDevices), nil
}

func convertBlockDevices(devices []blockDevice) []httpapi.Disk {
	result := make([]httpapi.Disk, 0, len(devices))
	for _, device := range devices {
		result = append(result, httpapi.Disk{
			Name:        device.Name,
			Path:        device.Path,
			Size:        device.Size,
			Type:        device.Type,
			FileSystem:  device.FileSystem,
			Transport:   device.Transport,
			Removable:   device.Removable,
			ReadOnly:    device.ReadOnly,
			Mountpoints: device.Mountpoints,
			Model:       device.Model,
			Children:    convertBlockDevices(device.Children),
		})
	}
	return result
}
