package block

import (
	"github.com/diamondburned/chatkit/md"
	"github.com/diamondburned/chatkit/md/hl"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"
	"github.com/diamondburned/gotkit/app/prefs"
	"github.com/diamondburned/gotkit/gtkutil/cssutil"
)

// CodeBlock is a widget containing a block of code.
type CodeBlock struct {
	*gtk.Overlay
	state  *ContainerState
	scroll *gtk.ScrolledWindow
	lang   *gtk.Label
	text   *TextBlock
}

var (
	_ WidgetBlock     = (*CodeBlock)(nil)
	_ TextWidgetBlock = (*CodeBlock)(nil)
)

var CodeBlockCSS = cssutil.Applier("md-codeblock", `
	.md-codeblock scrollbar {
		background: none;
		border:     none;
	}
	.md-codeblock:active scrollbar {
		opacity: 0.2;
	}
	.md-codeblock:not(.md-codeblock-expanded) scrollbar {
		opacity: 0;
	}
	.md-codeblock-text {
		font-family: monospace;
		padding: 4px 6px;
		padding-bottom: 0px; /* bottom-margin */
	}
	.md-codeblock-actions > *:not(label) {
		background-color: @theme_bg_color;
		margin-top:    4px;
		margin-right:  4px;
		margin-bottom: 4px;
	}
	.md-codeblock-language {
		font-family: monospace;
		font-size: 0.9em;
		margin: 0px 6px;
		color: mix(@theme_bg_color, @theme_fg_color, 0.85);
	}
	/*
	 * ease-in-out-gradient -steps 5 -min 0.2 -max 0 
	 * ease-in-out-gradient -steps 5 -min 0 -max 75 -f $'%.2fpx\n'
	 */
	.md-codeblock-voverflow .md-codeblock-cover {
		background-image: linear-gradient(
			to top,
			alpha(@theme_bg_color, 0.25) 0.00px,
			alpha(@theme_bg_color, 0.24) 2.40px,
			alpha(@theme_bg_color, 0.19) 19.20px,
			alpha(@theme_bg_color, 0.06) 55.80px,
			alpha(@theme_bg_color, 0.01) 72.60px
		);
	}
`)

var codeLowerHeight = prefs.NewInt(200, prefs.IntMeta{
	Name:    "Collapsed Codeblock Height",
	Section: "Text",
	Description: "The height of a collapsed codeblock." +
		" Long snippets of code will appear cropped.",
	Min: 50,
	Max: 5000,
})

var codeUpperHeight = prefs.NewInt(400, prefs.IntMeta{
	Name:    "Expanded Codeblock Height",
	Section: "Text",
	Description: "The height of an expanded codeblock." +
		" Codeblocks are either shorter than this or as tall." +
		" Ignored if this is lower than the collapsed height.",
	Min: 50,
	Max: 5000,
})

func init() { prefs.Order(codeLowerHeight, codeUpperHeight) }

// NewCodeBlock creates a new CodeBlock.
func NewCodeBlock(state *ContainerState) *CodeBlock {
	text := NewTextBlock(state)
	text.AddCSSClass("md-codeblock-text")
	text.SetWrapMode(gtk.WrapNone)
	text.SetVScrollPolicy(gtk.ScrollMinimum)
	text.SetBottomMargin(18)

	sw := gtk.NewScrolledWindow()
	sw.SetPolicy(gtk.PolicyAutomatic, gtk.PolicyAutomatic)
	sw.SetPropagateNaturalHeight(true)
	sw.SetChild(text)

	language := gtk.NewLabel("")
	language.AddCSSClass("md-codeblock-language")
	language.SetHExpand(true)
	language.SetEllipsize(pango.EllipsizeEnd)
	language.SetSingleLineMode(true)
	language.SetXAlign(0)
	language.SetVAlign(gtk.AlignCenter)

	wrap := gtk.NewToggleButton()
	wrap.SetIconName("format-justify-left-symbolic")
	wrap.SetTooltipText("Toggle Word Wrapping")

	copy := gtk.NewButtonFromIconName("edit-copy-symbolic")
	copy.SetTooltipText("Copy All")
	copy.ConnectClicked(func() {
		popover := gtk.NewPopover()
		popover.SetCanTarget(false)
		popover.SetAutohide(false)
		popover.SetChild(gtk.NewLabel("Copied!"))
		popover.SetPosition(gtk.PosLeft)
		popover.SetParent(copy)

		start, end := text.Buffer.Bounds()
		text := text.Buffer.Text(start, end, false)

		clipboard := gdk.DisplayGetDefault().Clipboard()
		clipboard.SetText(text)

		popover.Popup()
		glib.TimeoutSecondsAdd(3, func() {
			popover.Popdown()
			popover.Unparent()
		})
	})

	expand := gtk.NewToggleButton()
	expand.SetTooltipText("Toggle Reveal Code")

	actions := gtk.NewBox(gtk.OrientationHorizontal, 0)
	actions.AddCSSClass("md-codeblock-actions")
	actions.SetHAlign(gtk.AlignFill)
	actions.SetVAlign(gtk.AlignStart)
	actions.Append(language)
	actions.Append(wrap)
	actions.Append(copy)
	actions.Append(expand)

	clickOverlay := gtk.NewBox(gtk.OrientationVertical, 0)
	clickOverlay.Append(sw)
	// Clicking on the codeblock will click the button for us, but only if it's
	// collapsed.
	click := gtk.NewGestureClick()
	click.SetButton(gdk.BUTTON_PRIMARY)
	click.SetExclusive(true)
	click.ConnectPressed(func(n int, x, y float64) {
		// TODO: don't handle this on a touchscreen.
		if !expand.Active() {
			expand.Activate()
		}
	})
	clickOverlay.AddController(click)

	overlay := gtk.NewOverlay()
	overlay.SetOverflow(gtk.OverflowHidden)
	overlay.SetChild(clickOverlay)
	overlay.AddOverlay(actions)
	overlay.SetMeasureOverlay(actions, true)
	overlay.AddCSSClass("frame")
	CodeBlockCSS(overlay)

	// Lazily initialized in notify::upper below.
	var cover *gtk.Box
	coverSetVisible := func(visible bool) {
		if cover != nil {
			cover.SetVisible(visible)
		}
	}

	// Manually keep track of the expanded height, since SetMaxContentHeight
	// doesn't work (below issue).
	var maxHeight int
	var minHeight int

	vadj := text.VAdjustment()

	toggleExpand := func() {
		if expand.Active() {
			overlay.AddCSSClass("md-codeblock-expanded")
			expand.SetIconName("view-restore-symbolic")
			sw.SetCanTarget(true)
			sw.SetSizeRequest(-1, maxHeight)
			sw.SetMarginTop(actions.AllocatedHeight())
			language.SetOpacity(1)
			coverSetVisible(false)
		} else {
			overlay.RemoveCSSClass("md-codeblock-expanded")
			expand.SetIconName("view-fullscreen-symbolic")
			sw.SetCanTarget(false)
			sw.SetSizeRequest(-1, minHeight)
			sw.SetMarginTop(0)
			language.SetOpacity(0)
			coverSetVisible(true)
			// Restore scrolling when uncollapsed.
			vadj.SetValue(0)
		}
	}
	expand.ConnectClicked(toggleExpand)

	// Workaround for issue https://gitlab.gnome.org/GNOME/gtk/-/issues/3515.
	vadj.NotifyProperty("upper", func() {
		upperHeight := codeUpperHeight.Value()
		lowerHeight := codeLowerHeight.Value()
		if upperHeight < lowerHeight {
			upperHeight = lowerHeight
		}

		upper := int(vadj.Upper())
		maxHeight = upper
		minHeight = upper

		if maxHeight > upperHeight {
			maxHeight = upperHeight
		}

		if minHeight > lowerHeight {
			minHeight = lowerHeight
			overlay.AddCSSClass("md-codeblock-voverflow")

			if cover == nil {
				// Use a fading gradient to let the user know (visually) that
				// there's still more code hidden. This isn't very accessible.
				cover = gtk.NewBox(gtk.OrientationHorizontal, 0)
				cover.AddCSSClass("md-codeblock-cover")
				cover.SetCanTarget(false)
				cover.SetVAlign(gtk.AlignFill)
				cover.SetHAlign(gtk.AlignFill)
				overlay.AddOverlay(cover)
			}
		}

		// Quite expensive when it's put here, but it's safer.
		toggleExpand()
	})

	wrap.ConnectClicked(func() {
		if wrap.Active() {
			text.SetWrapMode(gtk.WrapWordChar)
		} else {
			// TODO: this doesn't shrink back, which is weird.
			text.SetWrapMode(gtk.WrapNone)
		}
	})

	return &CodeBlock{
		Overlay: overlay,
		state:   state,
		scroll:  sw,
		lang:    language,
		text:    text,
	}
}

// TextBlock returns this CodeBlock's internal TextBlock containing all the
// code.
func (b *CodeBlock) TextBlock() *TextBlock { return b.text }

// Highlight highlights the whole codeblock by the given language. Calling this
// method will always add the _nohyphens tag. If language is empty, then no
// highlighting is actually done.
func (b *CodeBlock) Highlight(language string) {
	start := b.text.Buffer.StartIter()
	end := b.text.Iter

	// Don't add any hyphens.
	noHyphens := md.Tags.FromTable(b.state.TagTable(), "_nohyphens")
	b.text.Buffer.ApplyTag(noHyphens, start, end)

	if language != "" {
		hl.Highlight(b.state.Context(), start, end, language)
	}
}