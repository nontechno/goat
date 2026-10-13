package main

import (
	"regexp"
	"strings"
)

// Title changes are listed in the events window, except two kinds that
// programs (such as AI coding assistants) repeat many times a second or
// minute:
//
//	action   "[ . ] Action Required ..." or "[ ! ] Action Required ...":
//	         listed once, as an "action" event, until another title comes
//	spinner  "⠋ Working..." (a Braille spinner frame, then a space): listed
//	         once, as a "spinner" event; the following frames are not
//
// Any other title is listed as a title change, as always, and ends a run
// of action or spinner titles (so the next one is listed again). The title
// bar still shows every title (title_source = "terminal").

type titleClass int

const (
	titleOther titleClass = iota
	titleAction
	titleSpinner
)

const (
	evAction  eventKind = "action"
	evSpinner eventKind = "spinner"
)

// actionTitle matches "[ . ] Action Required" / "[ ! ] Action Required" at
// the start of a title (spaces inside the brackets optional), followed by a
// space, the end, or punctuation.
var actionTitle = regexp.MustCompile(`^\[\s*[.!]\s*\]\s*Action Required(?:[\s:,.!-]|$)`)

// spinnerFrames are the Braille dots spinner's frames.
const spinnerFrames = "⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏"

// classifyTitle says which kind a title is, and for a spinner the text
// after its frame.
func classifyTitle(s string) (titleClass, string) {
	if actionTitle.MatchString(s) {
		return titleAction, s
	}
	for _, f := range spinnerFrames {
		if rest, ok := strings.CutPrefix(s, string(f)+" "); ok {
			return titleSpinner, rest
		}
	}
	return titleOther, s
}

// titleFilter decides which title changes of one window are listed.
type titleFilter struct {
	last titleClass // the kind of title last listed
}

// next returns the event to list for a new title, if any.
func (f *titleFilter) next(title string) (kind eventKind, text string, report bool) {
	class, rest := classifyTitle(title)
	repeat := class != titleOther && class == f.last
	f.last = class
	if repeat {
		return "", "", false
	}
	switch class {
	case titleAction:
		return evAction, titleEventText(strings.TrimSpace(rest)), true
	case titleSpinner:
		return evSpinner, titleEventText(strings.TrimSpace(rest)), true
	}
	return evTitle, titleEventText(title), true
}
