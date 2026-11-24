package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"time"

	gio "github.com/diamondburned/gotk4/pkg/gio/v2"
	gtk "github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/ojuny-project/nix-connect/internal/cli"
	"github.com/ojuny-project/nix-connect/internal/httpapi"
	"github.com/ojuny-project/nix-connect/internal/jobs"
	"github.com/ojuny-project/nix-connect/internal/pairing"
	"github.com/ojuny-project/nix-connect/internal/trust"
)

func main() {
	application := gtk.NewApplication("org.ojuny.NixConnect", gio.ApplicationFlagsNone)
	client := httpapi.NewLocal(cli.DefaultSocket)
	application.ConnectActivate(func() {
		build(application, client)
	})
	application.Run(os.Args)
}

func build(application *gtk.Application, client *httpapi.Client) {
	window := gtk.NewApplicationWindow(application)
	window.SetTitle("nix-connect")
	window.SetDefaultSize(1200, 700)

	box := gtk.NewBox(gtk.OrientationVertical, 6)
	box.SetMarginTop(12)
	box.SetMarginBottom(12)
	box.SetMarginStart(12)
	box.SetMarginEnd(12)

	header := gtk.NewLabel("nix-connect")
	header.SetXAlign(0)
	header.AddCSSClass("title-1")
	box.Append(header)

	status := gtk.NewLabel("")
	status.SetXAlign(0)
	box.Append(status)

	list := gtk.NewListBox()
	scrolled := gtk.NewScrolledWindow()
	scrolled.SetChild(list)
	scrolled.SetVExpand(true)
	box.Append(scrolled)

	actions := gtk.NewBox(gtk.OrientationHorizontal, 6)
	deploy := gtk.NewButtonWithLabel("Deploy")
	update := gtk.NewButtonWithLabel("Update")
	register := gtk.NewButtonWithLabel("Register")
	unregister := gtk.NewButtonWithLabel("Unregister")
	publish := gtk.NewButtonWithLabel("Publish")
	ssh := gtk.NewButtonWithLabel("SSH")
	pair := gtk.NewButtonWithLabel("Pair")
	trustButton := gtk.NewButtonWithLabel("Trust")
	jobsButton := gtk.NewButtonWithLabel("Jobs")
	reset := gtk.NewButtonWithLabel("Reset")
	rotate := gtk.NewButtonWithLabel("Rotate key")
	share := gtk.NewButtonWithLabel("Share key")
	refresh := gtk.NewButtonWithLabel("Refresh")
	for _, button := range []*gtk.Button{deploy, update, register, unregister, publish, ssh, pair, trustButton, jobsButton, reset, rotate, share, refresh} {
		actions.Append(button)
	}
	box.Append(actions)
	window.SetChild(box)

	var devices []httpapi.Device
	var load func()
	load = func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		loaded, err := client.Devices(ctx)
		if err != nil {
			status.SetText(err.Error())
			return
		}
		current, err := client.Status(ctx)
		if err != nil {
			status.SetText(err.Error())
			return
		}
		devices = loaded
		status.SetText(fmt.Sprintf("%s  %s  %d devices", current.Hostname, current.ShortID, len(devices)))
		list.RemoveAll()
		for _, device := range devices {
			label := fmt.Sprintf("%-20s  %-12s  %-10s  registered=%t  paired=%t",
				device.Hostname, device.Address, device.Kind, device.Registered, device.Paired)
			row := gtk.NewListBoxRow()
			labelWidget := gtk.NewLabel(label)
			labelWidget.SetXAlign(0)
			row.SetChild(labelWidget)
			list.Append(row)
		}
	}

	selected := func() (httpapi.Device, bool) {
		row := list.SelectedRow()
		if row == nil {
			return httpapi.Device{}, false
		}
		for index := range devices {
			if list.RowAtIndex(index) == row {
				return devices[index], true
			}
		}
		return httpapi.Device{}, false
	}

	deploy.ConnectClicked(func() {
		device, ok := selected()
		if !ok {
			status.SetText("select a device first")
			return
		}
		prompt(window, "Deploy", []string{"flake URL", "branch", "host", "disk", "account", "password", "firmware"},
			[]string{"", "main", "host", "", "", "", "efi"}, func(values []string) {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				job, err := client.Deploy(ctx, device.ID, httpapi.DeployRequest{
					Repository: values[0], Branch: values[1], Host: values[2],
					Disk: values[3], Account: values[4], Password: values[5], Firmware: values[6],
				})
				if err != nil {
					status.SetText(err.Error())
					return
				}
				status.SetText("job " + job)
			})
	})

	update.ConnectClicked(func() {
		device, ok := selected()
		if !ok {
			status.SetText("select a device first")
			return
		}
		prompt(window, "Update", []string{"password"}, []string{""}, func(values []string) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			job, err := client.Update(ctx, device.ID, httpapi.UpdateRequest{Password: values[0]})
			if err != nil {
				status.SetText(err.Error())
				return
			}
			status.SetText("job " + job)
		})
	})

	register.ConnectClicked(func() {
		device, ok := selected()
		if !ok {
			status.SetText("select a device first")
			return
		}
		prompt(window, "Register", []string{"flake URL", "branch", "host", "account", "password"},
			[]string{"", "main", "host", "", ""}, func(values []string) {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				if err := client.Register(ctx, device.ID, httpapi.RegisterRequest{
					Repository: values[0], Branch: values[1], Host: values[2],
					Account: values[3], Password: values[4],
				}); err != nil {
					status.SetText(err.Error())
					return
				}
				status.SetText("registration accepted")
				load()
			})
	})

	unregister.ConnectClicked(func() {
		device, ok := selected()
		if !ok {
			status.SetText("select a device first")
			return
		}
		prompt(window, "Unregister", []string{"password"}, []string{""}, func(values []string) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := client.Unregister(ctx, device.ID, values[0]); err != nil {
				status.SetText(err.Error())
				return
			}
			status.SetText("registration removed")
			load()
		})
	})

	publish.ConnectClicked(func() {
		prompt(window, "Publish", []string{"new remote", "branch", "password"},
			[]string{"", "main", ""}, func(values []string) {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				job, err := client.Publish(ctx, httpapi.PublishRequest{
					RemoteURL: values[0], Branch: values[1], Password: values[2],
				})
				if err != nil {
					status.SetText(err.Error())
					return
				}
				status.SetText("job " + job)
			})
	})

	ssh.ConnectClicked(func() {
		device, ok := selected()
		if !ok {
			status.SetText("select a device first")
			return
		}
		if err := openTerminal("nixconnect", "ssh", device.ID); err != nil {
			status.SetText(err.Error())
			return
		}
		status.SetText("ssh opened for " + device.Hostname)
	})

	var pairResponse *httpapi.PairResponse
	var pairTarget string
	pair.ConnectClicked(func() {
		device, ok := selected()
		if !ok {
			status.SetText("select a device first")
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if pairResponse != nil && pairTarget == device.ID {
			if err := client.PairConfirm(ctx, httpapi.PairConfirmRequest{ID: pairResponse.ID, Code: pairResponse.Code}); err != nil {
				status.SetText(err.Error())
				return
			}
			status.SetText("pairing confirmed for " + device.Hostname)
			pairResponse = nil
			pairTarget = ""
			return
		}
		response, err := client.PairStart(ctx, httpapi.PairRequest{PeerID: device.ID})
		if err != nil {
			status.SetText(err.Error())
			return
		}
		pairResponse = &response
		pairTarget = device.ID
		status.SetText("pairing code " + response.Code)
		messageDialog(window, "Pairing", fmt.Sprintf("Code %s for %s.\n\nConfirm on the target, then press Pair again.", response.Code, device.Hostname))
	})

	trustButton.ConnectClicked(func() {
		trustDialog(window, client, status)
	})

	jobsButton.ConnectClicked(func() {
		jobsDialog(window, client, status)
	})

	reset.ConnectClicked(func() {
		confirmAction(window, "Reset registration", "Reset the registration to no-commit with updates disabled?", func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := client.ResetRegistration(ctx); err != nil {
				status.SetText(err.Error())
				return
			}
			status.SetText("registration reset to no-commit")
			load()
		})
	})

	rotate.ConnectClicked(func() {
		confirmAction(window, "Rotate administration key", "Rotate the administration key? Existing deploytarget ISOs become invalid and every target must be redeployed.", func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			publicKey, err := client.RotateAdminKey(ctx)
			if err != nil {
				status.SetText(err.Error())
				return
			}
			status.SetText("administration key rotated: " + publicKey)
		})
	})

	share.ConnectClicked(func() {
		device, ok := selected()
		if !ok {
			status.SetText("select a paired device first")
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		current, err := client.Status(ctx)
		if err != nil {
			status.SetText(err.Error())
			return
		}
		code := pairing.Code(current.ID, device.ID)
		confirmAction(window, "Share administration key", "Key sharing code "+code+".\n\nImport the administration key from "+device.Hostname+"? The local key is replaced and existing ISOs become invalid.", func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			publicKey, err := client.ImportSharedKey(ctx, device.ID)
			if err != nil {
				status.SetText(err.Error())
				return
			}
			status.SetText("administration key imported: " + publicKey)
		})
	})

	refresh.ConnectClicked(load)
	load()
	window.Present()
}

func prompt(parent *gtk.ApplicationWindow, title string, labels, defaults []string, callback func([]string)) {
	dialog := gtk.NewDialog()
	dialog.SetTitle(title)
	dialog.SetTransientFor(&parent.Window)
	dialog.SetModal(true)
	dialog.AddButton("Cancel", int(gtk.ResponseCancel))
	dialog.AddButton("Run", int(gtk.ResponseOK))
	content := dialog.ContentArea()
	entries := make([]*gtk.Entry, len(labels))
	for index, label := range labels {
		entry := gtk.NewEntry()
		entry.SetPlaceholderText(label)
		if index < len(defaults) && defaults[index] != "" {
			entry.Buffer().SetText(defaults[index], -1)
		}
		content.Append(entry)
		entries[index] = entry
	}
	dialog.ConnectResponse(func(response int) {
		if response == int(gtk.ResponseOK) {
			values := make([]string, len(entries))
			for index, entry := range entries {
				values[index] = entry.Buffer().Text()
			}
			callback(values)
		}
		dialog.Close()
	})
	dialog.Present()
}

func confirmAction(parent *gtk.ApplicationWindow, title, text string, callback func()) {
	dialog := gtk.NewDialog()
	dialog.SetTitle(title)
	dialog.SetTransientFor(&parent.Window)
	dialog.SetModal(true)
	dialog.AddButton("Cancel", int(gtk.ResponseCancel))
	dialog.AddButton("Continue", int(gtk.ResponseOK))
	label := gtk.NewLabel(text)
	label.SetXAlign(0)
	label.SetWrap(true)
	dialog.ContentArea().Append(label)
	dialog.ConnectResponse(func(response int) {
		if response == int(gtk.ResponseOK) {
			callback()
		}
		dialog.Close()
	})
	dialog.Present()
}

func trustDialog(parent *gtk.ApplicationWindow, client *httpapi.Client, status *gtk.Label) {
	dialog := gtk.NewDialog()
	dialog.SetTitle("Pairing and trust")
	dialog.SetTransientFor(&parent.Window)
	dialog.SetModal(true)
	dialog.SetDefaultSize(820, 560)

	content := dialog.ContentArea()

	pendingTitle := gtk.NewLabel("Pending pairings")
	pendingTitle.SetXAlign(0)
	content.Append(pendingTitle)
	pendingList := gtk.NewListBox()
	pendingScrolled := gtk.NewScrolledWindow()
	pendingScrolled.SetChild(pendingList)
	pendingScrolled.SetVExpand(true)
	content.Append(pendingScrolled)

	trustTitle := gtk.NewLabel("Trusted devices")
	trustTitle.SetXAlign(0)
	content.Append(trustTitle)
	trustList := gtk.NewListBox()
	trustScrolled := gtk.NewScrolledWindow()
	trustScrolled.SetChild(trustList)
	trustScrolled.SetVExpand(true)
	content.Append(trustScrolled)

	message := gtk.NewLabel("")
	message.SetXAlign(0)
	content.Append(message)

	buttons := gtk.NewBox(gtk.OrientationHorizontal, 6)
	confirmPending := gtk.NewButtonWithLabel("Confirm selected pairing")
	revoke := gtk.NewButtonWithLabel("Revoke selected")
	buttons.Append(confirmPending)
	buttons.Append(revoke)
	content.Append(buttons)

	var pending []pairing.Request
	var records []trust.Record
	refresh := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		loadedPending, err := client.PairPending(ctx)
		if err != nil {
			message.SetText(err.Error())
			return
		}
		loadedRecords, err := client.TrustRecords(ctx)
		if err != nil {
			message.SetText(err.Error())
			return
		}
		pending = loadedPending
		records = loadedRecords
		message.SetText(fmt.Sprintf("%d pending, %d paired", len(pending), len(records)))
		pendingList.RemoveAll()
		for _, request := range pending {
			row := gtk.NewListBoxRow()
			labelWidget := gtk.NewLabel(fmt.Sprintf("%-22s  %-20s  code %s  %s",
				request.ID, request.PeerID, request.Code, request.Expires.Format("15:04")))
			labelWidget.SetXAlign(0)
			row.SetChild(labelWidget)
			pendingList.Append(row)
		}
		trustList.RemoveAll()
		for _, record := range records {
			row := gtk.NewListBoxRow()
			labelWidget := gtk.NewLabel(fmt.Sprintf("%-24s  %-18s  %-14s  %s",
				record.Fingerprint, record.Hostname, record.Address, record.PairedAt.Format("2006-01-02 15:04")))
			labelWidget.SetXAlign(0)
			row.SetChild(labelWidget)
			trustList.Append(row)
		}
	}
	refresh()

	confirmPending.ConnectClicked(func() {
		index := selectedIndex(pendingList, len(pending))
		if index < 0 {
			message.SetText("select a pending pairing first")
			return
		}
		request := pending[index]
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := client.PairConfirm(ctx, httpapi.PairConfirmRequest{ID: request.ID, Code: request.Code}); err != nil {
			message.SetText(err.Error())
			return
		}
		status.SetText("pairing confirmed with " + request.PeerID)
		refresh()
	})

	revoke.ConnectClicked(func() {
		index := selectedIndex(trustList, len(records))
		if index < 0 {
			message.SetText("select a paired device first")
			return
		}
		fingerprint := records[index].Fingerprint
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := client.RevokeTrust(ctx, fingerprint); err != nil {
			message.SetText(err.Error())
			return
		}
		status.SetText("trust record revoked: " + fingerprint)
		refresh()
	})

	dialog.AddButton("Close", int(gtk.ResponseClose))
	dialog.ConnectResponse(func(int) {
		dialog.Close()
	})
	dialog.Present()
}

func jobsDialog(parent *gtk.ApplicationWindow, client *httpapi.Client, status *gtk.Label) {
	dialog := gtk.NewDialog()
	dialog.SetTitle("Jobs")
	dialog.SetTransientFor(&parent.Window)
	dialog.SetModal(true)
	dialog.SetDefaultSize(900, 520)

	content := dialog.ContentArea()
	message := gtk.NewLabel("")
	message.SetXAlign(0)
	content.Append(message)

	list := gtk.NewListBox()
	scrolled := gtk.NewScrolledWindow()
	scrolled.SetChild(list)
	scrolled.SetVExpand(true)
	content.Append(scrolled)

	buttons := gtk.NewBox(gtk.OrientationHorizontal, 6)
	confirm := gtk.NewButtonWithLabel("Confirm selected")
	cancel := gtk.NewButtonWithLabel("Cancel selected")
	reload := gtk.NewButtonWithLabel("Refresh")
	buttons.Append(confirm)
	buttons.Append(cancel)
	buttons.Append(reload)
	content.Append(buttons)

	var items []jobs.Job
	refresh := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		loaded, err := client.Jobs(ctx)
		if err != nil {
			message.SetText(err.Error())
			return
		}
		items = loaded
		message.SetText(fmt.Sprintf("%d jobs", len(items)))
		list.RemoveAll()
		for _, job := range items {
			label := fmt.Sprintf("%-12s  %-8s  %-24s  %-22s  %s  %s",
				job.ID, job.Kind, job.Target, job.State, job.Phase, job.Updated.Format("15:04:05"))
			row := gtk.NewListBoxRow()
			labelWidget := gtk.NewLabel(label)
			labelWidget.SetXAlign(0)
			row.SetChild(labelWidget)
			list.Append(row)
		}
	}
	refresh()
	reload.ConnectClicked(refresh)

	confirm.ConnectClicked(func() {
		index := selectedIndex(list, len(items))
		if index < 0 {
			message.SetText("select a job first")
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := client.Confirm(ctx, items[index].ID); err != nil {
			message.SetText(err.Error())
			return
		}
		status.SetText("job confirmed: " + items[index].ID)
		refresh()
	})

	cancel.ConnectClicked(func() {
		index := selectedIndex(list, len(items))
		if index < 0 {
			message.SetText("select a job first")
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := client.Cancel(ctx, items[index].ID); err != nil {
			message.SetText(err.Error())
			return
		}
		status.SetText("job cancelled: " + items[index].ID)
		refresh()
	})

	dialog.AddButton("Close", int(gtk.ResponseClose))
	dialog.ConnectResponse(func(int) {
		dialog.Close()
	})
	dialog.Present()
}

func messageDialog(parent *gtk.ApplicationWindow, title, text string) {
	dialog := gtk.NewDialog()
	dialog.SetTitle(title)
	dialog.SetTransientFor(&parent.Window)
	dialog.SetModal(true)
	dialog.AddButton("Close", int(gtk.ResponseClose))
	label := gtk.NewLabel(text)
	label.SetXAlign(0)
	label.SetWrap(true)
	dialog.ContentArea().Append(label)
	dialog.ConnectResponse(func(int) {
		dialog.Close()
	})
	dialog.Present()
}

func selectedIndex(list *gtk.ListBox, count int) int {
	row := list.SelectedRow()
	if row == nil {
		return -1
	}
	for index := 0; index < count; index++ {
		if list.RowAtIndex(index) == row {
			return index
		}
	}
	return -1
}

func openTerminal(arguments ...string) error {
	for _, name := range []string{"xdg-terminal-exec", "ghostty", "xterm"} {
		path, err := exec.LookPath(name)
		if err != nil {
			continue
		}
		if name == "ghostty" || name == "xterm" {
			arguments = append([]string{"-e"}, arguments...)
		}
		command := exec.Command(path, arguments...)
		command.Env = os.Environ()
		return command.Start()
	}
	return fmt.Errorf("no terminal emulator found")
}
