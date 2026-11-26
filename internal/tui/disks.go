package tui

import (
	"fmt"
	"strings"

	"github.com/ojuny-project/nix-connect/internal/httpapi"
)

const minimumDiskSize = 8 << 30

func eligibleDisks(disks []httpapi.Disk) []httpapi.Disk {
	eligible := make([]httpapi.Disk, 0, len(disks))
	for _, disk := range disks {
		if disk.Type != "disk" || disk.Removable || disk.ReadOnly {
			continue
		}
		if strings.EqualFold(disk.Transport, "usb") {
			continue
		}
		if disk.Size < minimumDiskSize {
			continue
		}
		if diskMounted(disk) {
			continue
		}
		if diskHasFileSystem(disk, "vfat") ||
			diskHasFileSystem(disk, "iso9660") ||
			diskHasFileSystem(disk, "squashfs") {
			continue
		}
		eligible = append(eligible, disk)
	}
	return eligible
}

func diskMounted(disk httpapi.Disk) bool {
	if len(disk.Mountpoints) > 0 {
		return true
	}
	for _, child := range disk.Children {
		if diskMounted(child) {
			return true
		}
	}
	return false
}

func diskHasFileSystem(disk httpapi.Disk, fileSystem string) bool {
	if strings.EqualFold(disk.FileSystem, fileSystem) {
		return true
	}
	for _, child := range disk.Children {
		if diskHasFileSystem(child, fileSystem) {
			return true
		}
	}
	return false
}

func diskLabel(disk httpapi.Disk) string {
	label := disk.Path + "  " + formatDiskSize(disk.Size)
	if disk.Model != "" {
		label += "  " + disk.Model
	}
	return label
}

func formatDiskSize(size uint64) string {
	const gibibyte = 1 << 30
	if size >= gibibyte {
		return fmt.Sprintf("%.1f GiB", float64(size)/float64(gibibyte))
	}
	const mebibyte = 1 << 20
	return fmt.Sprintf("%.0f MiB", float64(size)/float64(mebibyte))
}
