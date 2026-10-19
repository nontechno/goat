package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
)

// panelScreen is a panel window's visible text.
func panelScreen(m *WM, w *Window) string {
	scr := uv.NewScreen(m.cols, m.rows)
	scene{m}.Draw(scr, scr.Bounds())
	var b strings.Builder
	for y := w.contentY(); y < w.contentY()+w.contentH(); y++ {
		for x := w.contentX(); x < w.contentX()+w.contentW(); x++ {
			if c := scr.CellAt(x, y); c != nil && !c.IsZero() {
				b.WriteString(c.Content)
			}
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// withPid gives a fake window a program pid.
func withPid(w *Window, pid int) {
	w.cmd = &exec.Cmd{Process: &os.Process{Pid: pid}}
}

// fakeProcs makes sampling return this table at once.
func fakeProcs(m *WM, procs ...procSample) {
	t := procTable{at: time.Now(), procs: map[int]procSample{}}
	for _, p := range procs {
		t.procs[p.pid] = p
	}
	m.procs.table.Store(&t)
	m.procs.sample = func() procTable { return t }
}

func TestProcTree(t *testing.T) {
	var tb procTable
	tb.procs = map[int]procSample{
		1:   {pid: 1, ppid: 0, rss: 1 << 20, comm: "init"},
		100: {pid: 100, ppid: 1, rss: 2 << 20, cpu: 1.5, comm: "zsh"},
		101: {pid: 101, ppid: 100, rss: 10 << 20, cpu: 20, comm: "vim"},
		102: {pid: 102, ppid: 101, rss: 3 << 20, cpu: 0.5, comm: "zsh"},
		200: {pid: 200, ppid: 1, rss: 5 << 20, comm: "other"},
	}
	tot, ok := tb.tree(100)
	if !ok || tot.count != 3 || tot.rss != 15<<20 || tot.cpu != 22 || strings.Join(tot.names, " ") != "zsh vim" {
		t.Errorf("tree: %+v", tot)
	}
	if _, ok := tb.tree(999); ok {
		t.Error("a missing pid has a tree")
	}
	var none *procTable
	if _, ok := none.tree(100); ok {
		t.Error("no table, yet a tree")
	}
}

func TestParsePS(t *testing.T) {
	out := `    1     0  12000   0.0 /sbin/launchd
  512     1   4096  12,5 /Applications/iTerm.app/Contents/MacOS/iTerm2
  600   512   2048   0.3 -zsh
  700   600   1024   1.0 /usr/bin/my tool
garbage line
`
	tb := parsePS(out)
	if len(tb.procs) != 4 {
		t.Fatalf("%d processes: %+v", len(tb.procs), tb.procs)
	}
	if p := tb.procs[512]; p.comm != "iTerm2" || p.rss != 4096*1024 || p.cpu != 12.5 || p.ppid != 1 {
		t.Errorf("512: %+v", p)
	}
	if p := tb.procs[600]; p.comm != "zsh" {
		t.Errorf("login shell: %+v", p)
	}
	if p := tb.procs[700]; p.comm != "my tool" {
		t.Errorf("spaces: %+v", p)
	}
}

func TestFormatters(t *testing.T) {
	for got, want := range map[string]string{
		fmtBytes(512):                            "512 B",
		fmtBytes(2048):                           "2 KB",
		fmtBytes(41 << 20):                       "41.0 MB",
		fmtBytes(3 << 30):                        "3.0 GB",
		fmtPct(0.25):                             "0.2%",
		fmtPct(42.4):                             "42%",
		fmtSince(45 * time.Second):               "45s",
		fmtSince(12*time.Minute + 5*time.Second): "12m 5s",
		fmtSince(3*time.Hour + 7*time.Minute):    "3h 07m",
		fmtSince(52 * time.Hour):                 "2d 4h",
		fmtCount(1234567):                        "1,234,567",
		fmtCount(999):                            "999",
		nouns(1, "process", "processes"):         "1 process",
		nouns(1200, "line", "lines"):             "1,200 lines",
	} {
		if got != want {
			t.Errorf("%q, want %q", got, want)
		}
	}
}

func TestInventoryToggleAndChoose(t *testing.T) {
	m, bg, a, b := wmWithBackground(t)
	m.cols, m.rows = 100, 40
	withPid(bg, 100)
	withPid(a, 200)
	fakeProcs(m,
		procSample{pid: 100, ppid: 1, rss: 2 << 20, comm: "zsh"},
		procSample{pid: 200, ppid: 1, rss: 1 << 20, cpu: 0.5, comm: "zsh"},
		procSample{pid: 201, ppid: 200, rss: 40 << 20, cpu: 12, comm: "vim"},
		procSample{pid: 202, ppid: 201, rss: 1 << 20, comm: "gopls"},
	)
	a.status.apply(statusReport{state: stWorking, progress: 40, app: "claude", title: "Running tests"})
	a.dir = "/tmp/project"
	m.focus(a)
	key(m, uv.Key{Code: uv.KeyF3})
	inv := m.inv.win
	if !m.inventoryShown() || m.focused != inv || m.inv.chosen != a {
		t.Fatalf("F3: shown %v, focused %v, chosen %v", m.inventoryShown(), m.focused == inv, m.inv.chosen)
	}
	text := panelScreen(m, inv)
	for _, want := range []string{"2 floating windows + the background", "0:", "1:", "2:", "background",
		"zsh vim gopls · 3 processes · pid 200", "CPU    ", "memory ",
		"42.0 MB", "/tmp/project", "status: working 40% · claude · Running tests",
		"          ██████████" + strings.Repeat(" ", 15) + " 40%", "Enter go to it"} {
		if !strings.Contains(text, want) {
			t.Errorf("inventory lacks %q:\n%s", want, text)
		}
	}
	// Not a numbered window; Alt+0 doesn't hide it.
	if m.number(a) != 1 || m.number(b) != 2 || m.tabName(inv) != "inventory" {
		t.Error("the inventory is numbered")
	}

	// ↓ chooses the next window, Enter goes to it.
	key(m, uv.Key{Code: uv.KeyDown})
	if m.inv.chosen != b {
		t.Fatalf("↓ chose %v", m.inv.chosen.id)
	}
	key(m, uv.Key{Code: uv.KeyEnter})
	if m.focused != b || !m.inventoryShown() {
		t.Fatalf("Enter: focused %v", m.focused.id)
	}
	// F3 while another window is focused hides it.
	key(m, uv.Key{Code: uv.KeyF3})
	if m.inventoryShown() || m.focused != b {
		t.Fatal("F3 again didn't hide the inventory")
	}

	// A hidden window: listed with 👻; Enter brings it back.
	m.focus(bg)
	key(m, uv.Key{Code: '0', Mod: uv.ModAlt}) // hide a and b
	if !a.minimized {
		t.Fatal("not hidden")
	}
	key(m, uv.Key{Code: uv.KeyF3})
	if m.inv.chosen != bg || inv.minimized {
		t.Fatalf("chosen %v, inventory hidden %v", m.inv.chosen.id, inv.minimized)
	}
	if text := panelScreen(m, inv); !strings.Contains(text, ghost+" hidden") {
		t.Errorf("hidden not shown:\n%s", text)
	}
	key(m, uv.Key{Code: uv.KeyDown})
	key(m, uv.Key{Code: uv.KeyEnter})
	if m.focused != a || a.minimized || !b.minimized {
		t.Fatalf("Enter on a hidden window: focused %v, a hidden %v, b hidden %v", m.focused.id, a.minimized, b.minimized)
	}
	// Alt+x on the inventory hides it.
	m.focus(inv)
	key(m, uv.Key{Code: 'x', Mod: uv.ModAlt})
	if m.inventoryShown() {
		t.Fatal("Alt+x didn't hide the inventory")
	}
}

// A click chooses an entry; a double click goes to it.
func TestInventoryClick(t *testing.T) {
	m, _, a, b := wmWithBackground(t)
	m.cols, m.rows = 100, 40
	fakeProcs(m)
	m.toggleInventory()
	inv := m.inv.win
	panelScreen(m, inv)
	var row invRow
	for _, r := range m.inv.rows {
		if r.win == a {
			row = r
		}
	}
	y := inv.contentY() + row.first + 1 - (inv.emu.ScrollbackLen() - inv.scroll) // a detail line
	click := uv.MouseClickEvent{X: inv.contentX() + 3, Y: y, Button: uv.MouseLeft}
	m.handleMouse(click)
	m.handleMouse(uv.MouseReleaseEvent{X: click.X, Y: y, Button: uv.MouseLeft})
	if m.inv.chosen != a || m.focused != inv {
		t.Fatalf("click: chosen %v, focused %v", m.inv.chosen, m.focused.id)
	}
	m.handleMouse(click)
	m.handleMouse(uv.MouseReleaseEvent{X: click.X, Y: y, Button: uv.MouseLeft})
	if m.focused != a {
		t.Fatalf("double click: focused %v", m.focused.id)
	}
	_ = b
}

// The list follows the windows: a new one shows within a refresh.
func TestInventoryRefresh(t *testing.T) {
	m, _, _, _ := wmWithBackground(t)
	m.cols, m.rows = 100, 40
	fakeProcs(m)
	m.toggleInventory()
	panelScreen(m, m.inv.win)
	c := fakeWindow(3, 30, 5, 30, 8, true, false)
	c.shellName = "newcomer"
	m.windows = append(m.windows, c)
	m.tick(time.Now().Add(2 * infoEvery))
	if text := panelScreen(m, m.inv.win); !strings.Contains(text, "3:") || !strings.Contains(text, "3 floating") {
		t.Errorf("new window not listed:\n%s", text)
	}
}
