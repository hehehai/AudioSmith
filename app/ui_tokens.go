package main

// Design tokens (0.5 overhaul, plan/ui-overhaul-plan.md §4, after the
// ElevenLabs study research/elevenlabs-ui/README.md §3): monochrome — a
// neutral grey scale from white to near-black, white controls inside a
// 1 px hairline, near-black ink for the one primary action of a view (and
// switches, sliders, active filters) — one accent, teal, for state only
// (selection, progress, focus rings, links, what plays), and a red-orange
// for recording and destructive actions. applyTheme puts them into MyGo's
// Theme, so that MyGo's widgets follow them; views read the rest with
// tok(t).

import "github.com/egoist/mygo/ui"

// Radii, DIPs at 100 % (scale them with px).
const (
	radiusControl = 8  // buttons, inputs, chips, list rows
	radiusCard    = 12 // cards, popovers, image cards
	radiusFeature = 20 // feature cards (浅灰底的大卡片)
)

// Type scale, DIPs at 100 % (px(t, fsBody)). Body text is the platform's
// size (13 pt on the Mac, the theme's FontSize): px scales the rest with
// it, so a bigger body would scale the whole interface.
const (
	fsTitle   = 26 // big titles: pages, the setup, empty pages (regular weight)
	fsSection = 15 // section headings, dialog titles
	fsBody    = 13 // text
	fsCaption = 12 // secondary lines, captions, meta
	fsMicro   = 11 // badges, keyboard hints
)

// Spacing steps, DIPs at 100 %: 4, 8, 12, 16, 20, 24, 32.
const (
	sp1 = 4
	sp2 = 8
	sp3 = 12
	sp4 = 16
	sp5 = 20
	sp6 = 24
	sp8 = 32
)

// greys are the neutral scale, from the background (0) to the text (9).
type greys [10]ui.Color

// Light: 0 the page, 1 the sidebar (#fafafa), 2 hover, 3 the chosen row
// (#ededed), 4 hairlines (#e5e5e5), 7 secondary text (#6b6b6b), 9 text
// and ink (#0b0b0b). Dark mirrors it on near-black.
var (
	lightGreys = greys{
		ui.Hex("#ffffff"), ui.Hex("#fafafa"), ui.Hex("#f3f3f3"), ui.Hex("#ededed"), ui.Hex("#e5e5e5"),
		ui.Hex("#d4d4d4"), ui.Hex("#a1a1a1"), ui.Hex("#6b6b6b"), ui.Hex("#3d3d3d"), ui.Hex("#0b0b0b"),
	}
	darkGreys = greys{
		ui.Hex("#111111"), ui.Hex("#171717"), ui.Hex("#1f1f1f"), ui.Hex("#262626"), ui.Hex("#2e2e2e"),
		ui.Hex("#404040"), ui.Hex("#6b6b6b"), ui.Hex("#a3a3a3"), ui.Hex("#d4d4d4"), ui.Hex("#f5f5f5"),
	}
)

// accentSet is an accent in the light and the dark theme.
type accentSet struct{ light, dark string }

// accents are the brand accent candidates; the design review (2026-10-14)
// chose teal. accentName is the one in use.
var accents = map[string]accentSet{
	"blue":   {"#2f6bf0", "#4f86f7"},
	"indigo": {"#5752e0", "#7d79f2"},
	"teal":   {"#0d9384", "#2bb3a3"},
}

// accentName is the brand accent (a key of accents); setAccent changes
// it.
var accentName = "teal"

// tokens are the colours of one appearance.
type tokens struct {
	G greys
	// Surfaces.
	Bg      ui.Color // pages, dialogs
	Sidebar ui.Color // the sidebar, the status bar
	Subtle  ui.Color // a card's fill (cards have a fill or a hairline, not both)
	Field   ui.Color // the face of controls: buttons, inputs, selects, chips
	Hover   ui.Color // ghost buttons and rows under the pointer
	Pressed ui.Color // … pressed; the chosen row
	Line    ui.Color // hairlines: dividers, card and control borders
	// Lift is the faint shadow under a white control (secondary buttons,
	// a segmented control's chosen segment); clear in the dark.
	Lift ui.Color
	// Selected: rows selected among many (the logs' lines), the accent's
	// tint, stronger than Hover; SelectedHover one under the pointer.
	Selected, SelectedHover ui.Color
	// Text.
	Text, Text2, Text3 ui.Color // primary, secondary, tertiary
	// Ink: the primary button (near-black, near-white in the dark).
	Ink, InkHover, InkPressed, OnInk ui.Color
	// Accent: state only (selection, progress, focus, links, playing);
	// AccentInk is the accent as text on the background (markup, counts),
	// darker in the light theme for contrast.
	Accent, AccentSoft, AccentInk ui.Color
	// Record: recording and destructive actions (red-orange).
	Record, RecordSoft ui.Color
	// Semantic colours, for small dots and text only.
	Success, Warning, Danger ui.Color
}

func makeTokens(dark bool) *tokens {
	g := lightGreys
	ac := accents[accentName]
	accent := ui.Hex(ac.light)
	k := &tokens{G: g}
	if dark {
		g = darkGreys
		accent = ui.Hex(ac.dark)
		k = &tokens{G: g,
			InkHover: ui.Hex("#dedede"), InkPressed: ui.Hex("#c4c4c4"),
			Record: ui.Hex("#ff6b47"), Success: ui.Hex("#3ccb7f"), Warning: ui.Hex("#f2a53a"),
		}
		k.Bg, k.Sidebar, k.Subtle, k.Field = g[0], g[1], g[1], g[2]
		k.Hover, k.Pressed, k.Line = g[3], g[4], g[4]
	} else {
		k.InkHover, k.InkPressed = ui.Hex("#2b2b2b"), ui.Hex("#3d3d3d")
		k.Record, k.Success, k.Warning = ui.Hex("#eb5230"), ui.Hex("#1f9d55"), ui.Hex("#c27803")
		k.Bg, k.Sidebar, k.Subtle, k.Field = g[0], g[1], ui.Hex("#f7f7f7"), g[0]
		k.Hover, k.Pressed, k.Line = g[2], g[3], g[4]
		k.Lift = ui.RGBA(0, 0, 0, 0.05)
	}
	k.Text, k.Text2, k.Text3 = g[9], g[7], g[6]
	k.Ink, k.OnInk = g[9], g[0]
	k.Accent = accent
	k.AccentSoft = accent.Alpha(0.12)
	k.Selected, k.SelectedHover = accent.Alpha(0.2), accent.Alpha(0.28)
	k.AccentInk = accent
	if !dark {
		k.AccentInk = accent.Mix(ui.Hex("#000000"), 0.18)
	}
	k.RecordSoft = k.Record.Alpha(0.12)
	k.Danger = k.Record
	return k
}

// The tokens of each appearance, for accentName.
var lightTokens, darkTokens = makeTokens(false), makeTokens(true)

// setAccent makes name (a key of accents) the brand accent: the design
// review's comparison shots use it.
func setAccent(name string) {
	accentName = name
	lightTokens, darkTokens = makeTokens(false), makeTokens(true)
}

// tok returns the tokens of t's appearance.
func tok(t *ui.Theme) *tokens {
	if t.Dark {
		return darkTokens
	}
	return lightTokens
}

// styleTheme puts the tokens into th, MyGo's theme.
func styleTheme(th *ui.Theme) {
	k := tok(th)
	th.Background = k.Bg
	// Controls are white (raised a step in the dark) inside a hairline.
	th.Surface = k.Field
	th.SurfaceHover = k.Hover
	th.SurfacePressed = k.Pressed
	th.Border = k.Line
	th.Text, th.TextMuted = k.Text, k.Text2
	th.Accent = k.Accent
	th.AccentHover = k.Accent.Mix(ui.Hex("#000000"), 0.12)
	th.AccentPressed = k.Accent.Mix(ui.Hex("#000000"), 0.24)
	if th.Dark {
		th.AccentHover = k.Accent.Mix(ui.Hex("#ffffff"), 0.15)
		th.AccentPressed = k.Accent.Mix(ui.Hex("#000000"), 0.15)
	}
	th.AccentText = ui.Hex("#ffffff")
	if th.Dark && accentName == "teal" {
		th.AccentText = ui.Hex("#06201c") // dark text on the light teal
	}
	th.Danger, th.Warning, th.Success = k.Danger, k.Warning, k.Success
	th.Selection = k.Accent.Alpha(0.22)
	th.Focus = k.Accent.Alpha(0.5)
	th.Inverse, th.InverseText = k.G[9], k.G[0]
	if th.Dark {
		th.Inverse, th.InverseText = k.G[3], k.G[9]
	}
	th.Scrollbar = k.Text.Alpha(0.22)
	th.Radius = radiusControl
}

// inkTheme is th with ink as its accent: MyGo's primary button in it is
// the near-black (near-white in the dark) primary action.
func inkTheme(th *ui.Theme) *ui.Theme {
	k := tok(th)
	ink := *th
	ink.Accent, ink.AccentHover, ink.AccentPressed, ink.AccentText = k.Ink, k.InkHover, k.InkPressed, k.OnInk
	ink.Focus = k.Accent.Alpha(0.5)
	return &ink
}
