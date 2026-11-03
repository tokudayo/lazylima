package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"lazylima/internal/engine"
)

type preferences struct {
	Timestamps bool `json:"timestamps"`
	Wrap       bool `json:"wrap"`
}
type row struct {
	Key, Group     string
	Container      *engine.Container
	Count, Running int
}
type refreshMsg struct {
	id         uint64
	profiles   []engine.Profile
	containers []engine.Container
	err        error
	profile    string
}

// statsMsg arrives separately because sampling usage takes over a second;
// the container list shouldn't wait for it.
type statsMsg struct {
	profile string
	stats   []engine.Stats
	err     error
}
type tickMsg time.Time
type logOpened struct {
	epoch  uint64
	reader io.ReadCloser
	err    error
}
type logData struct {
	epoch uint64
	data  string
	err   error
}
type progressMsg string
type operationDone struct {
	err     error
	profile string
	kind    string
}
type shellDone struct{ err error }
type clipDone struct {
	label string // what was copied, for the status line
	err   error
}

type Model struct {
	backend                   engine.Backend
	demo                      bool
	ctx                       context.Context
	cancel                    context.CancelFunc
	width, height             int
	profiles                  []engine.Profile
	profile                   string
	containers                []engine.Container
	stats                     []engine.Stats
	sampled                   time.Time
	statsError                string
	selected                  int
	focus                     int
	collapsed                 map[string]bool
	prefs                     preferences
	prefsPath                 string
	query                     string
	search                    textinput.Model
	searchMode                string
	status, errorText         string
	refreshID                 uint64
	refreshBusy               bool
	cancelRefresh             context.CancelFunc
	logEpoch                  uint64
	reader                    io.ReadCloser
	cancelLogs                context.CancelFunc
	logs                      []string
	partial                   string
	logBytes                  int
	logQuery                  string
	follow                    bool
	logEnded                  bool
	logRetry                  time.Time
	logViewport               viewport.Model
	modal                     string
	modalIndex                int
	menuQuery                 string
	form                      []textinput.Model
	formIndex                 int
	formNew                   bool
	formProfile               engine.Profile
	pending                   engine.Operation
	confirmTitle, confirmText string
	busy                      bool
	handoff                   bool
	opCancel                  context.CancelFunc
	opEvents                  chan tea.Msg
	progress                  []string
	progressTop               int
	progressPinned            bool
	wave                      int
	logRows                   []logRow
	sel                       selection
	waving                    bool
	statsBusy                 bool
	statusKind                statusKind
	statusAt                  time.Time
	running                   engine.Operation // the operation in the output view
	canceled                  bool             // the user asked to cancel it
	opFailed                  bool             // the error bar is about that operation
	settling                  bool             // operation done; waiting for the refresh that shows it
	tab                       string           // "" for containers, "images"
	images                    []engine.Image
	imageSel                  int
	imagesBusy                bool
	layers                    []engine.Layer
	layersFor                 string // image ID the layers belong to
	layerTop                  int
	offline                   bool // the last refresh failed
	shellName                 string
	clipboard                 func(string) error // pbcopy; swapped out in tests
	lastWheel                 time.Time
	wheelMuted                bool // ignore wheel events until a quiet gap
}

func New(b engine.Backend, demo bool) *Model {
	ctx, cancel := context.WithCancel(context.Background())
	s := textinput.New()
	s.CharLimit = 256
	s.Prompt = ""
	s.Width = 50
	m := &Model{backend: b, demo: demo, ctx: ctx, cancel: cancel, collapsed: map[string]bool{}, prefs: preferences{Wrap: true}, search: s, logViewport: viewport.New(60, 15), follow: true, status: "Connecting to Colima…"}
	m.clipboard = m.pbcopy
	if !demo {
		if base, err := os.UserConfigDir(); err == nil {
			m.prefsPath = filepath.Join(base, "lazylima", "preferences.json")
			data, err := os.ReadFile(m.prefsPath)
			if err == nil {
				if err = json.Unmarshal(data, &m.prefs); err != nil {
					m.errorText = "Could not read preferences: " + err.Error()
				}
			} else if !os.IsNotExist(err) {
				m.errorText = err.Error()
			}
		}
	}
	return m
}
func (m *Model) Close() {
	m.cancel()
	if m.cancelRefresh != nil {
		m.cancelRefresh()
	}
	m.stopLogs()
	if m.opCancel != nil {
		m.opCancel()
	}
}

// pickProfile follows whatever Colima already has running, preferring
// "default", and falls back to "default" or the first profile.
func pickProfile(ps []engine.Profile) *engine.Profile {
	var best *engine.Profile
	score := -1
	for i := range ps {
		s := 0
		if ps[i].Running() {
			s += 2
		}
		if ps[i].Name == "default" {
			s++
		}
		if s > score {
			best, score = &ps[i], s
		}
	}
	return best
}

func (m *Model) Init() tea.Cmd { return tea.Batch(m.refresh(), tick()) }

type waveMsg struct{}

func waveTick() tea.Cmd {
	return tea.Tick(70*time.Millisecond, func(time.Time) tea.Msg { return waveMsg{} })
}

// welcoming reports whether Colima answered with an empty profile list.
func (m *Model) welcoming() bool { return m.profiles != nil && len(m.profiles) == 0 }
func tick() tea.Cmd              { return tea.Tick(4*time.Second, func(t time.Time) tea.Msg { return tickMsg(t) }) }
func (m *Model) current() *engine.Profile {
	for i := range m.profiles {
		if m.profiles[i].Name == m.profile {
			return &m.profiles[i]
		}
	}
	return nil
}
func (m *Model) rows() []row {
	groups := map[string][]engine.Container{}
	var standalone []engine.Container
	query := strings.ToLower(m.query)
	for _, c := range m.containers {
		if query != "" && !strings.Contains(strings.ToLower(c.Names+" "+c.Image+" "+c.Project+" "+c.Service+" "+c.State), query) {
			continue
		}
		if c.Project != "" {
			groups[c.Project] = append(groups[c.Project], c)
		} else {
			standalone = append(standalone, c)
		}
	}
	var names []string
	for name := range groups {
		names = append(names, name)
	}
	sort.Strings(names)
	var rows []row
	for _, name := range names {
		running := 0
		for _, c := range groups[name] {
			if c.Active() {
				running++
			}
		}
		rows = append(rows, row{Key: "group:" + name, Group: name, Count: len(groups[name]), Running: running})
		if m.collapsed[name] && query == "" {
			continue
		}
		for _, c := range groups[name] {
			copy := c
			rows = append(rows, row{Key: c.ID, Group: name, Container: &copy})
		}
	}
	for _, c := range standalone {
		copy := c
		rows = append(rows, row{Key: c.ID, Container: &copy})
	}
	return rows
}
func (m *Model) selectedRow() *row {
	rows := m.rows()
	if m.selected < 0 || m.selected >= len(rows) {
		return nil
	}
	return &rows[m.selected]
}
func (m *Model) container() *engine.Container {
	if m.onImages() {
		return nil
	}
	if r := m.selectedRow(); r != nil {
		return r.Container
	}
	return nil
}
func (m *Model) selectedKey() string {
	if r := m.selectedRow(); r != nil {
		return r.Key
	}
	return ""
}
func (m *Model) restoreSelection(key string) {
	rows := m.rows()
	for i, r := range rows {
		if r.Key == key {
			m.selected = i
			return
		}
	}
	m.selected = min(max(0, m.selected), max(0, len(rows)-1))
}

// fetchStats samples container usage in the background, one sample at a time.
func (m *Model) fetchStats() tea.Cmd {
	p := m.current()
	if m.statsBusy || p == nil || !p.Running() || !p.Docker() {
		return nil
	}
	m.statsBusy = true
	profile := *p
	return func() tea.Msg {
		s, err := m.backend.Stats(m.ctx, profile)
		return statsMsg{profile: profile.Name, stats: s, err: err}
	}
}
func (m *Model) refresh() tea.Cmd {
	if m.refreshBusy {
		return nil
	}
	m.refreshBusy = true
	m.refreshID++
	id := m.refreshID
	name := m.profile
	ctx, cancel := context.WithCancel(m.ctx)
	m.cancelRefresh = cancel
	return func() tea.Msg {
		defer cancel()
		ps, err := m.backend.Profiles(ctx)
		msg := refreshMsg{id: id, profiles: ps, profile: name, err: err}
		if err != nil {
			return msg
		}
		var p *engine.Profile
		for i := range ps {
			if ps[i].Name == name {
				p = &ps[i]
			}
		}
		if p == nil {
			p = pickProfile(ps)
		}
		if p == nil {
			return msg
		}
		msg.profile = p.Name
		if !p.Running() || !p.Docker() {
			return msg
		}
		msg.containers, msg.err = m.backend.Containers(ctx, *p)
		return msg
	}
}
func (m *Model) switchProfile(name string) tea.Cmd {
	if name == m.profile {
		return nil
	}
	m.note(statusInfo, "Switched to "+name)
	m.profile = name
	m.selected = 0
	m.images, m.layers, m.layersFor, m.imageSel = nil, nil, "", 0
	m.containers = nil
	m.stats = nil
	m.sampled = time.Time{}
	m.query = ""
	m.errorText = ""
	m.logs = nil
	m.partial = ""
	m.logBytes = 0
	m.logQuery = ""
	m.stopLogs()
	m.syncLogView()
	if m.cancelRefresh != nil {
		m.cancelRefresh()
	}
	m.refreshBusy = false
	return m.refresh()
}
func (m *Model) stopLogs() {
	m.logEpoch++
	if m.cancelLogs != nil {
		m.cancelLogs()
		m.cancelLogs = nil
	}
	if m.reader != nil {
		m.reader.Close()
		m.reader = nil
	}
}
func (m *Model) selectLogs(reset bool) tea.Cmd {
	m.stopLogs()
	if reset {
		m.logs = nil
		m.partial = ""
		m.logBytes = 0
		m.logQuery = ""
		m.follow = true
		m.logEnded = false
		m.logViewport.GotoTop()
		m.syncLogView()
	}
	c, p := m.container(), m.current()
	if c == nil || p == nil || !p.Running() || !p.Docker() || !m.follow {
		return nil
	}
	epoch := m.logEpoch
	profile, id := *p, c.ID
	since := ""
	if !reset {
		for i := len(m.logs) - 1; i >= 0; i-- {
			stamp, _, _ := strings.Cut(m.logs[i], " ")
			if t, err := time.Parse(time.RFC3339Nano, stamp); err == nil {
				since = t.Add(time.Nanosecond).Format(time.RFC3339Nano)
				break
			}
		}
	}
	ctx, cancel := context.WithCancel(m.ctx)
	m.cancelLogs = cancel
	m.logEnded = false
	return func() tea.Msg {
		r, err := m.backend.Logs(ctx, profile, id, since)
		if ctx.Err() != nil && r != nil {
			r.Close()
			r = nil
		}
		return logOpened{epoch: epoch, reader: r, err: err}
	}
}
func readLogs(epoch uint64, r io.Reader) tea.Cmd {
	return func() tea.Msg {
		buf := make([]byte, 8192)
		n, err := r.Read(buf)
		return logData{epoch: epoch, data: string(buf[:n]), err: err}
	}
}
func (m *Model) appendLogs(data string, done bool) {
	parts := strings.Split(m.partial+data, "\n")
	m.partial = parts[len(parts)-1]
	if done && m.partial != "" {
		parts = append(parts, "")
		m.partial = ""
	}
	for _, line := range parts[:len(parts)-1] {
		line = clean(line)
		m.logs = append(m.logs, line)
		m.logBytes += len(line)
	}
	if len(m.partial) > 256<<10 {
		m.partial = m.partial[len(m.partial)-(256<<10):]
		m.note(statusWarn, "Trimmed a very long log line")
	}
	for len(m.logs) > 5000 || m.logBytes > 4<<20 {
		m.logBytes -= len(m.logs[0])
		m.logs[0] = ""
		m.logs = m.logs[1:]
	}
	m.syncLogView()
}
func (m *Model) logText() string {
	var lines []string
	for _, line := range m.logs {
		if m.logQuery != "" && !strings.Contains(strings.ToLower(line), strings.ToLower(m.logQuery)) {
			continue
		}
		if !m.prefs.Timestamps {
			stamp, rest, ok := strings.Cut(line, " ")
			if ok {
				if _, err := time.Parse(time.RFC3339Nano, stamp); err == nil {
					line = rest
				}
			}
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}
func (m *Model) syncLogView() {
	offset := m.logViewport.YOffset
	m.logRows = m.logRows[:0]
	var text []string
	for _, line := range strings.Split(m.logText(), "\n") {
		parts := []string{line}
		if m.prefs.Wrap {
			parts = strings.Split(ansi.Hardwrap(line, max(1, m.logViewport.Width), true), "\n")
		}
		for i, part := range parts {
			m.logRows = append(m.logRows, logRow{text: part, cont: i > 0})
			text = append(text, part)
		}
	}
	m.sel = selection{}
	m.logViewport.SetContent(strings.Join(text, "\n"))
	if m.follow {
		m.logViewport.GotoBottom()
	} else {
		m.logViewport.SetYOffset(offset)
	}
}

// wheelQuiet is the gap that ends a trackpad's momentum stream; coasting
// events arrive every ~16 ms, deliberate scrolls start after a pause.
const wheelQuiet = 200 * time.Millisecond

func (m *Model) wheelLogs(msg tea.MouseMsg) tea.Cmd {
	now := time.Now()
	coasting := now.Sub(m.lastWheel) < wheelQuiet
	m.lastWheel = now
	if m.wheelMuted && coasting {
		return nil // momentum from a scroll the user already ended with Esc or f
	}
	m.wheelMuted = false
	return m.scrollLogs(msg.Button == tea.MouseButtonWheelUp, func() tea.Cmd {
		var cmd tea.Cmd
		m.logViewport, cmd = m.logViewport.Update(msg)
		return cmd
	})
}

// scrollLogs moves the logs viewport. Scrolling up pauses so the view holds
// still; scrolling down while live has nothing to show, and scrolling back
// down to the newest line goes live again.
func (m *Model) scrollLogs(up bool, move func() tea.Cmd) tea.Cmd {
	if m.follow {
		if !up {
			return nil
		}
		m.pauseLogs()
	}
	cmd := move()
	if !up && m.logViewport.AtBottom() && m.container() != nil {
		return tea.Batch(cmd, m.resumeLogs())
	}
	return cmd
}

func (m *Model) resumeLogs() tea.Cmd {
	m.follow = true
	m.note(statusOK, "Logs resumed")
	return m.selectLogs(false)
}

func (m *Model) pauseLogs() {
	if m.follow {
		m.note(statusInfo, "Logs paused")
		m.follow = false
		m.stopLogs()
		m.appendLogs("", true)
	}
}
func clean(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			if r == '\t' {
				return ' '
			}
			return -1
		}
		return r
	}, ansi.Strip(s))
}
func (m *Model) savePrefs() {
	if m.prefsPath == "" {
		return
	}
	data, err := json.MarshalIndent(m.prefs, "", "  ")
	if err == nil {
		err = engine.AtomicWrite(m.prefsPath, data, 0600)
	}
	if err != nil {
		m.errorText = "Preferences were not saved: " + err.Error()
	}
}
func (m *Model) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	wasProgress := m.modal == "progress"
	model, cmd := m.update(message)
	// Release the mouse in the output view so the terminal can drag-select text.
	if isProgress := m.modal == "progress"; isProgress != wasProgress {
		toggle := tea.EnableMouseCellMotion
		if isProgress {
			toggle = tea.DisableMouse
		}
		cmd = tea.Batch(cmd, toggle)
	}
	if m.welcoming() && !m.waving {
		m.waving = true
		cmd = tea.Batch(cmd, waveTick())
	}
	return model, cmd
}
func (m *Model) update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := message.(type) {
	case waveMsg:
		m.waving = m.welcoming()
		if m.waving {
			m.wave++
			return m, waveTick()
		}
		return m, nil
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.resize()
		m.syncLogView()
	case tickMsg:
		var retry tea.Cmd
		if !m.demo && m.follow && m.logEnded && time.Since(m.logRetry) > 4*time.Second {
			c := m.container()
			if c != nil && c.Active() {
				retry = m.selectLogs(false)
			}
		}
		return m, tea.Batch(m.refresh(), retry, tick())
	case refreshMsg:
		if msg.id != m.refreshID {
			return m, nil
		}
		m.refreshBusy = false
		if !m.onImages() {
			m.settling = false // on the images tab, the image list settles it
		}
		old := m.selectedKey()
		oldProfile := m.profile
		if msg.profiles != nil {
			m.profiles = msg.profiles
			m.profile = msg.profile
		}
		if oldProfile != m.profile {
			m.containers = nil
			m.stats = nil
			m.query = ""
			m.selected = 0
			m.stopLogs()
			m.logs = nil
			m.partial = ""
			m.logBytes = 0
			m.logQuery = ""
			m.syncLogView()
			old = ""
		}
		if msg.err != nil {
			m.errorText = clean(msg.err.Error())
			m.note(statusErr, "Can't reach Colima · retrying")
			m.offline = true
			return m, nil
		}
		m.containers = msg.containers
		m.restoreSelection(old)
		if old == "" {
			for i, r := range m.rows() {
				if r.Container != nil {
					m.selected = i
					break
				}
			}
		}
		if m.offline {
			m.offline = false
			m.note(statusOK, "Reconnected to Colima")
		} else if m.statusKind == statusPlain {
			m.status = "" // connected; the corner is for what the user does
		}
		stats := m.fetchStats()
		if m.onImages() {
			stats = tea.Batch(stats, m.fetchImages())
		}
		if oldProfile != m.profile || old != m.selectedKey() {
			return m, tea.Batch(m.selectLogs(true), stats)
		}
		return m, stats
	case imagesMsg:
		return m, m.gotImages(msg)
	case historyMsg:
		if msg.id == m.layersFor {
			m.layers = msg.layers
		}
	case statsMsg:
		m.statsBusy = false
		if msg.profile != m.profile {
			return m, nil
		}
		// Keep metrics optional: a metrics error must not hide the container list.
		m.stats = msg.stats
		m.sampled = time.Now()
		m.statsError = ""
		if msg.err != nil {
			m.statsError = clean(msg.err.Error())
		}
	case tea.KeyMsg:
		return m, m.key(msg)
	case tea.MouseMsg:
		if m.modal != "" || m.searchMode != "" {
			return m, nil
		}
		if m.onImages() { // no logs here; the wheel scrolls the layer list
			if tea.MouseEvent(msg).IsWheel() && m.overDetails(msg.X) {
				m.focus = 1
				step := map[bool]int{true: -1, false: 1}[msg.Button == tea.MouseButtonWheelUp]
				m.layerTop = min(max(0, m.layerTop+step), max(0, len(m.layers)-1))
			}
			return m, nil
		}
		if cmd, ok := m.selectMouse(msg); ok {
			return m, cmd
		}
		if tea.MouseEvent(msg).IsWheel() && m.overDetails(msg.X) {
			m.focus = 1
			return m, m.wheelLogs(msg)
		}
	case logOpened:
		if msg.epoch != m.logEpoch {
			if msg.reader != nil {
				msg.reader.Close()
			}
			return m, nil
		}
		if msg.err != nil {
			m.note(statusWarn, "Logs unavailable: "+clean(msg.err.Error()))
			m.logEnded = true
			m.logRetry = time.Now()
			return m, nil
		}
		if msg.reader != nil {
			m.reader = msg.reader
			return m, readLogs(msg.epoch, msg.reader)
		}
	case logData:
		if msg.epoch != m.logEpoch {
			return m, nil
		}
		m.appendLogs(msg.data, msg.err != nil)
		if msg.err != nil {
			if m.reader != nil {
				m.reader.Close()
				m.reader = nil
			}
			m.logEnded = true
			m.logRetry = time.Now()
			if msg.err != io.EOF {
				m.note(statusWarn, "Log stream ended")
			}
			return m, nil
		}
		if m.reader != nil {
			return m, readLogs(msg.epoch, m.reader)
		}
	case progressMsg:
		for _, line := range strings.Split(string(msg), "\n") {
			m.progress = append(m.progress, clean(line))
		}
		if trim := len(m.progress) - maxProgress; trim > 0 {
			m.progress = m.progress[trim:]
			m.progressTop = max(0, m.progressTop-trim)
		}
		return m, waitOperation(m.opEvents)
	case operationDone:
		m.busy = false
		m.settling = true
		if m.opCancel != nil {
			m.opCancel()
			m.opCancel = nil
		}
		m.progress = append(m.progress, " ")
		_, done, verb := opText(m.running)
		if msg.err != nil {
			m.errorText = clean(msg.err.Error())
			m.progress = append(m.progress, "FAILED: "+m.errorText)
			m.opFailed = true
			if m.canceled {
				m.note(statusWarn, "Canceled: "+verb)
			} else {
				m.note(statusErr, "Couldn't "+verb)
			}
		} else {
			m.errorText = ""
			m.progress = append(m.progress, "Completed successfully.")
		}
		m.canceled = false
		if msg.kind == "create" && msg.err == nil {
			cmd := m.switchProfile(msg.profile)
			m.note(statusOK, done)
			return m, cmd
		}
		if msg.err == nil {
			m.note(statusOK, done)
		}
		if m.cancelRefresh != nil {
			m.cancelRefresh()
		}
		m.refreshBusy = false
		return m, tea.Batch(m.refresh(), m.selectLogs(false))
	case shellReady:
		m.handoff = false
		if msg.err != nil {
			m.errorText = "Shell: " + msg.err.Error()
			m.note(statusErr, "Couldn't open shell")
			return m, m.selectLogs(false)
		}
		return m, tea.ExecProcess(msg.cmd, func(err error) tea.Msg { return shellDone{err: err} })
	case shellDone:
		if msg.err != nil {
			m.errorText = "Shell: " + msg.err.Error()
			m.note(statusWarn, "Shell exited with an error")
		} else {
			m.note(statusInfo, "Left "+m.shellName+" shell")
		}
		// Bubble Tea restores the alt screen after a handed-off process but not
		// mouse reporting, so switch it back on for wheel scroll and drag-select.
		return m, tea.Batch(m.refresh(), m.selectLogs(false), tea.EnableMouseCellMotion)
	case clipDone:
		if msg.err != nil {
			m.errorText = "Clipboard: " + msg.err.Error()
			m.note(statusErr, "Copy failed")
		} else {
			m.note(statusOK, msg.label)
		}
	}
	return m, nil
}
func (m *Model) resize() {
	width := m.width - m.leftWidth()
	if m.narrow() {
		width = m.width
	}
	m.logViewport.Width = max(1, width-4)
	m.logViewport.Height = max(1, m.height-5-m.detailRows())
	m.search.Width = max(10, m.width-25)
	for i := range m.form {
		m.form[i].Width = max(1, min(30, m.width-28))
	}
}

func (m *Model) narrow() bool { return m.width < 80 }
func (m *Model) detailRows() int {
	if m.height < 24 {
		return 2
	}
	return infoRows
}
func (m *Model) overDetails(x int) bool {
	if m.narrow() {
		return m.focus == 1
	}
	return x >= m.leftWidth()
}

const maxProgress = 5000

// progressRows is how many output lines fit between the header and hint bar.
func (m *Model) progressRows() int { return max(1, m.height-4) }

// progressStart is the first visible output line; unpinned output follows the tail.
func (m *Model) progressStart() int {
	last := max(0, len(m.progress)-m.progressRows())
	if !m.progressPinned {
		return last
	}
	return min(m.progressTop, last)
}

func (m *Model) leftWidth() int { return min(46, max(28, m.width/3)) }
func human(n int64) string {
	if n >= 1<<30 {
		return fmt.Sprintf("%.1f GiB", float64(n)/(1<<30))
	}
	if n >= 1<<20 {
		return fmt.Sprintf("%.0f MiB", float64(n)/(1<<20))
	}
	return fmt.Sprintf("%d B", n)
}
