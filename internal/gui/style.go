package gui

import (
	gdk "github.com/diamondburned/gotk4/pkg/gdk/v4"
	gtk "github.com/diamondburned/gotk4/pkg/gtk/v4"
)

const styleSheet = `
.nc-mono { font-family: monospace; }
.nc-muted { opacity: 0.65; }
.nc-hero { font-size: 1.35em; font-weight: 700; }
.nc-title { font-weight: 700; }
.nc-table-header { font-weight: 700; opacity: 0.7; }
.nc-panel-title { font-weight: 700; }
.nc-state-ok { color: #26a269; font-weight: 600; }
.nc-state-warn { color: #c64600; font-weight: 600; }
.nc-state-error { color: #c01c28; font-weight: 600; }
.nc-banner { padding: 8px 12px; border-radius: 6px; }
.nc-banner-error { background-color: rgba(192, 28, 40, 0.16); }
.nc-banner-info { background-color: rgba(53, 132, 228, 0.14); }
.nc-field-label { font-weight: 600; }
.nc-action { padding: 6px 10px; }
.nc-card { padding: 8px; }
`

func applyStyle() {
	display := gdk.DisplayGetDefault()
	if display == nil {
		return
	}
	provider := gtk.NewCSSProvider()
	provider.LoadFromData(styleSheet)
	gtk.StyleContextAddProviderForDisplay(display, provider, gtk.STYLE_PROVIDER_PRIORITY_APPLICATION)
}
