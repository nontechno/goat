package main

import (
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Program Status Protocol, OSC 7501 (revision 0.4):
// https://www.superlogical.com/rex/docs/build/program-status
//
// A program in a window reports what it is doing:
//
//	ESC ] 7501 ; state=blocked:kind=permission:app=claude:msg=<base64> ST
//
// The body is key=value pairs separated by ":". Values use letters, digits
// and _ . , + / = - only; free text (msg, title) is base64 UTF-8. goat keeps
// one record per id (no id = the root record) for each window, replaced
// whole by every report, and lists every message in the events window.
// A query, ESC ] 7501 ; ? ST, is answered with
// ESC ] 7501 ; ? : states=idle,working,done,blocked,error,clear :
// kinds=permission,question,auth ST (without the spaces): supported, and
// what goat understands. It goes out before any reply to a query sent
// after it (Claude Code sends no reports when the device-attributes reply
// comes first).
//
// What goat does with each kind of report is in the onStatus* methods at
// the end of this file (empty for now).
//
// Lifetimes, as the protocol has them: working and blocked records end when
// a new shell prompt starts (OSC 133;A) or the window's program exits (the
// window closes); done, error and idle stay until replaced or cleared. A
// full reset (RIS) clears every record; a soft reset doesn't.

// statusState is a report's state.
type statusState string

const (
	stIdle    statusState = "idle"
	stWorking statusState = "working"
	stDone    statusState = "done"
	stBlocked statusState = "blocked"
	stError   statusState = "error"
	stClear   statusState = "clear" // not a state: removes records
)

// Limits (per report; the protocol's hard caps).
const (
	statusMaxSeq       = 4096 // OSC through ST
	statusMaxKey       = 16
	statusMaxMsgEnc    = 2732
	statusMaxMsg       = 2048
	statusMaxTitleEnc  = 256
	statusMaxTitle     = 192
	statusMaxApp       = 32
	statusMaxID        = 128
	statusMaxSegment   = 32
	statusMaxDepth     = 8
	statusMaxRecords   = 256 // per window; the least recently updated goes
	statusQueryReply   = "\x1b]7501;?:states=idle,working,done,blocked,error,clear:kinds=permission,question,auth\x1b\\"
	statusOSCOverhead  = len("\x1b]") + len("\x1b\\")
	statusOSCPrefix    = "7501;"
	statusQueryPayload = "?"
)

// statusReport is one parsed report. Absent keys are zero ("" or -1).
type statusReport struct {
	state    statusState
	id       string // "" = the root record
	kind     string // blocked only: permission, question or auth
	progress int    // 0-100 for working and blocked; -1 = absent (indeterminate)
	app      string
	title    string // decoded
	msg      string // decoded
	unknown  []string
	skipped  []string // malformed pairs, ignored
}

// statusRecord is what a window holds for one id.
type statusRecord struct {
	statusReport
	seq uint64 // update order, for evicting the least recently updated
}

// statusStore is a window's records.
type statusStore struct {
	records map[string]*statusRecord
	seq     uint64
	used    bool // a report has arrived (since the last reset)
}

// parseStatus parses an OSC 7501 payload (without "7501;"). query is true
// for the feature-detection query. A report that must be ignored as a whole
// comes back with err set, and nothing of it is to be applied.
func parseStatus(body string) (r statusReport, query bool, err error) {
	r.progress = -1
	if body == statusQueryPayload {
		return r, true, nil
	}
	if len(statusOSCPrefix)+len(body)+statusOSCOverhead > statusMaxSeq {
		return r, false, fmt.Errorf("longer than %d bytes", statusMaxSeq)
	}
	pairs := map[string]string{}
	var order []string
	for _, pair := range strings.Split(body, ":") {
		if pair == "" {
			continue
		}
		key, value, ok := strings.Cut(pair, "=")
		if len(key) > statusMaxKey {
			return r, false, fmt.Errorf("key %.20q longer than %d bytes", key, statusMaxKey)
		}
		if !ok || !statusKey(key) || !statusValue(value) {
			r.skipped = append(r.skipped, pair)
			continue
		}
		if _, seen := pairs[key]; !seen {
			order = append(order, key)
		}
		pairs[key] = value // a repeated key: the last one counts
	}

	state, ok := pairs["state"]
	switch s := statusState(state); {
	case !ok:
		return r, false, fmt.Errorf("no state")
	case s == stIdle, s == stWorking, s == stDone, s == stBlocked, s == stError, s == stClear:
		r.state = s
	default:
		return r, false, fmt.Errorf("unknown state %q", state)
	}
	if id, ok := pairs["id"]; ok {
		if !statusIDValid(id) {
			return r, false, fmt.Errorf("invalid id %q", id)
		}
		r.id = id
	}
	// Free text: over a limit, bad base64 or control characters discard the
	// whole report (sizes checked before decoding).
	if v, ok := pairs["title"]; ok {
		if r.title, err = statusText(v, statusMaxTitleEnc, statusMaxTitle); err != nil {
			return r, false, fmt.Errorf("title: %w", err)
		}
	}
	if v, ok := pairs["msg"]; ok {
		if r.msg, err = statusText(v, statusMaxMsgEnc, statusMaxMsg); err != nil {
			return r, false, fmt.Errorf("msg: %w", err)
		}
	}
	// The rest: an invalid value counts as absent.
	if v, ok := pairs["kind"]; ok && r.state == stBlocked {
		switch v {
		case "permission", "question", "auth":
			r.kind = v
		}
	}
	if v, ok := pairs["progress"]; ok && (r.state == stWorking || r.state == stBlocked) {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 && n <= 100 && len(v) <= 3 {
			r.progress = n
		}
	}
	if v, ok := pairs["app"]; ok && len(v) <= statusMaxApp && v != "" && strings.IndexFunc(v, func(c rune) bool { return !statusSegmentRune(c) }) < 0 {
		r.app = v
	}
	for _, k := range order {
		switch k {
		case "state", "id", "kind", "progress", "app", "title", "msg":
		default:
			r.unknown = append(r.unknown, k) // ignored, for future revisions
		}
	}
	return r, false, nil
}

func statusKey(k string) bool {
	if k == "" {
		return false
	}
	for i := 0; i < len(k); i++ {
		if k[i] < 'a' || k[i] > 'z' {
			return false
		}
	}
	return true
}

func statusValue(v string) bool {
	for i := 0; i < len(v); i++ {
		switch c := v[i]; {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c == '_', c == '.', c == ',', c == '+', c == '/', c == '=', c == '-':
		default:
			return false
		}
	}
	return true
}

func statusSegmentRune(c rune) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
		c == '_' || c == '.' || c == '+' || c == '-'
}

// statusIDValid checks an id path: up to 8 segments of 1-32 characters,
// 128 bytes in all.
func statusIDValid(id string) bool {
	if id == "" || len(id) > statusMaxID {
		return false
	}
	segs := strings.Split(id, "/")
	if len(segs) > statusMaxDepth {
		return false
	}
	for _, s := range segs {
		if s == "" || len(s) > statusMaxSegment || strings.IndexFunc(s, func(c rune) bool { return !statusSegmentRune(c) }) >= 0 {
			return false
		}
	}
	return true
}

// statusText decodes a base64 text value (padding optional).
func statusText(v string, maxEnc, maxDec int) (string, error) {
	if len(v) > maxEnc {
		return "", fmt.Errorf("longer than %d bytes encoded", maxEnc)
	}
	b, err := base64.RawStdEncoding.DecodeString(strings.TrimRight(v, "="))
	if err != nil {
		return "", fmt.Errorf("invalid base64")
	}
	if len(b) > maxDec {
		return "", fmt.Errorf("longer than %d bytes", maxDec)
	}
	if !utf8.Valid(b) {
		return "", fmt.Errorf("not UTF-8")
	}
	if strings.IndexFunc(string(b), unicode.IsControl) >= 0 {
		return "", fmt.Errorf("control characters")
	}
	return string(b), nil
}

// apply stores a report. For clear it returns the ids removed.
func (s *statusStore) apply(r statusReport) (removed []string) {
	s.used = true
	if r.state == stClear {
		return s.remove(func(id string) bool {
			return r.id == "" || id == r.id || strings.HasPrefix(id, r.id+"/")
		})
	}
	if s.records == nil {
		s.records = map[string]*statusRecord{}
	}
	if _, ok := s.records[r.id]; !ok && len(s.records) >= statusMaxRecords {
		oldest := ""
		var oldestSeq uint64
		for id, rec := range s.records {
			if oldestSeq == 0 || rec.seq < oldestSeq {
				oldest, oldestSeq = id, rec.seq
			}
		}
		delete(s.records, oldest)
	}
	s.seq++
	s.records[r.id] = &statusRecord{statusReport: r, seq: s.seq}
	return nil
}

// remove deletes the records drop picks, and says which.
func (s *statusStore) remove(drop func(id string) bool) (removed []string) {
	for id := range s.records {
		if drop(id) {
			delete(s.records, id)
			removed = append(removed, statusIDName(id))
		}
	}
	return removed
}

// dropTransient removes working and blocked records (a new shell prompt).
func (s *statusStore) dropTransient() []string {
	return s.remove(func(id string) bool {
		st := s.records[id].state
		return st == stWorking || st == stBlocked
	})
}

// reset forgets everything (a full terminal reset).
func (s *statusStore) reset() []string {
	removed := s.remove(func(string) bool { return true })
	s.used = false
	return removed
}

// app is the record's app, or the nearest ancestor's.
func (s *statusStore) app(id string) string {
	for {
		if rec, ok := s.records[id]; ok && rec.app != "" {
			return rec.app
		}
		if id == "" {
			return ""
		}
		if i := strings.LastIndexByte(id, '/'); i >= 0 {
			id = id[:i]
		} else {
			id = ""
		}
	}
}

func statusIDName(id string) string {
	if id == "" {
		return "(root)"
	}
	return id
}

// describe is a report as the events window lists it:
// `blocked id=deploy/eu app=claude kind=permission progress=40 title="…" msg="…"`.
func (r statusReport) describe(inheritedApp string) string {
	parts := []string{string(r.state)}
	if r.id != "" {
		parts = append(parts, "id="+r.id)
	}
	switch {
	case r.app != "":
		parts = append(parts, "app="+r.app)
	case inheritedApp != "":
		parts = append(parts, "app="+inheritedApp+" (inherited)")
	}
	if r.kind != "" {
		parts = append(parts, "kind="+r.kind)
	}
	if r.progress >= 0 {
		parts = append(parts, fmt.Sprintf("progress=%d%%", r.progress))
	} else if r.state == stWorking || r.state == stBlocked {
		parts = append(parts, "progress=?")
	}
	if r.title != "" {
		parts = append(parts, "title="+strconv.Quote(r.title))
	}
	if r.msg != "" {
		parts = append(parts, "msg="+strconv.Quote(r.msg))
	}
	if len(r.unknown) > 0 {
		parts = append(parts, "ignored keys: "+strings.Join(r.unknown, ","))
	}
	if len(r.skipped) > 0 {
		parts = append(parts, fmt.Sprintf("skipped %d malformed pair(s)", len(r.skipped)))
	}
	return strings.Join(parts, " ")
}

// ---- goat's side --------------------------------------------------------------

// evStatus lists program status messages in the events window.
const evStatus eventKind = "status"

// handleStatusOSC takes an OSC 7501 from w's program (payload "7501;…"):
// answers a query, stores a report, lists it in the events window and calls
// the onStatus* method for its state.
func (m *WM) handleStatusOSC(w *Window, data []byte) {
	body, _ := strings.CutPrefix(string(data), statusOSCPrefix)
	if string(data) == "7501" {
		body = "" // no payload at all
	}
	r, query, err := parseStatus(body)
	switch {
	case query:
		w.replyStatusQuery()
		m.logEvent(w, evStatus, "query ? (answered: supported)")
		m.onStatusQuery(w)
	case err != nil:
		m.logEvent(w, evStatus, "ignored: "+err.Error()+": "+statusRaw(body))
		m.onStatusRejected(w, body, err)
	case r.state == stClear:
		removed := w.appStatus.apply(r)
		text := "clear " + statusIDName(r.id)
		if r.id == "" {
			text = "clear (all)"
		}
		if len(removed) > 0 {
			text += ": removed " + strings.Join(removed, ", ")
		} else {
			text += ": nothing to remove"
		}
		m.logEvent(w, evStatus, text)
		m.onStatusClear(w, r.id, removed)
	default:
		w.appStatus.apply(r)
		inherited := ""
		if r.app == "" {
			inherited = w.appStatus.app(r.id)
		}
		m.logEvent(w, evStatus, r.describe(inherited))
		rec := w.appStatus.records[r.id]
		switch r.state {
		case stIdle:
			m.onStatusIdle(w, rec)
		case stWorking:
			m.onStatusWorking(w, rec)
		case stDone:
			m.onStatusDone(w, rec)
		case stBlocked:
			m.onStatusBlocked(w, rec)
		case stError:
			m.onStatusError(w, rec)
		}
	}
}

// statusRaw is a rejected payload, shortened for the events window.
func statusRaw(body string) string {
	if body == "" {
		return "(empty)"
	}
	if len(body) > 120 {
		body = body[:120] + ellipsis
	}
	return strconv.Quote(body)
}

// handlePromptOSC takes an OSC 133 (shell integration) from w's program: a
// new prompt (133;A) ends the working and blocked records.
func (m *WM) handlePromptOSC(w *Window, data []byte) {
	mark, _, _ := strings.Cut(strings.TrimPrefix(string(data), "133;"), ";")
	if mark != "A" {
		return
	}
	if removed := w.appStatus.dropTransient(); len(removed) > 0 {
		m.logEvent(w, evStatus, "prompt: ended "+strings.Join(removed, ", "))
		m.onStatusEnded(w, "prompt", removed)
	}
}

// statusReset is a full reset (RIS) in w: every record goes.
func (m *WM) statusReset(w *Window) {
	if removed := w.appStatus.reset(); len(removed) > 0 {
		m.logEvent(w, evStatus, "terminal reset: cleared "+strings.Join(removed, ", "))
		m.onStatusEnded(w, "reset", removed)
	}
}

// replyStatusQuery answers OSC 7501 ; ? through the emulator's reply
// stream, so it reaches the program in order with the emulator's own
// replies (a device-attributes reply sent after the query comes after it).
func (w *Window) replyStatusQuery() {
	if w.emu != nil {
		_, _ = w.emu.InputPipe().Write([]byte(statusQueryReply))
	}
}

// ---- reactions: called for each message received (empty for now) ----------

// onStatusIdle: a program is at rest, waiting for its next instruction.
func (m *WM) onStatusIdle(w *Window, rec *statusRecord) {}

// onStatusWorking: a program is busy (rec.progress 0-100, or -1).
func (m *WM) onStatusWorking(w *Window, rec *statusRecord) {}

// onStatusDone: a program finished; its result is waiting to be seen.
func (m *WM) onStatusDone(w *Window, rec *statusRecord) {}

// onStatusBlocked: a program needs the user (rec.kind: permission,
// question, auth or "").
func (m *WM) onStatusBlocked(w *Window, rec *statusRecord) {}

// onStatusError: a program failed and stopped.
func (m *WM) onStatusError(w *Window, rec *statusRecord) {}

// onStatusClear: a program removed records (id "" = all of them).
func (m *WM) onStatusClear(w *Window, id string, removed []string) {}

// onStatusQuery: a program asked whether goat supports the protocol (it
// has been answered).
func (m *WM) onStatusQuery(w *Window) {}

// onStatusRejected: a report was ignored as a whole (why: err).
func (m *WM) onStatusRejected(w *Window, body string, err error) {}

// onStatusEnded: records ended without a report: a new shell prompt
// ("prompt") or a full terminal reset ("reset").
func (m *WM) onStatusEnded(w *Window, why string, removed []string) {}
