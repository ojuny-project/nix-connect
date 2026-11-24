package gui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	gdk "github.com/diamondburned/gotk4/pkg/gdk/v4"
	gio "github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	gtk "github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"

	"github.com/ojuny-project/nix-connect/internal/httpapi"
	"github.com/ojuny-project/nix-connect/internal/pairing"
)

type App struct {
	application  *gtk.Application
	client       *httpapi.Client
	model        *Model
	window       *gtk.ApplicationWindow
	headerHost   *gtk.Label
	headerState  *gtk.Label
	banner       *gtk.Box
	bannerLabel  *gtk.Label
	deviceList   *gtk.ListBox
	statusBox    *gtk.Box
	actions      map[string]*gtk.Button
	pairResponse *httpapi.PairResponse
	pairTarget   string
}

func Run(client *httpapi.Client) error {
	application := gtk.NewApplication("org.ojuny.NixConnect", gio.ApplicationFlagsNone)
	value := &App{
		application: application,
		client:      client,
		model:       NewModel(client),
		actions:     make(map[string]*gtk.Button),
	}
	application.ConnectActivate(value.build)
	application.Run(os.Args)
	return nil
}

func (a *App) build() {
	applyStyle()

	window := gtk.NewApplicationWindow(a.application)
	window.SetTitle("nix-connect")
	window.SetDefaultSize(1280, 780)
	a.window = window

	window.SetTitlebar(a.buildHeader())

	root := gtk.NewBox(gtk.OrientationVertical, 10)
	root.SetMarginTop(12)
	root.SetMarginBottom(12)
	root.SetMarginStart(12)
	root.SetMarginEnd(12)

	a.banner = a.buildBanner()
	root.Append(a.banner)

	paned := gtk.NewPaned(gtk.OrientationHorizontal)
	paned.SetStartChild(a.buildDevicePanel())
	paned.SetEndChild(a.buildStatusPanel())
	paned.SetPosition(620)
	paned.SetVExpand(true)
	root.Append(paned)

	root.Append(a.buildActions())
	window.SetChild(root)

	controller := gtk.NewEventControllerKey()
	controller.ConnectKeyPressed(func(keyval, keycode uint, state gdk.ModifierType) bool {
		if state&gdk.ControlMask != 0 {
			switch keyval {
			case gdk.KEY_r:
				a.refresh()
				return true
			case gdk.KEY_d:
				a.deploy()
				return true
			case gdk.KEY_u:
				a.update()
				return true
			case gdk.KEY_g:
				a.register()
				return true
			case gdk.KEY_n:
				a.unregister()
				return true
			case gdk.KEY_p:
				a.publish()
				return true
			case gdk.KEY_a:
				a.pair()
				return true
			case gdk.KEY_s:
				a.ssh()
				return true
			case gdk.KEY_t:
				a.openTrust()
				return true
			case gdk.KEY_j:
				a.openJobs()
				return true
			case gdk.KEY_i:
				a.buildISO()
				return true
			}
		}
		if keyval == gdk.KEY_Escape {
			a.model.Selected = -1
			a.model.Detail = false
			a.deviceList.UnselectAll()
			a.refreshStatus()
			a.updateActions()
			return true
		}
		return false
	})
	window.AddController(controller)

	a.refresh()
	window.Present()
}

func (a *App) buildHeader() *gtk.HeaderBar {
	bar := gtk.NewHeaderBar()

	title := gtk.NewBox(gtk.OrientationHorizontal, 8)
	name := gtk.NewLabel("nix-connect")
	name.AddCSSClass("nc-title")
	title.Append(name)
	a.headerHost = gtk.NewLabel("")
	a.headerHost.AddCSSClass("nc-muted")
	title.Append(a.headerHost)
	bar.SetTitleWidget(title)

	a.headerState = gtk.NewLabel("daemon offline")
	a.headerState.AddCSSClass("nc-muted")
	bar.PackStart(a.headerState)

	refresh := gtk.NewButton()
	refresh.SetIconName("view-refresh-symbolic")
	refresh.SetTooltipText("Reload devices, status, jobs, trust and pending pairings (Ctrl+R)")
	refresh.ConnectClicked(a.refresh)
	bar.PackEnd(refresh)

	return bar
}

func (a *App) buildBanner() *gtk.Box {
	banner := gtk.NewBox(gtk.OrientationHorizontal, 8)
	banner.AddCSSClass("nc-banner")
	banner.SetVisible(false)
	a.bannerLabel = gtk.NewLabel("")
	a.bannerLabel.SetXAlign(0)
	a.bannerLabel.SetWrap(true)
	a.bannerLabel.SetHExpand(true)
	dismiss := gtk.NewButtonWithLabel("Dismiss")
	dismiss.SetTooltipText("Hide this message")
	dismiss.ConnectClicked(func() { banner.SetVisible(false) })
	banner.Append(a.bannerLabel)
	banner.Append(dismiss)
	return banner
}

func (a *App) buildDevicePanel() gtk.Widgetter {
	frame := gtk.NewFrame("Devices")
	box := gtk.NewBox(gtk.OrientationVertical, 6)
	box.SetMarginTop(8)
	box.SetMarginBottom(8)
	box.SetMarginStart(8)
	box.SetMarginEnd(8)

	header := gtk.NewGrid()
	header.SetColumnSpacing(12)
	header.SetMarginStart(6)
	header.SetMarginEnd(6)
	columns := []struct {
		title string
		width int
	}{
		{"HOST", 160},
		{"ID", 110},
		{"ADDRESS", 120},
		{"KIND", 80},
		{"STATE", 90},
	}
	for index, column := range columns {
		label := gtk.NewLabel(column.title)
		label.AddCSSClass("nc-table-header")
		label.SetXAlign(0)
		label.SetSizeRequest(column.width, -1)
		header.Attach(label, index, 0, 1, 1)
	}
	box.Append(header)
	box.Append(gtk.NewSeparator(gtk.OrientationHorizontal))

	a.deviceList = gtk.NewListBox()
	a.deviceList.SetSelectionMode(gtk.SelectionSingle)
	a.deviceList.SetTooltipText("Discovered devices. Use the arrow keys to select and Enter for details.")
	placeholder := gtk.NewLabel("No devices discovered yet.\nPress Refresh or Ctrl+R to scan.")
	placeholder.AddCSSClass("nc-muted")
	placeholder.SetMarginTop(24)
	placeholder.SetMarginBottom(24)
	a.deviceList.SetPlaceholder(placeholder)
	a.deviceList.ConnectRowSelected(func(row *gtk.ListBoxRow) {
		a.model.Select(row.Index())
		a.refreshStatus()
		a.updateActions()
	})
	a.deviceList.ConnectRowActivated(func(row *gtk.ListBoxRow) {
		a.model.Select(row.Index())
		a.refreshStatus()
		a.updateActions()
	})

	scrolled := gtk.NewScrolledWindow()
	scrolled.SetChild(a.deviceList)
	scrolled.SetVExpand(true)
	box.Append(scrolled)

	frame.SetChild(box)
	return frame
}

func (a *App) buildStatusPanel() gtk.Widgetter {
	frame := gtk.NewFrame("Status")
	a.statusBox = gtk.NewBox(gtk.OrientationVertical, 4)
	a.statusBox.SetMarginTop(8)
	a.statusBox.SetMarginBottom(8)
	a.statusBox.SetMarginStart(10)
	a.statusBox.SetMarginEnd(10)

	scrolled := gtk.NewScrolledWindow()
	scrolled.SetChild(a.statusBox)
	scrolled.SetVExpand(true)
	frame.SetChild(scrolled)
	return frame
}

func (a *App) buildActions() gtk.Widgetter {
	frame := gtk.NewFrame("Actions")
	groups := gtk.NewBox(gtk.OrientationHorizontal, 16)
	groups.SetMarginTop(8)
	groups.SetMarginBottom(8)
	groups.SetMarginStart(8)
	groups.SetMarginEnd(8)

	groups.Append(a.actionGroup("Manage", []actionEntry{
		{"deploy", "document-send-symbolic", "_Deploy", "Deploy a flake to the selected device (Ctrl+D)", a.deploy},
		{"update", "software-update-available-symbolic", "_Update", "Update the selected registered device (Ctrl+U)", a.update},
		{"register", "document-new-symbolic", "Re_gister", "Register a flake with the selected device (Ctrl+G)", a.register},
		{"unregister", "edit-delete-symbolic", "U_nregister", "Remove the selected device registration (Ctrl+N)", a.unregister},
		{"publish", "send-to-symbolic", "_Publish", "Publish this device to a new remote (Ctrl+P)", a.publish},
	}))
	groups.Append(a.actionGroup("Connect", []actionEntry{
		{"pair", "emblem-synchronizing-symbolic", "P_air", "Start or confirm pairing with the selected device (Ctrl+A)", a.pair},
		{"share", "dialog-password-symbolic", "Share _key", "Import the administration key from the selected device", a.share},
		{"ssh", "utilities-terminal-symbolic", "_SSH", "Open an SSH session to the selected device (Ctrl+S)", a.ssh},
		{"trust", "security-high-symbolic", "_Trust", "Review pending pairings and trusted devices (Ctrl+T)", a.openTrust},
	}))
	groups.Append(a.actionGroup("System", []actionEntry{
		{"jobs", "view-list-symbolic", "_Jobs", "Show jobs, phases and confirmation deadlines (Ctrl+J)", a.openJobs},
		{"iso", "media-optical-symbolic", "Build _ISO", "Build the deploytarget ISO out-link (Ctrl+I)", a.buildISO},
		{"rotate", "emblem-system-symbolic", "R_otate key", "Rotate the administration key", a.rotate},
		{"reset", "edit-undo-symbolic", "R_eset", "Reset the registration to no-commit", a.reset},
		{"refresh", "view-refresh-symbolic", "_Refresh", "Reload the daemon state (Ctrl+R)", a.refresh},
	}))

	frame.SetChild(groups)
	return frame
}

type actionEntry struct {
	key     string
	icon    string
	label   string
	tooltip string
	handler func()
}

func (a *App) actionGroup(title string, entries []actionEntry) gtk.Widgetter {
	box := gtk.NewBox(gtk.OrientationVertical, 6)
	box.SetHExpand(true)
	heading := gtk.NewLabel(title)
	heading.AddCSSClass("nc-panel-title")
	heading.SetXAlign(0)
	box.Append(heading)

	grid := gtk.NewGrid()
	grid.SetColumnSpacing(6)
	grid.SetRowSpacing(6)
	for index, entry := range entries {
		button := gtk.NewButton()
		button.AddCSSClass("nc-action")
		button.SetHExpand(true)
		button.SetTooltipText(entry.tooltip)
		content := gtk.NewBox(gtk.OrientationHorizontal, 8)
		if entry.icon != "" {
			content.Append(gtk.NewImageFromIconName(entry.icon))
		}
		label := gtk.NewLabel(entry.label)
		label.SetXAlign(0)
		label.SetUseUnderline(true)
		label.SetMnemonicWidget(button)
		content.Append(label)
		button.SetChild(content)
		button.ConnectClicked(entry.handler)
		grid.Attach(button, 0, index, 1, 1)
		a.actions[entry.key] = button
	}
	box.Append(grid)
	return box
}

func (a *App) refresh() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := a.model.Load(ctx); err != nil {
		a.showBanner(err.Error(), true)
		a.headerState.SetText("daemon error")
		a.headerState.RemoveCSSClass("nc-state-ok")
		a.headerState.AddCSSClass("nc-state-error")
	} else {
		a.hideBanner()
		a.headerState.SetText("daemon online")
		a.headerState.RemoveCSSClass("nc-state-error")
		a.headerState.AddCSSClass("nc-state-ok")
	}
	a.refreshHeader()
	a.refreshDevices()
	a.refreshStatus()
	a.updateActions()
}

func (a *App) refreshHeader() {
	host := fallback(a.model.Status.Hostname, "local device")
	if a.model.Status.ShortID != "" {
		host += "  " + a.model.Status.ShortID
	}
	a.headerHost.SetText(host)
}

func (a *App) refreshDevices() {
	a.deviceList.RemoveAll()
	for _, device := range a.model.Devices {
		a.deviceList.Append(a.deviceRow(device))
	}
	if a.model.Selected >= 0 && a.model.Selected < len(a.model.Devices) {
		a.deviceList.SelectRow(a.deviceList.RowAtIndex(a.model.Selected))
	}
}

func (a *App) deviceRow(device httpapi.Device) *gtk.ListBoxRow {
	row := gtk.NewListBoxRow()
	grid := gtk.NewGrid()
	grid.SetColumnSpacing(12)
	grid.SetMarginTop(5)
	grid.SetMarginBottom(5)
	grid.SetMarginStart(6)
	grid.SetMarginEnd(6)

	state := deviceState(device)
	values := []struct {
		text  string
		width int
		class string
	}{
		{fallback(device.Hostname, "-"), 160, ""},
		{deviceShortID(device), 110, "nc-mono"},
		{deviceAddress(device), 120, ""},
		{deviceKind(device), 80, ""},
		{state, 90, stateClass(state)},
	}
	for index, value := range values {
		label := gtk.NewLabel(value.text)
		label.SetXAlign(0)
		label.SetSizeRequest(value.width, -1)
		label.SetEllipsize(pango.EllipsizeEnd)
		if value.class != "" {
			label.AddCSSClass(value.class)
		}
		grid.Attach(label, index, 0, 1, 1)
	}
	row.SetChild(grid)
	row.SetTooltipText(fmt.Sprintf(
		"%s\nID: %s\nAddress: %s\nKind: %s\nRegistered: %t\nPaired: %t",
		fallback(device.Hostname, "-"),
		device.ID,
		deviceAddress(device),
		deviceKind(device),
		device.Registered,
		device.Paired,
	))
	return row
}

func stateClass(state string) string {
	switch state {
	case "paired", "self":
		return "nc-state-ok"
	default:
		return "nc-state-warn"
	}
}

func (a *App) refreshStatus() {
	for child := a.statusBox.FirstChild(); child != nil; child = a.statusBox.FirstChild() {
		a.statusBox.Remove(child)
	}

	device, selected := a.model.SelectedDevice()
	if selected && a.model.Detail {
		local := gtk.NewButtonWithLabel("Local status")
		local.SetTooltipText("Return to the local device summary")
		local.SetHAlign(gtk.AlignStart)
		local.ConnectClicked(func() {
			a.model.Selected = -1
			a.model.Detail = false
			a.deviceList.UnselectAll()
			a.refreshStatus()
			a.updateActions()
		})
		a.statusBox.Append(local)

		section(a.statusBox, "Selected device")
		field(a.statusBox, "Hostname", fallback(device.Hostname, "-"), "")
		field(a.statusBox, "ID", device.ID, "nc-mono")
		field(a.statusBox, "Short ID", deviceShortID(device), "nc-mono")
		field(a.statusBox, "Address", deviceAddress(device), "")
		field(a.statusBox, "Kind", deviceKind(device), "")
		field(a.statusBox, "SSH port", fmt.Sprintf("%d", device.SSHPort), "")
		field(a.statusBox, "Registered", fmt.Sprintf("%t", device.Registered), "")
		field(a.statusBox, "Paired", fmt.Sprintf("%t", device.Paired), "")
		field(a.statusBox, "Last seen", formatTimestamp(device.LastSeen), "")
		field(a.statusBox, "Job", fallback(device.Job, "-"), "")
		return
	}

	section(a.statusBox, "Local device")
	hero := gtk.NewLabel(fallback(a.model.Status.Hostname, "local device") + "  " + a.model.Status.ShortID)
	hero.AddCSSClass("nc-hero")
	hero.SetXAlign(0)
	a.statusBox.Append(hero)

	address := "-"
	kind := "installed"
	if self, ok := a.model.SelfDevice(); ok {
		address = deviceAddress(self)
		kind = deviceKind(self)
	}
	field(a.statusBox, "Address", address, "")
	field(a.statusBox, "Kind", kind, "")
	field(a.statusBox, "Info port", fmt.Sprintf("%d", a.model.Status.InfoPort), "")
	field(a.statusBox, "Control port", fmt.Sprintf("%d", a.model.Status.ControlPort), "")
	field(a.statusBox, "Peers", fmt.Sprintf("%d", a.model.Status.Peers), "")
	field(a.statusBox, "Devices", fmt.Sprintf("%d", len(a.model.Devices)), "")

	section(a.statusBox, "Registration")
	record := a.model.Status.Registration
	if record.Registered() {
		field(a.statusBox, "Repository", record.Repository, "")
		field(a.statusBox, "Host", fallback(record.Host, "-"), "")
		field(a.statusBox, "Branch", fallback(record.Branch, "-"), "")
		field(a.statusBox, "Updates", fmt.Sprintf("%t", record.UpdatesAllowed), "")
		field(a.statusBox, "Mode", string(record.InstallMode), "")
	} else {
		muted(a.statusBox, "Not registered.")
	}

	section(a.statusBox, "Magic rollback")
	rollback := a.model.Status.MagicRollback
	field(a.statusBox, "Enabled", fmt.Sprintf("%t", rollback.Enable), "")
	field(a.statusBox, "Mode", fallback(rollback.Mode, "automatic"), "")
	field(a.statusBox, "Timeouts", fmt.Sprintf("%ds / %ds", rollback.AutomaticTimeout, rollback.ManualTimeout), "")

	section(a.statusBox, "Activity")
	active, latest := a.model.ActiveJobs()
	field(a.statusBox, "Jobs", fmt.Sprintf("%d (%d active)", len(a.model.Jobs), active), "")
	if latest != nil {
		field(a.statusBox, "Latest job", string(latest.State)+" "+string(latest.Kind), jobStateClass(latest.State))
	}
	field(a.statusBox, "Trusted", fmt.Sprintf("%d", len(a.model.Trust)), "")
	field(a.statusBox, "Pending", fmt.Sprintf("%d", len(a.model.Pending)), "")
}

func (a *App) updateActions() {
	for key, button := range a.actions {
		button.SetSensitive(a.model.actionAvailable(key))
	}
}

func (a *App) showBanner(text string, isError bool) {
	a.bannerLabel.SetText(text)
	a.banner.RemoveCSSClass("nc-banner-error")
	a.banner.RemoveCSSClass("nc-banner-info")
	if isError {
		a.banner.AddCSSClass("nc-banner-error")
	} else {
		a.banner.AddCSSClass("nc-banner-info")
	}
	a.banner.SetVisible(true)
}

func (a *App) setMessage(text string) {
	a.showBanner(text, false)
	glib.TimeoutAdd(4000, func() bool {
		a.banner.SetVisible(false)
		return false
	})
}

func (a *App) setError(err error) {
	a.showBanner(err.Error(), true)
}

func (a *App) hideBanner() {
	a.banner.SetVisible(false)
}

func (a *App) selectedDevice() (httpapi.Device, bool) {
	device, ok := a.model.SelectedDevice()
	if !ok {
		a.showBanner("Select a device first.", true)
	}
	return device, ok
}

func (a *App) withTimeout(seconds int) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), time.Duration(seconds)*time.Second)
}

func (a *App) deploy() {
	device, ok := a.selectedDevice()
	if !ok {
		return
	}
	diskRequired := device.Kind == "live"
	a.formDialog("Deploy", "Deploy a flake to "+fallback(device.Hostname, device.ID), []formField{
		{"Flake URL", "", false, true},
		{"Branch", "main", false, true},
		{"Host", "host", false, true},
		{"Disk", "", false, diskRequired},
		{"Account", "", false, false},
		{"Password", "", true, false},
		{"Firmware", "efi", false, false},
	}, func(values []string) error {
		ctx, cancel := a.withTimeout(30)
		defer cancel()
		job, err := a.client.Deploy(ctx, device.ID, httpapi.DeployRequest{
			Repository: values[0],
			Branch:     values[1],
			Host:       values[2],
			Disk:       values[3],
			Account:    values[4],
			Password:   values[5],
			Firmware:   values[6],
		})
		if err != nil {
			return err
		}
		a.refresh()
		a.setMessage("job " + job)
		return nil
	})
}

func (a *App) update() {
	device, ok := a.selectedDevice()
	if !ok {
		return
	}
	a.formDialog("Update", "Update "+fallback(device.Hostname, device.ID)+" from its registered flake", []formField{
		{"Password", "", true, false},
	}, func(values []string) error {
		ctx, cancel := a.withTimeout(30)
		defer cancel()
		job, err := a.client.Update(ctx, device.ID, httpapi.UpdateRequest{Password: values[0]})
		if err != nil {
			return err
		}
		a.refresh()
		a.setMessage("job " + job)
		return nil
	})
}

func (a *App) register() {
	device, ok := a.selectedDevice()
	if !ok {
		return
	}
	a.formDialog("Register", "Register a flake with "+fallback(device.Hostname, device.ID), []formField{
		{"Flake URL", "", false, true},
		{"Branch", "main", false, true},
		{"Host", "host", false, true},
		{"Account", "", false, false},
		{"Password", "", true, false},
	}, func(values []string) error {
		ctx, cancel := a.withTimeout(30)
		defer cancel()
		err := a.client.Register(ctx, device.ID, httpapi.RegisterRequest{
			Repository: values[0],
			Branch:     values[1],
			Host:       values[2],
			Account:    values[3],
			Password:   values[4],
		})
		if err != nil {
			return err
		}
		a.refresh()
		a.setMessage("registration accepted")
		return nil
	})
}

func (a *App) unregister() {
	device, ok := a.selectedDevice()
	if !ok {
		return
	}
	a.formDialog("Unregister", "Remove the registration from "+fallback(device.Hostname, device.ID), []formField{
		{"Password", "", true, false},
	}, func(values []string) error {
		ctx, cancel := a.withTimeout(30)
		defer cancel()
		if err := a.client.Unregister(ctx, device.ID, values[0]); err != nil {
			return err
		}
		a.refresh()
		a.setMessage("registration removed")
		return nil
	})
}

func (a *App) publish() {
	a.formDialog("Publish", "Publish this no-commit device to a new remote", []formField{
		{"New remote", "", false, true},
		{"Branch", "main", false, true},
		{"Password", "", true, false},
	}, func(values []string) error {
		ctx, cancel := a.withTimeout(30)
		defer cancel()
		job, err := a.client.Publish(ctx, httpapi.PublishRequest{
			RemoteURL: values[0],
			Branch:    values[1],
			Password:  values[2],
		})
		if err != nil {
			return err
		}
		a.refresh()
		a.setMessage("job " + job)
		return nil
	})
}

func (a *App) ssh() {
	device, ok := a.selectedDevice()
	if !ok {
		return
	}
	if err := openTerminal("nixconnect", "ssh", device.ID); err != nil {
		a.setError(err)
		return
	}
	a.setMessage("SSH opened for " + fallback(device.Hostname, device.ID))
}

func (a *App) pair() {
	device, ok := a.selectedDevice()
	if !ok {
		return
	}
	ctx, cancel := a.withTimeout(10)
	defer cancel()
	if a.pairResponse != nil && a.pairTarget == device.ID {
		if err := a.client.PairConfirm(ctx, httpapi.PairConfirmRequest{ID: a.pairResponse.ID, Code: a.pairResponse.Code}); err != nil {
			a.setError(err)
			return
		}
		a.pairResponse = nil
		a.pairTarget = ""
		a.refresh()
		a.setMessage("pairing confirmed with " + fallback(device.Hostname, device.ID))
		return
	}
	response, err := a.client.PairStart(ctx, httpapi.PairRequest{PeerID: device.ID})
	if err != nil {
		a.setError(err)
		return
	}
	a.pairResponse = &response
	a.pairTarget = device.ID
	a.messageDialog("Pairing", fmt.Sprintf("Code %s for %s.\n\nConfirm on the target, then choose Pair again.", response.Code, fallback(device.Hostname, device.ID)))
}

func (a *App) share() {
	device, ok := a.selectedDevice()
	if !ok {
		return
	}
	ctx, cancel := a.withTimeout(10)
	defer cancel()
	current, err := a.client.Status(ctx)
	if err != nil {
		a.setError(err)
		return
	}
	code := pairing.Code(current.ID, device.ID)
	a.confirmDialog("Share administration key", "Key sharing code "+code+".\n\nImport the administration key from "+fallback(device.Hostname, device.ID)+"? The local key is replaced and existing ISOs become invalid.", func() {
		ctx, cancel := a.withTimeout(30)
		defer cancel()
		publicKey, err := a.client.ImportSharedKey(ctx, device.ID)
		if err != nil {
			a.setError(err)
			return
		}
		a.refresh()
		a.setMessage("administration key imported: " + publicKey)
	})
}

func (a *App) rotate() {
	a.confirmDialog("Rotate administration key", "Rotate the administration key? Existing deploytarget ISOs become invalid and every target must be redeployed.", func() {
		ctx, cancel := a.withTimeout(30)
		defer cancel()
		publicKey, err := a.client.RotateAdminKey(ctx)
		if err != nil {
			a.setError(err)
			return
		}
		a.refresh()
		a.setMessage("administration key rotated: " + publicKey)
	})
}

func (a *App) reset() {
	a.confirmDialog("Reset registration", "Reset the registration to no-commit with updates disabled?", func() {
		ctx, cancel := a.withTimeout(30)
		defer cancel()
		if err := a.client.ResetRegistration(ctx); err != nil {
			a.setError(err)
			return
		}
		a.refresh()
		a.setMessage("registration reset to no-commit")
	})
}

func (a *App) buildISO() {
	a.formDialog("Build deploytarget ISO", "The out-link points to a directory containing iso/<image>.iso. The daemon does not copy the image.", []formField{
		{"Output link", "deploy-target-iso", false, true},
	}, func(values []string) error {
		output := strings.TrimSpace(values[0])
		if output == "" {
			output = "deploy-target-iso"
		}
		destination, err := filepath.Abs(output)
		if err != nil {
			return err
		}
		ctx, cancel := a.withTimeout(10)
		defer cancel()
		job, err := a.client.BuildISO(ctx, httpapi.ISORequest{Output: destination})
		if err != nil {
			return err
		}
		a.setMessage("ISO job " + job + " -> " + destination)
		return nil
	})
}

func section(box *gtk.Box, text string) {
	label := gtk.NewLabel(text)
	label.AddCSSClass("nc-panel-title")
	label.SetXAlign(0)
	label.SetMarginTop(10)
	box.Append(label)
}

func field(box *gtk.Box, label, value, class string) {
	row := gtk.NewBox(gtk.OrientationHorizontal, 8)
	name := gtk.NewLabel(label)
	name.AddCSSClass("nc-muted")
	name.SetXAlign(0)
	name.SetSizeRequest(120, -1)
	content := gtk.NewLabel(value)
	content.SetXAlign(0)
	content.SetSelectable(true)
	content.SetWrap(true)
	content.SetHExpand(true)
	if class != "" {
		content.AddCSSClass(class)
	}
	row.Append(name)
	row.Append(content)
	box.Append(row)
}

func muted(box *gtk.Box, text string) {
	label := gtk.NewLabel(text)
	label.AddCSSClass("nc-muted")
	label.SetXAlign(0)
	box.Append(label)
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
