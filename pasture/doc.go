// Package pasture is a small terminal "mux server": a background process
// that owns pseudo-terminals and the programs running on them, so that
// those programs outlive the application (goat) that displays them.
//
// The server (cmd/pasture) listens on a Unix socket. A client (goat, via
// Dial) asks it to spawn programs, attaches to them to receive their output
// and send them input, resizes them, and detaches. On attach the client
// gets a snapshot: escape sequences that recreate the pane's screen,
// scrollback, modes and cursor in a fresh terminal emulator of the given
// size, followed by live output.
//
// The server keeps its own terminal emulator per pane (charmbracelet/x/vt)
// to be able to produce that snapshot, and to answer the program's terminal
// queries (device attributes, cursor position) while no client is attached.
// While a client is attached, the client's emulator answers them.
//
// Unix only (Linux, macOS, BSD).
package pasture
