package main

// Iconography: every symbol goat draws itself, in one place. Change one here
// and it changes everywhere it is used.
//
// Width matters. ASCII and box-drawing characters take one column in every
// terminal. Emoji take two in most, and the screen writer makes sure a
// terminal that disagrees can't push the frame out of place
// (third_party/ultraviolet/uncertain.go). Labels are measured, not
// counted, so any symbol can be swapped for one of another width.
//
// Not here: text goat matches rather than draws (the Braille spinner frames
// of program titles, titles.go; the characters macOS Option types, input.go),
// key names in prompts (learn.go) and the log file (logging.go).

// ---- window frame labels (bottom border) ------------------------------------

// The identity label on the left of a window's bottom border (show_user,
// show_host; ident.go frameText, host.go drawHost):
//
//	└─ 🧑alice 💻myhost 📖nontechno/goat 🌿main ──[]─┘
//
// hostSymbol also marks the host goat runs on in the status bar
// (local.toml show_launch_host; render.go drawStatus).
//
// goat -learn checks that the terminal draws them two columns wide (else
// frames show plain user@host; learn.go).
const (
	userSymbol = "🧑" // the user of the window's foreground program (or ssh's)
	hostSymbol = "💻" // the host: goat's, or where ssh logs in
)

// The git label after the identity (show_git; git.go frameText).
const (
	repoSymbol   = "📖" // repository: owner/name
	branchSymbol = "🌿" // branch, or "detached <commit>"
)

// The line-wrapping state near the right end of the bottom border, drawn
// without spaces around it (render.go wrapLabel, drawWrapIndicator; host.go
// keeps the identity label clear of it).
const (
	wrapSymbol   = "[]" // long lines wrap: kept within the brackets
	nowrapSymbol = "[)" // long lines run past the right edge (Alt+z)
)

// ---- window frame lines (render.go drawFrame) -------------------------------
//
//	╭─ 1:vim ────╮    compact frame: title in the top line
//	│            │
//	└─ 💻box ──[]┘
//
//	╭────────────╮    full frame: a title row and a separator under it
//	│   1:vim    │
//	├────────────┤
//
// Frame "none" is only a top line of frameH.
const (
	frameH           = '─'
	frameV           = '│'
	frameTopLeft     = '╭'
	frameTopRight    = '╮'
	frameBottomLeft  = '└'
	frameBottomRight = '┘'
	frameTeeLeft     = '├' // the full frame's separator under the title row
	frameTeeRight    = '┤'
)

// The quit dialog's box (dialog.go drawQuitDialog): rounded at the bottom
// too, so it doesn't look like a window.
const (
	dialogBottomLeft  = '╰'
	dialogBottomRight = '╯'
)

// The palette dialog (Alt+t; paldlg.go): its box is drawn with the frame
// lines above; these join its columns, and the double box frames the
// chosen palette:
//
//	╭───────theme─┬─────────────────────────palette─╮
//	│ dark        │ Minty Fresh Void │ Pea Soup …    │
//	│ in-between  │ Coral Reef Check ╔═══════════════╗
//	│ light       │ Boiled Lobster … ║ Twilight Hug  ║
//	├───────lingo─┤ …                ╚═══════════════╝
const (
	dialogTeeDown = '┬' // the top line, where the columns meet
	dialogTeeUp   = '┴' // the bottom line
	paletteTL     = '╔' // the chosen palette's frame
	paletteTR     = '╗'
	paletteBL     = '╚'
	paletteBR     = '╝'
	paletteH      = '═'
	paletteV      = '║'
)

// The selected button in the quit dialog: "> Quit <" (dialog.go
// buttonCells); the other button gets spaces of the same width.
const (
	buttonMarkLeft  = "> "
	buttonMarkRight = " <"
)

// While a window is scrolled back, the scroll position marker (render.go
// drawScrollMarker): on the right border, or, for windows without one (frame
// "none", the background shell), over the last column of the content.
const (
	scrollMarkBorder  = '┃'
	scrollMarkOverlay = '▐'
)

// ---- status bar (render.go drawStatus) --------------------------------------

// Window tabs: " 2:vim* " for a pinned window (Alt+w), " 👻3:htop " for one
// hidden with Alt+0 (minimize.go). The ghost also marks a hidden window in
// the inventory window (F3; inventory.go inventoryFlags).
const (
	pinnedSymbol = "*"
	ghost        = "👻"
)

// Unseen events while the events window is hidden: " !3 " left of the clock
// (events.go eventsIndicator).
const unseenEventsSymbol = "!"

// ---- text ------------------------------------------------------------------

// ellipsis marks text cut to fit: "abc…" (render.go fitStart; also every
// line of the status and inventory windows, info.go, inventory.go), "…/src/goat"
// (fitEnd: the status bar directory), a long notification or ignored status
// report in the events window (events.go cleanEventText, status.go
// statusRaw).
const ellipsis = "…"

// repeatSymbol counts repeats of an event in a row: "bell ×3" (events.go).
const repeatSymbol = "×"

// changeSymbol shows a change in the events window: "alice@box → root@box"
// (ident.go), "nontechno/goat: main → feature" (git.go).
const changeSymbol = "→"
