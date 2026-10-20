package main

import (
	"fmt"
	"slices"
	"strings"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
)

// Keyboard input: shortcuts, Esc as Alt, keys to programs, paste.

// ---- keyboard --------------------------------------------------------------

func (m *WM) run(a action) error {
	defer m.saveDesktop() // Alt+s may change its colors (the palette dialog saves its own)
	switch a {
	case actNewWindow:
		return m.newWindow()
	case actFocusNext, actFocusPrev:
		ids := m.shownCycle()
		if len(ids) < 2 || m.focused == nil {
			return nil
		}
		i := slices.Index(ids, m.focused)
		if a == actFocusNext {
			i = (i + 1) % len(ids)
		} else {
			i = (i - 1 + len(ids)) % len(ids)
		}
		m.focus(ids[i])
	case actQuit:
		m.askQuit() // asks first when floating windows are open
	case actCloseWindow:
		switch w := m.focused; {
		case w == nil:
		case w.background:
			m.setBarMessage("the background shell can't be closed: type exit, or Alt+"+m.cfg.Keys.Quit+" to quit", 4*time.Second)
		case m.isEvents(w):
			m.setBarMessage("Alt+"+m.cfg.Keys.ShowEvents+" hides the events window", 3*time.Second)
		case m.isPanel(w):
			m.hidePanel(w) // (as Alt+F1, Alt+F2, Alt+F3 do)
		default:
			m.remove(w)
		}
	case actMoveLeft:
		m.moveFocused(-1, 0)
	case actMoveRight:
		m.moveFocused(1, 0)
	case actMoveUp:
		m.moveFocused(0, -1)
	case actMoveDown:
		m.moveFocused(0, 1)
	case actResizeLeft:
		m.resizeFocused(-1, 0)
	case actResizeRight:
		m.resizeFocused(1, 0)
	case actResizeUp:
		m.resizeFocused(0, -1)
	case actResizeDown:
		m.resizeFocused(0, 1)
	case actPalette:
		m.openPalette() // paldlg.go
	case actToggleWrap:
		if w := m.focused; w != nil {
			m.clearSelectionIn(w) // its columns may not exist afterwards
			w.setWrap(w.nowrap)
			m.setBarMessage(fmt.Sprintf("window %d: %s", m.number(w), map[bool]string{
				true:  "long lines run past the edge (scroll sideways: Alt+< / Alt+>, Shift+wheel)",
				false: "long lines wrap"}[w.nowrap]), 3*time.Second)
			m.dirty = true
		}
	case actScrollLeft, actScrollRight:
		if w := m.focused; w != nil {
			step := max(w.contentW()/2, 1)
			if w.scrollSideways(pick(a == actScrollLeft, -step, step)) {
				m.dirty = true
			}
		}
	case actShowEvents:
		m.toggleEvents()
	case actSwapBackground:
		m.swapWithBackground()
	case actRepaint:
		m.repaint()
	case actSettings:
		m.openSettings()
	case actPinWindow:
		if w := m.focused; w != nil && !w.background {
			w.pinned = !w.pinned
			m.raise(w)
			m.dirty = true
		}
	}
	return nil
}

// shortcut runs the window-manager action for an Alt key, if it is one.
// It reports whether the key was consumed.
func (m *WM) shortcut(k uv.Key) (bool, error) {
	if name := m.panelForKey(k); name != "" { // an Alt key from system.toml
		m.togglePanel(name)
		return true, nil
	}
	shift := k.Mod.Contains(uv.ModShift)
	switch k.Code {
	case uv.KeyLeft:
		return true, m.run(pick(shift, actResizeLeft, actMoveLeft))
	case uv.KeyRight:
		return true, m.run(pick(shift, actResizeRight, actMoveRight))
	case uv.KeyUp:
		return true, m.run(pick(shift, actResizeUp, actMoveUp))
	case uv.KeyDown:
		return true, m.run(pick(shift, actResizeDown, actMoveDown))
	// History: Alt+PageUp/PageDown by a page, with Shift a few lines;
	// Alt+Home to the oldest line, Alt+End back to the live view.
	case uv.KeyPgUp:
		m.scrollFocused(pick(shift, scrollStep, max(m.focusedPage(), 1)))
		return true, nil
	case uv.KeyPgDown:
		m.scrollFocused(-pick(shift, scrollStep, max(m.focusedPage(), 1)))
		return true, nil
	case uv.KeyHome:
		if w := m.focused; w != nil {
			m.scrollWindow(w, w.emu.ScrollbackLen())
		}
		return true, nil
	case uv.KeyEnd:
		if w := m.focused; w != nil {
			m.scrollWindow(w, -w.scroll)
		}
		return true, nil
	}
	r, ok := bindingRune(k)
	if !ok {
		return false, nil
	}
	if r == '0' {
		switch {
		case m.bg == nil:
		case m.focused == m.bg: // again: hide or bring back the others
			m.toggleHidden()
		default:
			m.focus(m.bg)
		}
		return true, nil
	}
	if r >= '1' && r <= '9' {
		if ids := m.byID(); int(r-'1') < len(ids) {
			m.focus(ids[r-'1'])
		}
		return true, nil
	}
	if a, ok := m.cfg.bindings[r]; ok {
		return true, m.run(a)
	}
	return false, nil
}

// scrollStep is how many lines Alt+Shift+PageUp/PageDown scroll.
const scrollStep = 3

func pick[T any](cond bool, a, b T) T {
	if cond {
		return a
	}
	return b
}

// learnOptionArrows works out, in macOS Terminal, whether Esc b / Esc f are
// Option+Left / Option+Right (Option types characters: the default) or
// Alt+b / Alt+f ("Use Option as Meta key" is on: then Option+letters arrive
// as Esc + letter too). Any other Alt+letter arriving as Esc + letter means
// Meta is on; an Option character (ç, ∫, ...) means it is off.
func (m *WM) learnOptionArrows(k uv.Key) {
	if r, ok := bindingRune(k); ok && k.Mod.Contains(uv.ModAlt) && r != 'b' && r != 'f' {
		m.optionArrows, m.arrowsLearn = false, false
		logger.Info("macOS Terminal sends Option as Meta: Esc b / Esc f are Alt+b / Alt+f")
		return
	}
	if _, ok := macOptionKey(k); ok {
		m.arrowsLearn = false // Option types characters: Esc b / Esc f are arrows
		logger.Info("macOS Terminal types Option characters: Esc b / Esc f are Option+Left / Right")
	}
}

func (m *WM) handleKey(k uv.Key) error {
	// Keys this terminal sends differently, as goat -learn found out.
	if !m.pasting && !m.escPending {
		k = m.remapKey(k)
	}
	// macOS Terminal: Option+Left / Option+Right arrive as Esc b / Esc f.
	// (Esc then b typed by hand comes as two keys: still Alt+b.)
	if m.arrowsLearn && !m.pasting && !m.escPending {
		m.learnOptionArrows(k)
	}
	if m.optionArrows && !m.pasting && !m.escPending {
		if k2, ok := optionArrowKey(k); ok {
			k = k2
		}
	}
	// The quit shortcut isn't held up by the palette or settings dialog:
	// it closes it, as Esc does, and quits (asking first, as always).
	qk := k
	if k2, ok := macOptionKey(k); ok && m.macOption { // macOS Option+q types œ
		qk = k2
	}
	if r, ok := bindingRune(qk); ok && qk.Mod.Contains(uv.ModAlt) && m.cfg.bindings[r] == actQuit &&
		(m.settings != nil || m.palDlg != nil) && !m.pasting {
		if m.palDlg != nil {
			m.closePalette(false)
		}
		if m.settings != nil {
			m.closeSettings()
		}
		return m.run(actQuit)
	}
	if m.settings != nil && !m.pasting {
		m.settingsKey(k)
		return nil
	}
	if m.quitAsk != nil && !m.pasting {
		m.quitDialogKey(k)
		return nil
	}
	if m.palDlg != nil && !m.pasting {
		m.paletteKey(k)
		return nil
	}
	// The help window waiting for a shortcut's new key, or for Enter / Esc
	// on a collision (keymap.go): every key goes there, Esc and Alt+keys
	// included; but Alt+F1 … F3, which show / hide goat's windows (they
	// can't be shortcuts), still do that, and drop the change.
	if h := &m.help; (h.capturing || h.pending != nil) && m.isHelp(m.focused) && !m.pasting && m.panelForKey(k) == "" {
		if h.capturing {
			m.helpCapture(k)
		} else {
			m.helpKey(k)
		}
		return nil
	}
	// goat's own windows: Alt+F1 help, Alt+F2 status, Alt+F3 inventory
	// (system.go).
	if name := m.panelForKey(k); name != "" && !m.pasting {
		m.togglePanel(name)
		return nil
	}
	if m.pasting { // raw paste without bracketed-paste support upstream
		if k.Text != "" {
			m.pasteBuf = append(m.pasteBuf, k.Text...)
		} else {
			m.pasteBuf = append(m.pasteBuf, encodeKey(k, false)...)
		}
		return nil
	}

	// Esc followed quickly by a character key acts as Alt+key. Anything else
	// is not a shortcut: deliver the held-back Esc, then handle the key.
	if m.escPending {
		m.cancelEsc()
		if _, isChar := bindingRune(k); isChar && (k.Mod == 0 || k.Mod == uv.ModShift) {
			k2 := k
			k2.Mod |= uv.ModAlt
			if ok, err := m.shortcut(k2); ok || err != nil {
				return err
			}
		}
		m.sendFocused([]byte{0x1b})
	}

	if k.Mod.Contains(uv.ModAlt) {
		if ok, err := m.shortcut(k); ok || err != nil {
			return err
		}
	}

	// macOS Option+key typing a character (Option+c = ç): a shortcut if
	// that Alt+key is one, otherwise the character is typed as usual.
	if m.macOption {
		if k2, ok := macOptionKey(k); ok {
			if ok, err := m.shortcut(k2); ok || err != nil {
				return err
			}
		}
	}

	if k.Code == uv.KeyEscape && k.Mod == 0 && m.cfg.AltTimeoutMs > 0 && m.focused != nil {
		m.escPending = true
		m.escTimer = time.AfterFunc(time.Duration(m.cfg.AltTimeoutMs)*time.Millisecond, func() {
			select {
			case m.escFire <- struct{}{}:
			default:
			}
		})
		return nil
	}

	if w := m.focused; m.isPanel(w) {
		m.panelKey(w, k) // scrolling keys; there is no program to type to
		return nil
	}
	if w := m.focused; w != nil {
		m.clearSelectionIn(w)
		m.sendFocused(w.encodeKey(k)) // as the program asked for keys (keyboard.go); redraws if it was scrolled back
	}
	return nil
}

func (m *WM) cancelEsc() {
	m.escPending = false
	if m.escTimer != nil {
		m.escTimer.Stop()
	}
	select { // drop a timer that already fired
	case <-m.escFire:
	default:
	}
}

// escExpired: the Esc timeout passed with no key, so it was a plain Esc.
func (m *WM) escExpired() {
	if m.escPending {
		m.escPending = false
		m.sendFocused([]byte{0x1b})
	}
}

func (m *WM) sendFocused(b []byte) {
	if w := m.focused; w != nil && w.held {
		// Its program has ended: Enter closes the window.
		if strings.ContainsAny(string(b), "\r\n") {
			m.remove(w)
		}
		return
	}
	if w := m.focused; w != nil {
		if w.scroll != 0 {
			m.dirty = true
		}
		w.send(b)
	}
}

func (m *WM) paste(text string) {
	if m.settings != nil {
		m.settingsPaste(text)
		return
	}
	if m.quitAsk != nil || m.palDlg != nil {
		return
	}
	if m.isPanel(m.focused) {
		m.setBarMessage("not pasted: the "+m.tabName(m.focused)+" window takes no input", 3*time.Second)
		return
	}
	if w := m.focused; w != nil && !w.closed && text != "" {
		m.clearSelectionIn(w)
		w.scroll = 0
		w.emu.Paste(text) // bracketed if the program asked for it
		m.dirty = true
	}
}
