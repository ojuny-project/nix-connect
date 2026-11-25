package gui

import (
	"fmt"
	"strings"

	"github.com/diamondburned/gotk4/pkg/glib/v2"
	gtk "github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"

	"github.com/ojuny-project/nix-connect/internal/httpapi"
	"github.com/ojuny-project/nix-connect/internal/jobs"
	"github.com/ojuny-project/nix-connect/internal/pairing"
	"github.com/ojuny-project/nix-connect/internal/trust"
)

type formField struct {
	label        string
	defaultValue string
	password     bool
	required     bool
}

func (a *App) formDialog(title, description string, fields []formField, submit func([]string) error) {
	dialog := gtk.NewDialog()
	dialog.SetTitle(title)
	dialog.SetTransientFor(&a.window.Window)
	dialog.SetModal(true)
	dialog.SetDefaultSize(560, -1)
	dialog.AddButton("Cancel", int(gtk.ResponseCancel))
	dialog.AddButton("Run", int(gtk.ResponseOK))

	content := dialog.ContentArea()
	content.SetSpacing(8)
	if description != "" {
		label := gtk.NewLabel(description)
		label.SetXAlign(0)
		label.SetWrap(true)
		content.Append(label)
	}

	entries := make([]*gtk.Entry, len(fields))
	for index, field := range fields {
		box := gtk.NewBox(gtk.OrientationVertical, 3)
		label := gtk.NewLabel(field.label)
		label.AddCSSClass("nc-field-label")
		label.SetXAlign(0)
		box.Append(label)
		entry := gtk.NewEntry()
		entry.SetHExpand(true)
		entry.SetTooltipText(field.label)
		if field.password {
			entry.SetVisibility(false)
		}
		if field.defaultValue != "" {
			entry.Buffer().SetText(field.defaultValue, -1)
		}
		box.Append(entry)
		content.Append(box)
		entries[index] = entry
	}

	errorLabel := gtk.NewLabel("")
	errorLabel.AddCSSClass("nc-state-error")
	errorLabel.SetXAlign(0)
	errorLabel.SetWrap(true)
	errorLabel.SetVisible(false)
	content.Append(errorLabel)

	dialog.ConnectResponse(func(response int) {
		if response != int(gtk.ResponseOK) {
			dialog.Close()
			return
		}
		values := make([]string, len(entries))
		for index, entry := range entries {
			values[index] = entry.Buffer().Text()
		}
		for index, field := range fields {
			if field.required && strings.TrimSpace(values[index]) == "" {
				errorLabel.SetText(field.label + " is required")
				errorLabel.SetVisible(true)
				return
			}
		}
		if err := submit(values); err != nil {
			errorLabel.SetText(err.Error())
			errorLabel.SetVisible(true)
			return
		}
		dialog.Close()
	})

	if len(entries) > 0 {
		entries[len(entries)-1].ConnectActivate(func() {
			dialog.Response(int(gtk.ResponseOK))
		})
	}
	dialog.Present()
}

func (a *App) confirmDialog(title, text string, action func()) {
	dialog := gtk.NewDialog()
	dialog.SetTitle(title)
	dialog.SetTransientFor(&a.window.Window)
	dialog.SetModal(true)
	dialog.AddButton("Cancel", int(gtk.ResponseCancel))
	dialog.AddButton("Continue", int(gtk.ResponseOK))

	label := gtk.NewLabel(text)
	label.SetXAlign(0)
	label.SetWrap(true)
	dialog.ContentArea().Append(label)

	dialog.ConnectResponse(func(response int) {
		dialog.Close()
		if response == int(gtk.ResponseOK) {
			action()
		}
	})
	dialog.Present()
}

func (a *App) messageDialog(title, text string) {
	dialog := gtk.NewDialog()
	dialog.SetTitle(title)
	dialog.SetTransientFor(&a.window.Window)
	dialog.SetModal(true)
	dialog.AddButton("Close", int(gtk.ResponseClose))

	label := gtk.NewLabel(text)
	label.SetXAlign(0)
	label.SetWrap(true)
	dialog.ContentArea().Append(label)

	dialog.ConnectResponse(func(int) { dialog.Close() })
	dialog.Present()
}

func (a *App) openJobs() {
	window := gtk.NewWindow()
	window.SetTitle("Jobs")
	window.SetTransientFor(&a.window.Window)
	window.SetModal(true)
	window.SetDefaultSize(1000, 620)

	root := gtk.NewBox(gtk.OrientationVertical, 8)
	root.SetMarginTop(12)
	root.SetMarginBottom(12)
	root.SetMarginStart(12)
	root.SetMarginEnd(12)

	summary := gtk.NewLabel("")
	summary.SetXAlign(0)
	root.Append(summary)

	header := gtk.NewGrid()
	header.SetColumnSpacing(12)
	header.SetColumnHomogeneous(true)
	columns := []string{"ID", "KIND", "TARGET", "STATE", "UPDATED"}
	for index, column := range columns {
		label := gtk.NewLabel(column)
		label.AddCSSClass("nc-table-header")
		label.SetXAlign(0)
		label.SetHExpand(true)
		header.Attach(label, index, 0, 1, 1)
	}
	root.Append(header)
	root.Append(gtk.NewSeparator(gtk.OrientationHorizontal))

	list := gtk.NewListBox()
	list.SetSelectionMode(gtk.SelectionSingle)
	scrolled := gtk.NewScrolledWindow()
	scrolled.SetChild(list)
	scrolled.SetPolicy(gtk.PolicyAutomatic, gtk.PolicyAutomatic)
	scrolled.SetPropagateNaturalWidth(true)
	scrolled.SetVExpand(true)
	root.Append(scrolled)

	detail := gtk.NewBox(gtk.OrientationVertical, 3)
	detail.SetMarginTop(6)
	root.Append(detail)

	buttons := gtk.NewBox(gtk.OrientationHorizontal, 6)
	confirm := gtk.NewButtonWithLabel("Confirm selected")
	confirm.SetTooltipText("Confirm the selected magic rollback job")
	cancel := gtk.NewButtonWithLabel("Cancel selected")
	cancel.SetTooltipText("Cancel the selected job")
	reload := gtk.NewButtonWithLabel("Refresh")
	reload.SetTooltipText("Reload the job list")
	closeButton := gtk.NewButtonWithLabel("Close")
	buttons.Append(confirm)
	buttons.Append(cancel)
	buttons.Append(reload)
	buttons.Append(closeButton)
	root.Append(buttons)

	window.SetChild(root)

	var items []jobs.Job
	selected := -1
	showDetail := func() {
		clearBox(detail)
		if selected < 0 || selected >= len(items) {
			return
		}
		job := items[selected]
		field(detail, "Kind", string(job.Kind), "")
		field(detail, "Target", job.Target, "nc-mono")
		field(detail, "Phase", fallback(job.Phase, "-"), "")
		field(detail, "State", string(job.State), jobStateClass(job.State))
		if job.Error != "" {
			field(detail, "Error", job.Error, "nc-state-error")
		}
		if !job.ConfirmDeadline.IsZero() {
			field(detail, "Confirm by", formatTimestamp(job.ConfirmDeadline), "nc-state-warn")
		}
		field(detail, "Revision", fallback(job.Revision, "-"), "nc-mono")
		field(detail, "Owner", job.Owner, "nc-mono")
		field(detail, "Updated", formatTimestamp(job.Updated), "")
	}
	refresh := func() {
		ctx, cancelContext := a.withTimeout(5)
		loaded, err := a.client.Jobs(ctx)
		cancelContext()
		if err != nil {
			summary.SetText(err.Error())
			return
		}
		items = loaded
		active, _ := (&Model{Jobs: items}).ActiveJobs()
		summary.SetText(fmt.Sprintf("%d jobs, %d active", len(items), active))
		previous := selected
		list.RemoveAll()
		for _, job := range items {
			list.Append(jobRow(job))
		}
		if previous >= 0 && previous < len(items) {
			selected = previous
			list.SelectRow(list.RowAtIndex(previous))
		} else {
			selected = -1
			showDetail()
		}
	}
	list.ConnectRowSelected(func(row *gtk.ListBoxRow) {
		if row == nil {
			selected = -1
			showDetail()
			return
		}
		selected = row.Index()
		showDetail()
	})

	confirm.ConnectClicked(func() {
		if selected < 0 || selected >= len(items) {
			summary.SetText("Select a job first.")
			return
		}
		ctx, cancelContext := a.withTimeout(30)
		defer cancelContext()
		if err := a.client.Confirm(ctx, items[selected].ID); err != nil {
			summary.SetText(err.Error())
			return
		}
		a.setMessage("job confirmed: " + items[selected].ID)
		refresh()
	})
	cancel.ConnectClicked(func() {
		if selected < 0 || selected >= len(items) {
			summary.SetText("Select a job first.")
			return
		}
		ctx, cancelContext := a.withTimeout(30)
		defer cancelContext()
		if err := a.client.Cancel(ctx, items[selected].ID); err != nil {
			summary.SetText(err.Error())
			return
		}
		a.setMessage("job cancelled: " + items[selected].ID)
		refresh()
	})
	reload.ConnectClicked(refresh)
	closeButton.ConnectClicked(func() { window.Close() })

	refresh()
	glib.TimeoutAdd(2000, func() bool {
		if !window.Visible() {
			return false
		}
		refresh()
		return true
	})
	window.Present()
}

func jobRow(job jobs.Job) *gtk.ListBoxRow {
	row := gtk.NewListBoxRow()
	grid := gtk.NewGrid()
	grid.SetColumnSpacing(12)
	grid.SetColumnHomogeneous(true)
	grid.SetMarginTop(5)
	grid.SetMarginBottom(5)
	grid.SetMarginStart(6)
	grid.SetMarginEnd(6)
	values := []struct {
		text  string
		class string
	}{
		{job.ID, "nc-mono"},
		{string(job.Kind), ""},
		{fallback(job.Target, "-"), ""},
		{string(job.State), jobStateClass(job.State)},
		{job.Updated.Local().Format("15:04:05"), ""},
	}
	for index, value := range values {
		label := gtk.NewLabel(value.text)
		label.SetXAlign(0)
		label.SetHExpand(true)
		label.SetEllipsize(pango.EllipsizeEnd)
		if value.class != "" {
			label.AddCSSClass(value.class)
		}
		grid.Attach(label, index, 0, 1, 1)
	}
	row.SetChild(grid)
	row.SetTooltipText(job.ID + "\n" + string(job.Kind) + " " + string(job.State) + "\n" + fallback(job.Phase, "-"))
	return row
}

func (a *App) openTrust() {
	window := gtk.NewWindow()
	window.SetTitle("Pairing and trust")
	window.SetTransientFor(&a.window.Window)
	window.SetModal(true)
	window.SetDefaultSize(980, 620)

	root := gtk.NewBox(gtk.OrientationVertical, 8)
	root.SetMarginTop(12)
	root.SetMarginBottom(12)
	root.SetMarginStart(12)
	root.SetMarginEnd(12)

	summary := gtk.NewLabel("")
	summary.SetXAlign(0)
	root.Append(summary)

	pendingTitle := gtk.NewLabel("Pending pairings")
	pendingTitle.AddCSSClass("nc-panel-title")
	pendingTitle.SetXAlign(0)
	root.Append(pendingTitle)

	pendingHeader := gtk.NewGrid()
	pendingHeader.SetColumnSpacing(12)
	pendingHeader.SetColumnHomogeneous(true)
	for index, column := range []string{"PAIRING", "PEER", "CODE", "EXPIRES"} {
		label := gtk.NewLabel(column)
		label.AddCSSClass("nc-table-header")
		label.SetXAlign(0)
		label.SetHExpand(true)
		pendingHeader.Attach(label, index, 0, 1, 1)
	}
	root.Append(pendingHeader)

	pendingList := gtk.NewListBox()
	pendingList.SetSelectionMode(gtk.SelectionSingle)
	pendingScrolled := gtk.NewScrolledWindow()
	pendingScrolled.SetChild(pendingList)
	pendingScrolled.SetPolicy(gtk.PolicyAutomatic, gtk.PolicyAutomatic)
	pendingScrolled.SetPropagateNaturalWidth(true)
	pendingScrolled.SetVExpand(true)
	root.Append(pendingScrolled)

	trustTitle := gtk.NewLabel("Trusted devices")
	trustTitle.AddCSSClass("nc-panel-title")
	trustTitle.SetXAlign(0)
	root.Append(trustTitle)

	trustHeader := gtk.NewGrid()
	trustHeader.SetColumnSpacing(12)
	trustHeader.SetColumnHomogeneous(true)
	for index, column := range []string{"FINGERPRINT", "HOST", "ADDRESS", "PAIRED"} {
		label := gtk.NewLabel(column)
		label.AddCSSClass("nc-table-header")
		label.SetXAlign(0)
		label.SetHExpand(true)
		trustHeader.Attach(label, index, 0, 1, 1)
	}
	root.Append(trustHeader)

	trustList := gtk.NewListBox()
	trustList.SetSelectionMode(gtk.SelectionSingle)
	trustScrolled := gtk.NewScrolledWindow()
	trustScrolled.SetChild(trustList)
	trustScrolled.SetPolicy(gtk.PolicyAutomatic, gtk.PolicyAutomatic)
	trustScrolled.SetPropagateNaturalWidth(true)
	trustScrolled.SetVExpand(true)
	root.Append(trustScrolled)

	buttons := gtk.NewBox(gtk.OrientationHorizontal, 6)
	confirmPending := gtk.NewButtonWithLabel("Confirm selected pairing")
	confirmPending.SetTooltipText("Confirm the selected pending pairing")
	revoke := gtk.NewButtonWithLabel("Revoke selected trust")
	revoke.SetTooltipText("Remove the selected trust record on both sides")
	reload := gtk.NewButtonWithLabel("Refresh")
	reload.SetTooltipText("Reload pending pairings and trust records")
	closeButton := gtk.NewButtonWithLabel("Close")
	buttons.Append(confirmPending)
	buttons.Append(revoke)
	buttons.Append(reload)
	buttons.Append(closeButton)
	root.Append(buttons)

	window.SetChild(root)

	var pending []pairing.Request
	var records []trust.Record
	refresh := func() {
		ctx, cancelContext := a.withTimeout(5)
		loadedPending, err := a.client.PairPending(ctx)
		if err != nil {
			summary.SetText(err.Error())
			cancelContext()
			return
		}
		loadedRecords, err := a.client.TrustRecords(ctx)
		cancelContext()
		if err != nil {
			summary.SetText(err.Error())
			return
		}
		pending = loadedPending
		records = loadedRecords
		summary.SetText(fmt.Sprintf("%d pending, %d paired", len(pending), len(records)))
		pendingList.RemoveAll()
		for _, request := range pending {
			pendingList.Append(pendingRow(request))
		}
		trustList.RemoveAll()
		for _, record := range records {
			trustList.Append(trustRow(record))
		}
	}
	confirmPending.ConnectClicked(func() {
		row := pendingList.SelectedRow()
		if row == nil {
			summary.SetText("Select a pending pairing first.")
			return
		}
		index := row.Index()
		if index < 0 || index >= len(pending) {
			return
		}
		request := pending[index]
		ctx, cancelContext := a.withTimeout(30)
		defer cancelContext()
		if err := a.client.PairConfirm(ctx, httpapi.PairConfirmRequest{ID: request.ID, Code: request.Code}); err != nil {
			summary.SetText(err.Error())
			return
		}
		a.setMessage("pairing confirmed with " + request.PeerID)
		refresh()
	})
	revoke.ConnectClicked(func() {
		row := trustList.SelectedRow()
		if row == nil {
			summary.SetText("Select a trusted device first.")
			return
		}
		index := row.Index()
		if index < 0 || index >= len(records) {
			return
		}
		fingerprint := records[index].Fingerprint
		a.confirmDialog("Revoke trust", "Revoke the trust record for "+fingerprint+"?", func() {
			ctx, cancelContext := a.withTimeout(30)
			defer cancelContext()
			if err := a.client.RevokeTrust(ctx, fingerprint); err != nil {
				a.setError(err)
				return
			}
			a.setMessage("trust record revoked: " + fingerprint)
			refresh()
		})
	})
	reload.ConnectClicked(refresh)
	closeButton.ConnectClicked(func() { window.Close() })

	refresh()
	glib.TimeoutAdd(3000, func() bool {
		if !window.Visible() {
			return false
		}
		refresh()
		return true
	})
	window.Present()
}

func pendingRow(request pairing.Request) *gtk.ListBoxRow {
	row := gtk.NewListBoxRow()
	grid := gtk.NewGrid()
	grid.SetColumnSpacing(12)
	grid.SetColumnHomogeneous(true)
	grid.SetMarginTop(5)
	grid.SetMarginBottom(5)
	grid.SetMarginStart(6)
	grid.SetMarginEnd(6)
	values := []struct {
		text  string
		class string
	}{
		{request.ID, "nc-mono"},
		{request.PeerID, "nc-mono"},
		{request.Code, "nc-state-warn"},
		{request.Expires.Local().Format("15:04"), ""},
	}
	for index, value := range values {
		label := gtk.NewLabel(value.text)
		label.SetXAlign(0)
		label.SetHExpand(true)
		label.SetEllipsize(pango.EllipsizeEnd)
		if value.class != "" {
			label.AddCSSClass(value.class)
		}
		grid.Attach(label, index, 0, 1, 1)
	}
	row.SetChild(grid)
	row.SetTooltipText("Pairing " + request.ID + "\nPeer " + request.PeerID + "\nCode " + request.Code)
	return row
}

func trustRow(record trust.Record) *gtk.ListBoxRow {
	row := gtk.NewListBoxRow()
	grid := gtk.NewGrid()
	grid.SetColumnSpacing(12)
	grid.SetColumnHomogeneous(true)
	grid.SetMarginTop(5)
	grid.SetMarginBottom(5)
	grid.SetMarginStart(6)
	grid.SetMarginEnd(6)
	values := []struct {
		text  string
		class string
	}{
		{record.Fingerprint, "nc-mono"},
		{fallback(record.Hostname, "-"), ""},
		{fallback(record.Address, "-"), ""},
		{record.PairedAt.Local().Format("2006-01-02 15:04"), ""},
	}
	for index, value := range values {
		label := gtk.NewLabel(value.text)
		label.SetXAlign(0)
		label.SetHExpand(true)
		label.SetEllipsize(pango.EllipsizeEnd)
		if value.class != "" {
			label.AddCSSClass(value.class)
		}
		grid.Attach(label, index, 0, 1, 1)
	}
	row.SetChild(grid)
	row.SetTooltipText(record.Fingerprint + "\n" + fallback(record.Hostname, "-") + "\n" + fallback(record.Address, "-"))
	return row
}

func clearBox(box *gtk.Box) {
	for child := box.FirstChild(); child != nil; child = box.FirstChild() {
		box.Remove(child)
	}
}
