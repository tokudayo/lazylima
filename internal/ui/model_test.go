package ui

import (
	"context"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"lazylima/internal/engine"
)

func fixture(t *testing.T) *Model {
	t.Helper()
	b := engine.NewDemo()
	m := New(b, true)
	m.clipboard = func(string) error { return nil } // never touch the real clipboard
	t.Cleanup(m.Close)
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	ps, _ := b.Profiles(context.Background())
	cs, _ := b.Containers(context.Background(), ps[0])
	stats, _ := b.Stats(context.Background(), ps[0])
	m.profile = "default"
	m.profiles = ps
	m.containers = cs
	m.stats = stats
	m.sampled = time.Now()
	return m
}
func press(m *Model, k string) tea.Cmd {
	var msg tea.KeyMsg
	switch k {
	case "enter":
		msg.Type = tea.KeyEnter
	case "esc":
		msg.Type = tea.KeyEsc
	case "tab":
		msg.Type = tea.KeyTab
	case "down":
		msg.Type = tea.KeyDown
	case "up":
		msg.Type = tea.KeyUp
	case "space":
		msg.Type = tea.KeySpace
	default:
		msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
	}
	_, cmd := m.Update(msg)
	return cmd
}
func selectContainer(m *Model, name string) {
	for i, r := range m.rows() {
		if r.Container != nil && r.Container.Names == name {
			m.selected = i
			return
		}
	}
}
func TestGroupingAndStandaloneContainers(t *testing.T) {
	m := fixture(t)
	rows := m.rows()
	if rows[0].Group != "shop" || rows[0].Container != nil {
		t.Fatal("missing Compose project")
	}
	standalone := 0
	for _, r := range rows {
		if r.Container == nil && r.Group == "standalone" {
			t.Fatal("standalone containers should not require expansion")
		}
		if r.Container != nil && r.Group == "" {
			standalone++
		}
	}
	if standalone != 2 {
		t.Fatal(standalone)
	}
	press(m, "enter")
	if len(m.rows()) != 3 {
		t.Fatal("collapse failed")
	}
}

func TestComposeStackShortcuts(t *testing.T) {
	for _, filtered := range []bool{false, true} {
		for _, key := range []string{"s", "x"} {
			m := fixture(t)
			// One service needs starting, the other needs stopping.
			m.containers[1].State = "exited"
			if filtered {
				m.query = "shop-api"
			} else {
				m.collapsed["shop"] = true
			}
			cmd := press(m, key)
			kind := "start-project"
			if key == "x" {
				kind = "stop-project"
			}
			if cmd == nil || !m.busy || m.running.Kind != kind || m.running.Project != "shop" || m.running.Profile.Name != "default" {
				t.Fatalf("stack shortcut %s (filtered=%v): %+v", key, filtered, m.running)
			}
			if press(m, key) != nil {
				t.Fatal("started a second operation while busy")
			}
		}
	}
	m := fixture(t)
	if press(m, "s") != nil || m.busy {
		t.Fatal("start should be disabled for a running stack")
	}
	selectContainer(m, "redis")
	if press(m, "x") == nil || m.running.Kind != "stop-container" || m.running.Container.Names != "redis" {
		t.Fatal("container shortcut changed")
	}
}
func TestSearchModes(t *testing.T) {
	m := fixture(t)
	press(m, "/")
	press(m, "q")
	if m.searchMode != "containers" || m.query != "q" {
		t.Fatal("search key escaped into shortcut handler")
	}
	press(m, "esc")
	if m.query != "" {
		t.Fatal("esc did not clear the search")
	}
	press(m, "/")
	press(m, "re")
	press(m, "enter") // applied
	press(m, "/")
	press(m, "d")
	press(m, "esc")
	if m.query != "" || m.status != "Search cleared" {
		t.Fatalf("esc restored a previous search instead of clearing: %q", m.query)
	}
	m.focus = 1
	press(m, "/")
	press(m, "error")
	if m.logQuery != "error" || m.follow {
		t.Fatal("log search should pause streaming")
	}
	press(m, "enter")
	if m.searchMode != "" {
		t.Fatal("search did not apply")
	}
}
func TestRefreshAndLogMessagesCannotCrossProfiles(t *testing.T) {
	m := fixture(t)
	m.refreshID = 9
	m.Update(refreshMsg{id: 8, profile: "sandbox", profiles: []engine.Profile{{Name: "sandbox"}}})
	if m.profile != "default" {
		t.Fatal("applied stale refresh")
	}
	m.logEpoch = 4
	m.Update(logData{epoch: 3, data: "foreign profile log\n"})
	if len(m.logs) != 0 {
		t.Fatal("applied stale log")
	}
	m.Update(logData{epoch: 4, data: "correct log\n", err: io.EOF})
	if len(m.logs) != 1 || !m.logEnded {
		t.Fatal("current stream not applied")
	}
}
func TestDeleteRequiresStoppedContainerAndDefaultsToCancel(t *testing.T) {
	m := fixture(t)
	selectContainer(m, "redis")
	press(m, "d")
	if m.modal != "" {
		t.Fatal("running deletion was enabled")
	}
	selectContainer(m, "mailpit")
	press(m, "d")
	if m.modal != "confirm" || m.pending.Container.Names != "mailpit" {
		t.Fatal("delete did not capture exact target")
	}
	press(m, "enter")
	if m.busy || m.modal != "" {
		t.Fatal("confirmation did not default to cancel")
	}
}
func TestCreateAndConfigureValidation(t *testing.T) {
	m := fixture(t)
	press(m, "n")
	m.form[0].SetValue("work")
	press(m, "enter")
	if m.modal != "confirm" || m.pending.Kind != "create" || m.pending.Resources.Name != "work" {
		t.Fatal("create workflow failed")
	}
	press(m, "esc")
	m.dispatch("configure") // menu-only now; no hotkey
	m.form[3].SetValue("99")
	press(m, "enter")
	if m.modal != "form" || !strings.Contains(m.errorText, "shrink") {
		t.Fatal("disk shrink accepted")
	}
	m.form[3].SetValue("120")
	press(m, "enter")
	if !strings.Contains(m.confirmText, "Restart") || m.pending.Kind != "configure" {
		t.Fatal("restart scope not explained")
	}
}
func TestLogsBoundedAndSanitized(t *testing.T) {
	m := fixture(t)
	m.appendLogs(strings.Repeat("2026-01-01T00:00:00Z \x1b]52;c;malicious\ahello\n", 6000), false)
	if len(m.logs) > 5000 || m.logBytes > 4<<20 {
		t.Fatal("unbounded logs")
	}
	if strings.Contains(m.logText(), "\x1b") || strings.Contains(m.logText(), "malicious") {
		t.Fatal("terminal escape injection")
	}
	m.appendLogs(strings.Repeat("x", 300<<10), false)
	if len(m.partial) > 256<<10 {
		t.Fatal("unbounded partial log")
	}
}
func TestSelectionSurvivesRefreshReordering(t *testing.T) {
	m := fixture(t)
	selectContainer(m, "redis")
	key := m.selectedKey()
	cs := append([]engine.Container(nil), m.containers...)
	cs[0], cs[2] = cs[2], cs[0]
	m.refreshID = 3
	m.Update(refreshMsg{id: 3, profile: m.profile, profiles: m.profiles, containers: cs})
	if m.selectedKey() != key {
		t.Fatal("refresh moved selection")
	}
}
func TestViewsFitTerminal(t *testing.T) {
	m := fixture(t)
	selectContainer(m, "redis")
	m.progress = []string{"Starting VM…", "Completed successfully."}
	m.confirmTitle = "Delete container?"
	m.confirmText = strings.Repeat("Long confirmation line including a volume name\n", 30)
	for _, size := range [][2]int{{40, 12}, {60, 18}, {79, 23}, {80, 12}, {80, 24}, {100, 30}, {160, 50}} {
		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		for _, modal := range []string{"", "actions", "profiles", "container", "form", "confirm", "progress"} {
			if modal == "form" {
				m.openForm(true)
			}
			m.modal = modal
			text := m.View()
			lines := strings.Split(text, "\n")
			if len(lines) > size[1] {
				t.Errorf("%s %v: height %d", modal, size, len(lines))
			}
			for _, line := range lines {
				if w := ansi.StringWidth(line); w > size[0] {
					t.Errorf("%s %v: width %d", modal, size, w)
					break
				}
			}
		}
	}
}

func TestCompactLayoutSwitchesPanesAndResizesLogs(t *testing.T) {
	m := fixture(t)
	selectContainer(m, "redis")
	m.logs = []string{strings.Repeat("z", 90)}
	m.Update(tea.WindowSizeMsg{Width: 60, Height: 16})
	if m.logViewport.Width != 56 || m.logViewport.Height != 9 {
		t.Fatalf("compact viewport: %dx%d", m.logViewport.Width, m.logViewport.Height)
	}
	if m.inLogs(5, 6) {
		t.Fatal("hidden logs accept mouse selection")
	}
	if !strings.Contains(ansi.Strip(m.View()), "l details") || strings.Contains(ansi.Strip(m.View()), "zzzz") {
		t.Fatal("list pane did not use the compact layout")
	}
	press(m, "l")
	if !m.inLogs(5, 6) || !strings.Contains(ansi.Strip(m.View()), "zzzz") || !strings.Contains(ansi.Strip(m.View()), "h list") {
		t.Fatal("full-width logs not reachable")
	}
	if len(m.logRows) != 2 {
		t.Fatal("logs did not rewrap at compact width")
	}
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	if m.logsLeft() != m.leftWidth()+2 || m.logsTop() != 9 || m.logViewport.Width != 76 {
		t.Fatal("wide layout did not restore")
	}
}

func TestCompactMenusKeepSelectionAndConfirmationVisible(t *testing.T) {
	m := fixture(t)
	m.Update(tea.WindowSizeMsg{Width: 40, Height: 12})
	press(m, "?")
	m.modalIndex = len(m.filteredActions()) - 1
	if !strings.Contains(ansi.Strip(m.View()), m.filteredActions()[m.modalIndex].Label) {
		t.Fatal("last action is hidden")
	}
	m.confirm(engine.Operation{}, "Delete?", strings.Repeat("Long warning text ", 80))
	view := ansi.Strip(m.View())
	if !strings.Contains(view, "cancel") || !strings.Contains(view, "confirm") {
		t.Fatal("confirmation choices are hidden")
	}
	m.openForm(true)
	m.errorText = "Enter numeric values"
	if !strings.Contains(ansi.Strip(m.View()), m.errorText) {
		t.Fatal("form error is hidden")
	}
}

func TestSmallViewsFitForDetailsImagesAndWelcome(t *testing.T) {
	m := fixture(t)
	m.images, _ = m.backend.Images(context.Background(), *m.current())
	for _, size := range [][2]int{{20, 6}, {40, 12}, {60, 18}, {80, 12}} {
		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		for _, tab := range []string{"containers", "images"} {
			m.tab = tab
			for _, focus := range []int{0, 1} {
				m.focus = focus
				view := m.View()
				if lines := strings.Split(view, "\n"); len(lines) > size[1] {
					t.Fatalf("%v %s focus %d exceeds height", size, tab, focus)
				}
				for _, line := range strings.Split(view, "\n") {
					if ansi.StringWidth(line) > size[0] {
						t.Fatalf("%v %s focus %d exceeds width", size, tab, focus)
					}
				}
			}
		}
	}
	m.profiles, m.containers = nil, nil
	m.Update(tea.WindowSizeMsg{Width: 40, Height: 12})
	for _, line := range strings.Split(m.View(), "\n") {
		if ansi.StringWidth(line) > 40 {
			t.Fatal("welcome exceeds width")
		}
	}
}
func TestOperationCanCompleteAndResumeLogs(t *testing.T) {
	m := fixture(t)
	selectContainer(m, "redis")
	cmd := press(m, "x")
	if cmd == nil || !m.busy || m.modal != "" { // runs in the background
		t.Fatal("operation did not start")
	}
	msg := unbatch(cmd())
	for i := 0; i < 10; i++ {
		_, next := m.Update(msg)
		if _, done := msg.(operationDone); done {
			if m.busy || next == nil {
				t.Fatal("operation did not refresh and resume")
			}
			return
		}
		if next == nil {
			t.Fatal("lost operation event loop")
		}
		msg = unbatch(next())
	}
	t.Fatal("operation never completed")
}
func TestActionMenuFiltersAndUsesRegistry(t *testing.T) {
	m := fixture(t)
	press(m, "?")
	press(m, "create")
	items := m.filteredActions()
	if len(items) != 1 || items[0].ID != "create" {
		t.Fatal("menu filtering failed")
	}
	press(m, "enter")
	if m.modal != "form" || !m.formNew {
		t.Fatal("menu did not run registered action")
	}
}

func TestDisappearingProfileNeverRetainsForeignContainers(t *testing.T) {
	m := fixture(t)
	selectContainer(m, "redis")
	m.logs = []string{"old profile logs"}
	m.refreshID = 7
	m.Update(refreshMsg{id: 7, profile: "replacement", profiles: []engine.Profile{{Name: "replacement", Status: "Running", Runtime: "docker"}}, err: fmt.Errorf("replacement daemon unavailable")})
	if m.profile != "replacement" || len(m.containers) != 0 || len(m.logs) != 0 || m.container() != nil {
		t.Fatal("retained state from the previous profile")
	}
}

func TestShellPreparationFailureRestoresInteraction(t *testing.T) {
	m := fixture(t)
	selectContainer(m, "redis")
	cmd := press(m, "e")
	if !m.handoff || cmd == nil {
		t.Fatal("shell handoff did not start")
	}
	m.Update(cmd())
	if m.handoff || !strings.Contains(m.errorText, "demo mode") {
		t.Fatal("shell failure left the interface blocked")
	}
}

// unbatch runs batched commands and returns the first app message, skipping
// Bubble Tea's own terminal-control messages such as mouse toggles.
func unbatch(msg tea.Msg) tea.Msg {
	b, ok := msg.(tea.BatchMsg)
	if !ok {
		return msg
	}
	for _, c := range b {
		if c == nil {
			continue
		}
		if r := unbatch(c()); r != nil && !strings.HasPrefix(fmt.Sprintf("%T", r), "tea.") {
			return r
		}
	}
	return nil
}
func TestPickProfileFollowsRunningColima(t *testing.T) {
	ps := []engine.Profile{{Name: "alpha", Status: "Stopped"}, {Name: "default", Status: "Stopped"}, {Name: "work", Status: "Running"}}
	if p := pickProfile(ps); p.Name != "work" {
		t.Fatalf("picked %s, want the running profile", p.Name)
	}
	ps[1].Status = "Running"
	if p := pickProfile(ps); p.Name != "default" {
		t.Fatalf("picked %s, want running default", p.Name)
	}
	ps[1].Status, ps[2].Status = "Stopped", "Stopped"
	if p := pickProfile(ps); p.Name != "default" {
		t.Fatalf("picked %s, want default when nothing runs", p.Name)
	}
	if pickProfile(nil) != nil {
		t.Fatal("picked a profile from an empty list")
	}
}
func TestWelcomeWithoutProfiles(t *testing.T) {
	m := fixture(t)
	m.profiles, m.profile, m.containers = []engine.Profile{}, "", nil
	if _, cmd := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30}); cmd == nil || !m.waving {
		t.Fatal("wave animation did not start")
	}
	if !strings.Contains(ansi.Strip(m.View()), "create your first profile") {
		t.Fatal("welcome call to action missing")
	}
	press(m, "enter")
	if m.modal != "form" || !m.formNew {
		t.Fatal("enter did not open the create form")
	}
}
func TestProgressScrollsAndFollows(t *testing.T) {
	m := fixture(t)
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m.modal = "progress"
	for i := 0; i < 100; i++ {
		m.progress = append(m.progress, fmt.Sprintf("line %d", i))
	}
	tail := m.progressStart()
	press(m, "up")
	if !m.progressPinned || m.progressStart() != tail-1 {
		t.Fatal("up did not scroll back")
	}
	m.progress = append(m.progress, "new line")
	if m.progressStart() != tail-1 {
		t.Fatal("new output moved a scrolled-back view")
	}
	press(m, "G")
	if m.progressPinned || m.progressStart() != len(m.progress)-m.progressRows() {
		t.Fatal("end did not resume following")
	}
	if !strings.Contains(ansi.Strip(m.View()), "new line") {
		t.Fatal("tail not visible")
	}
}
func drag(m *Model, from, to [2]int) {
	m.Update(tea.MouseMsg{X: from[0], Y: from[1], Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	m.Update(tea.MouseMsg{X: to[0], Y: to[1], Action: tea.MouseActionMotion, Button: tea.MouseButtonLeft})
	if _, cmd := m.Update(tea.MouseMsg{X: to[0], Y: to[1], Action: tea.MouseActionRelease, Button: tea.MouseButtonLeft}); cmd != nil {
		m.Update(cmd()) // finish the copy so the status reports it
	}
}
func TestDragSelectsLogs(t *testing.T) {
	m := fixture(t)
	selectContainer(m, "redis")
	m.prefs.Wrap = true
	m.logs = []string{"alpha one", "bravo two", strings.Repeat("x", m.logViewport.Width) + "tail", "delta"}
	m.syncLogView()
	m.logViewport.GotoTop()
	x, y := m.logsLeft(), m.logsTop()
	drag(m, [2]int{x + 6, y}, [2]int{x + 4, y + 1})
	if m.follow || m.focus != 1 {
		t.Fatal("selecting did not pause and focus logs")
	}
	if got := m.selectedText(); got != "one\nbravo" {
		t.Fatalf("selected %q", got)
	}
	if !strings.Contains(m.status, "Copied 2 lines") {
		t.Fatalf("status %q", m.status)
	}
	if !strings.Contains(m.View(), selected.Render("one")) {
		t.Fatal("selection not highlighted")
	}
	drag(m, [2]int{x, y + 2}, [2]int{x + 3, y + 3})
	if got := m.selectedText(); got != strings.Repeat("x", m.logViewport.Width)+"tail" {
		t.Fatalf("wrapped line not rejoined: %q", got)
	}
	press(m, "esc")
	if m.sel.shown {
		t.Fatal("esc did not clear the selection")
	}
	m.Update(tea.MouseMsg{X: x + 2, Y: y, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	m.Update(tea.MouseMsg{X: x + 2, Y: y, Action: tea.MouseActionRelease, Button: tea.MouseButtonLeft})
	if m.sel.shown {
		t.Fatal("a plain click left a selection")
	}
}
func TestVimKeysAndEscResume(t *testing.T) {
	m := fixture(t)
	selectContainer(m, "redis")
	press(m, "l")
	if m.focus != 1 {
		t.Fatal("l did not focus logs")
	}
	press(m, "k")
	if m.follow {
		t.Fatal("scrolling logs did not pause them")
	}
	press(m, "esc")
	if !m.follow {
		t.Fatal("esc did not resume paused logs")
	}
	press(m, "h")
	if m.focus != 0 {
		t.Fatal("h did not focus containers")
	}
	for _, a := range m.actions() {
		switch a.Key {
		case "C", "E", "l", "L", "u", "N", "R", "g", "T", "X", "i":
			t.Fatalf("stale binding %+v", a)
		}
		if a.ID == "stop-profile" && a.Key != "" || a.ID == "stop-container" && a.Key != "x" || a.ID == "restart-container" && a.Key != "r" || a.ID == "timestamps" && a.Key != "t" {
			t.Fatalf("wrong stop binding %+v", a)
		}
		if a.ID == "advanced" {
			t.Fatalf("stale binding %+v", a)
		}
	}
}
func TestEnterOpensContainerActions(t *testing.T) {
	m := fixture(t)
	selectContainer(m, "mailpit")
	press(m, "enter")
	if m.modal != "container" {
		t.Fatal("enter did not open the container dialog")
	}
	ids := map[string]bool{}
	for _, a := range m.containerActions() {
		ids[a.ID] = true
	}
	if !ids["start-container"] || !ids["delete-container"] || ids["stop-container"] || ids["shell"] {
		t.Fatalf("wrong actions for a stopped container: %v", ids)
	}
	press(m, "d") // hotkeys work inside the dialog
	if m.modal != "confirm" || m.pending.Container.Names != "mailpit" {
		t.Fatal("dialog hotkey did not run the action")
	}
	press(m, "esc")
	selectContainer(m, "redis")
	press(m, "enter")
	if items := m.containerActions(); items[0].ID != "stop-container" || items[1].ID != "restart-container" {
		t.Fatalf("unexpected order %+v", items)
	}
	press(m, "down")
	press(m, "enter")
	if m.modal != "" || !m.busy || !strings.HasPrefix(m.progress[0], "restart-container") {
		t.Fatal("enter did not run the highlighted action")
	}
}
func TestShellExitRestoresMouse(t *testing.T) {
	m := fixture(t)
	_, cmd := m.Update(shellDone{})
	var names []string
	var walk func(tea.Cmd)
	walk = func(c tea.Cmd) {
		if c == nil {
			return
		}
		switch msg := c().(type) {
		case tea.BatchMsg:
			for _, c := range msg {
				walk(c)
			}
		default:
			names = append(names, fmt.Sprintf("%T", msg))
		}
	}
	walk(cmd)
	if !strings.Contains(strings.Join(names, " "), "enableMouseCellMotion") {
		t.Fatalf("mouse not re-enabled after the shell exits: %v", names)
	}
}
func TestNoLetterBoundInBothCases(t *testing.T) {
	m := fixture(t)
	bound := map[string]string{}
	for _, a := range m.actions() {
		if a.Key == "" {
			continue
		}
		if other, ok := bound[strings.ToLower(a.Key)]; ok {
			t.Fatalf("%q (%s) and %q share a letter", a.Key, a.ID, other)
		}
		bound[strings.ToLower(a.Key)] = a.Key
	}
}
func TestEnterStartsStoppedProfile(t *testing.T) {
	m := fixture(t)
	m.switchProfile("sandbox") // demo profile that is stopped
	m.containers = nil
	press(m, "enter")
	if !m.busy || m.modal != "" || !strings.HasPrefix(m.progress[0], "start · sandbox") {
		t.Fatalf("enter did not start the stopped profile: busy=%v modal=%q", m.busy, m.modal)
	}
}
func TestCopyFollowsFocusedPane(t *testing.T) {
	m := fixture(t)
	selectContainer(m, "redis")
	m.logs = nil
	for _, k := range []string{"c", "y"} {
		m.focus = 0
		if press(m, k) == nil {
			t.Fatalf("%s on the container list did not copy details", k)
		}
		m.focus, m.status = 1, ""
		if press(m, k) != nil || m.status != "" { // no logs yet: copying logs does nothing, quietly
			t.Fatalf("%s on the logs pane did not route to copying logs", k)
		}
	}
	m.logs = []string{"hello"}
	if press(m, "y") == nil {
		t.Fatal("y on the logs pane did not copy logs")
	}
}
func TestEscCancelsRunningOperation(t *testing.T) {
	m := fixture(t)
	selectContainer(m, "redis")
	press(m, "x")
	if !m.busy || m.modal != "" {
		t.Fatal("operation did not start in the background")
	}
	press(m, "o")
	if m.modal != "progress" {
		t.Fatal("o did not open the output")
	}
	press(m, "esc")
	if m.modal != "progress" || !strings.Contains(strings.Join(m.progress, "\n"), "Canceling command") {
		t.Fatal("esc did not cancel the running operation")
	}
	press(m, "esc")
	if n := strings.Count(strings.Join(m.progress, "\n"), "Canceling command"); n != 1 {
		t.Fatalf("cancel requested %d times", n)
	}
}
func TestStatusReportsWhatTheUserDid(t *testing.T) {
	m := fixture(t)
	var clipped string
	m.clipboard = func(s string) error { clipped = s; return nil }
	run := func(cmd tea.Cmd) {
		if cmd != nil {
			m.Update(unbatch(cmd()))
		}
	}
	selectContainer(m, "redis")
	run(press(m, "y"))
	if m.status != "Copied redis details" || m.statusKind != statusOK || !strings.Contains(clipped, "Container: redis") {
		t.Fatalf("copy details: %q / %q", m.status, clipped)
	}
	press(m, "w")
	if m.status != "Log wrapping off" {
		t.Fatalf("wrap: %q", m.status)
	}
	press(m, "/")
	press(m, "re")
	press(m, "enter")
	if m.status != "2 containers match “re”" { // redis and postgres
		t.Fatalf("search: %q", m.status)
	}
	press(m, "esc")
	if m.status != "Search cleared" {
		t.Fatalf("clear: %q", m.status)
	}
	selectContainer(m, "redis")
	cmd := press(m, "x")
	if m.status != "Stopping redis…" || m.statusKind != statusInfo {
		t.Fatalf("running: %q", m.status)
	}
	msg := unbatch(cmd())
	for i := 0; i < 10; i++ {
		_, next := m.Update(msg)
		if _, done := msg.(operationDone); done {
			break
		}
		msg = unbatch(next())
	}
	if m.status != "Stopped redis" || m.statusKind != statusOK {
		t.Fatalf("done: %q", m.status)
	}
	if strings.Contains(m.View(), "⟳") {
		t.Fatal("refresh spinner still shown")
	}
}
func TestWheelScrollPausesAndResumes(t *testing.T) {
	m := fixture(t)
	selectContainer(m, "redis")
	m.logs = nil
	for i := 0; i < 100; i++ {
		m.logs = append(m.logs, fmt.Sprintf("line %d", i))
	}
	m.syncLogView()
	x, y := m.logsLeft()+2, m.logsTop()+2
	wheel := func(b tea.MouseButton) {
		m.lastWheel = time.Time{} // a fresh, deliberate scroll
		m.Update(tea.MouseMsg{X: x, Y: y, Button: b, Action: tea.MouseActionPress})
	}
	wheel(tea.MouseButtonWheelDown)
	if !m.follow {
		t.Fatal("scrolling down while live paused the logs")
	}
	wheel(tea.MouseButtonWheelUp)
	if m.follow {
		t.Fatal("scrolling up did not pause")
	}
	for i := 0; i < 5 && !m.follow; i++ {
		wheel(tea.MouseButtonWheelDown)
	}
	if !m.follow || m.status != "Logs resumed" {
		t.Fatal("scrolling back to the bottom did not resume")
	}
}
func TestMomentumAfterEscDoesNotRepause(t *testing.T) {
	m := fixture(t)
	selectContainer(m, "redis")
	for i := 0; i < 100; i++ {
		m.logs = append(m.logs, fmt.Sprintf("line %d", i))
	}
	m.syncLogView()
	x, y := m.logsLeft()+2, m.logsTop()+2
	up := tea.MouseMsg{X: x, Y: y, Button: tea.MouseButtonWheelUp, Action: tea.MouseActionPress}
	m.Update(up)
	if m.follow {
		t.Fatal("scroll did not pause")
	}
	press(m, "esc")
	for i := 0; i < 20; i++ { // the trackpad keeps coasting after Esc
		m.Update(up)
	}
	if !m.follow {
		t.Fatal("momentum re-paused the logs after Esc")
	}
	m.lastWheel = time.Now().Add(-time.Second) // momentum over; the user scrolls again
	m.Update(up)
	if m.follow {
		t.Fatal("a new scroll after the momentum ended was ignored")
	}
}
func TestLogKeysScrollLikeTheWheel(t *testing.T) {
	m := fixture(t)
	selectContainer(m, "redis")
	for i := 0; i < 100; i++ {
		m.logs = append(m.logs, fmt.Sprintf("line %d", i))
	}
	m.syncLogView()
	m.focus = 1
	press(m, "j")
	if !m.follow {
		t.Fatal("j while live paused the logs")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyHome})
	if m.follow || m.logViewport.YOffset != 0 {
		t.Fatal("Home did not pause and jump to the top")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEnd})
	if !m.follow {
		t.Fatal("End did not go back to live")
	}
}
func TestFailedOperationOffersItsOutput(t *testing.T) {
	m := fixture(t)
	selectContainer(m, "redis")
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	press(m, "x")
	m.Update(operationDone{err: fmt.Errorf("daemon said no"), kind: "stop-container"})
	if m.status != "Couldn't stop redis" || !strings.Contains(ansi.Strip(m.View()), "o details") {
		t.Fatalf("failure not surfaced: %q", m.status)
	}
	press(m, "o")
	if m.modal != "progress" || !strings.Contains(strings.Join(m.progress, "\n"), "daemon said no") {
		t.Fatal("o did not show the failed output")
	}
	press(m, "esc")
	press(m, "esc") // dismiss the error
	m.errorText = "Clipboard: unavailable"
	if strings.Contains(ansi.Strip(m.View()), "o details") {
		t.Fatal("non-operation error offered operation output")
	}
}
func TestRowShowsOperationInFlight(t *testing.T) {
	m := fixture(t)
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	selectContainer(m, "redis")
	press(m, "x")
	view := ansi.Strip(m.View())
	if !strings.Contains(view, "◌ redis") || !strings.Contains(view, "stopping…") {
		t.Fatal("container row does not show it is stopping")
	}
	m.Update(operationDone{kind: "stop-container"})
	if !strings.Contains(ansi.Strip(m.View()), "stopping…") {
		t.Fatal("row flashed back to its old state before the refresh landed")
	}
	m.refreshBusy = false
	msg := m.refresh()()
	m.Update(msg)
	if strings.Contains(ansi.Strip(m.View()), "stopping…") {
		t.Fatal("in-flight label stuck after the refresh")
	}
	press(m, "?")
	press(m, "stop profile")
	press(m, "enter")
	press(m, "y")
	if !strings.Contains(ansi.Strip(m.View()), "◌ stopping…") {
		t.Fatal("header does not show the profile stopping")
	}
}
func TestImagesTab(t *testing.T) {
	m := fixture(t)
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	feed := func(cmd tea.Cmd) {
		for cmd != nil {
			msg := unbatch(cmd())
			if msg == nil {
				return
			}
			_, cmd = m.Update(msg)
			if _, ok := msg.(historyMsg); ok {
				return
			}
		}
	}
	feed(press(m, "tab"))
	if !m.onImages() || len(m.images) != 6 || m.container() != nil {
		t.Fatalf("images tab did not load: %d images", len(m.images))
	}
	rows := m.imageRows()
	if !rows[len(rows)-1].Dangling() {
		t.Fatal("untagged images should sort last")
	}
	if len(m.layers) == 0 {
		t.Fatal("layers for the selected image were not loaded")
	}
	view := ansi.Strip(m.View())
	if !strings.Contains(view, "redis:7-alpine") || !strings.Contains(view, "layers") {
		t.Fatal("image list or details not rendered")
	}
	for m.image().Ref() != "node:22-alpine" {
		feed(press(m, "j"))
	}
	if m.inUse(*m.image()) {
		t.Fatal("node should be unused")
	}
	press(m, "enter")
	ids := map[string]bool{}
	for _, a := range m.imageActions() {
		ids[a.ID] = true
	}
	if m.modal != "image" || !ids["pull-image"] || !ids["delete-image"] || !ids["copy-image"] {
		t.Fatalf("image dialog: modal=%q actions=%v", m.modal, ids)
	}
	press(m, "d")
	if m.modal != "confirm" || m.pending.Image.Ref() != "node:22-alpine" {
		t.Fatal("delete did not ask to confirm the exact image")
	}
	press(m, "esc")
	for m.image().Ref() != "redis:7-alpine" { // used by a container
		feed(press(m, "j"))
	}
	if a := m.imageActions(); len(a) != 2 {
		t.Fatalf("an image in use should not offer delete: %+v", a)
	}
	press(m, "u")
	if !m.busy || m.status != "Pulling redis:7-alpine…" || m.imageInFlight(m.image().ID) != "pulling…" {
		t.Fatalf("pull did not start: %q", m.status)
	}
	m.busy = false
	m.focus = 1
	press(m, "tab")
	if !m.onImages() {
		t.Fatal("tab on the right pane should not switch tabs")
	}
	press(m, "h")
	press(m, "tab")
	selectContainer(m, "redis")
	if m.onImages() || m.container() == nil {
		t.Fatal("i did not switch back to containers")
	}
}
