package ui

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/aymanbagabas/go-osc52/v2"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"lazylima/internal/engine"
)

type action struct {
	ID, Key, Label string
	Enabled        bool
}

func (m *Model) actions() []action {
	p, c := m.current(), m.container()
	exists := p != nil
	running := exists && p.Running()
	docker := running && p.Docker()
	selected := docker && c != nil
	active := selected && c.Active()
	stopped := selected && !c.Active()
	idle := !m.busy && !m.handoff
	img := m.image()
	projectStart, projectStop := false, false
	containerStartKey, containerStopKey := "s", "x"
	projectStartKey, projectStopKey := "", ""
	if project := m.project(); project != "" {
		containerStartKey, containerStopKey = "", ""
		projectStartKey, projectStopKey = "s", "x"
		for _, c := range m.containers {
			if c.Project == project {
				projectStart = projectStart || !c.Active()
				projectStop = projectStop || c.Active()
			}
		}
	}
	return []action{
		{"profiles", "p", "Switch profile", idle && len(m.profiles) > 0},
		{"create", "n", "Create profile", idle},
		{"configure", "", "Configure profile resources", idle && exists},
		{"start-profile", "", "Start profile", idle && exists && !running},
		{"stop-profile", "", "Stop profile…", idle && running},
		{"start-project", projectStartKey, "Start Compose stack", idle && docker && projectStart},
		{"stop-project", projectStopKey, "Stop Compose stack", idle && docker && projectStop},
		{"start-container", containerStartKey, "Start container", idle && stopped},
		{"stop-container", containerStopKey, "Stop container", idle && active},
		{"restart-container", "r", "Restart container", idle && selected},
		{"delete-container", "d", "Delete stopped container…", idle && stopped},
		{"shell", "e", "Open container shell", idle && selected && c.State == "running"},
		{"output", "o", "Show operation output", len(m.progress) > 0},
		{"images", "", "Switch between containers and images", docker},
		{"pull-image", "", "Pull image", idle && img != nil && !img.Dangling()},
		{"delete-image", "", "Delete image…", idle && img != nil && !m.inUse(*img)},
		{"copy-image", "", "Copy image name", img != nil},
		{"follow", "f", "Pause / resume logs", selected},
		{"timestamps", "t", "Toggle log timestamps", true},
		{"wrap", "w", "Toggle log wrapping", true},
		{"copy-details", "", "Copy container details", selected},
		{"copy-logs", "", "Copy filtered logs", len(m.logs) > 0},
	}
}

func (m *Model) project() string {
	if !m.onImages() {
		if r := m.selectedRow(); r != nil && r.Container == nil {
			return r.Group
		}
	}
	return ""
}

// containerActions lists what can be done to the selected container right
// now, for the dialog Enter opens.
func (m *Model) containerActions() []action {
	if m.container() == nil {
		return nil
	}
	var items []action
	for _, a := range m.actions() {
		switch a.ID {
		case "start-container", "stop-container", "restart-container", "delete-container", "shell", "copy-details":
			if a.Enabled {
				if a.ID == "copy-details" {
					a.Key = "y" // c works too; in this dialog copy means the container's details
				}
				items = append(items, a)
			}
		}
	}
	return items
}
func (m *Model) dispatch(id string) tea.Cmd {
	allowed := false
	for _, a := range m.actions() {
		if a.ID == id {
			allowed = a.Enabled
			break
		}
	}
	if !allowed {
		return nil // keys that don't apply do nothing; the ? menu shows them greyed out
	}
	p, c := m.current(), m.container()
	switch id {
	case "profiles":
		m.modal = "profiles"
		m.modalIndex = 0
		for i, p := range m.profiles {
			if p.Name == m.profile {
				m.modalIndex = i
			}
		}
	case "create":
		m.openForm(true)
	case "configure":
		m.openForm(false)
	case "start-profile":
		return m.startOperation(engine.Operation{Kind: "start", Profile: *p})
	case "stop-profile":
		m.confirm(engine.Operation{Kind: "stop-profile", Profile: *p}, "Stop profile?", "Stop the VM and interrupt every running container in "+p.Name+".")
	case "start-container", "stop-container", "restart-container":
		return m.startOperation(engine.Operation{Kind: id, Profile: *p, Container: *c})
	case "start-project", "stop-project":
		return m.startOperation(engine.Operation{Kind: id, Profile: *p, Project: m.project()})
	case "delete-container":
		m.confirm(engine.Operation{Kind: id, Profile: *p, Container: *c}, "Delete container?", "Permanently remove "+c.Names+" from "+p.Name+".\nThe container's writable layer is lost. Volumes are retained.")
	case "shell":
		m.handoff = true
		m.note(statusInfo, "Opening "+c.Names+" shell…")
		m.shellName = c.Names
		profile, container := *p, *c
		m.stopLogs()
		return func() tea.Msg {
			cmd, err := m.backend.Shell(profile, container.ID)
			return shellReady{cmd: cmd, err: err}
		}
	case "images":
		return m.toggleImages()
	case "pull-image", "delete-image", "copy-image":
		return m.dispatchImage(id, p)
	case "output":
		m.modal = "progress"
		m.progressPinned = false
	case "follow":
		if m.follow {
			m.pauseLogs()
		} else {
			m.wheelMuted = true // swallow trackpad momentum still coasting from before
			return m.resumeLogs()
		}
	case "timestamps":
		m.prefs.Timestamps = !m.prefs.Timestamps
		m.savePrefs()
		m.syncLogView()
		m.note(statusInfo, "Timestamps "+onOff(m.prefs.Timestamps))
	case "wrap":
		m.prefs.Wrap = !m.prefs.Wrap
		m.savePrefs()
		m.syncLogView()
		m.note(statusInfo, "Log wrapping "+onOff(m.prefs.Wrap))
	case "copy-details":
		return m.copy(fmt.Sprintf("Profile: %s\nContainer: %s\nID: %s\nImage: %s\nStatus: %s\nPorts: %s\nNetworks: %s", p.Name, c.Names, c.ID, c.Image, c.Status, c.Ports, c.Networks), "Copied "+c.Names+" details")
	case "copy-logs":
		text := m.logText()
		label := "Copied " + plural(strings.Count(text, "\n")+1, "log line")
		if m.logQuery != "" {
			label = "Copied " + plural(strings.Count(text, "\n")+1, "matching line")
		}
		return m.copy(text, label)
	}
	return nil
}

type shellReady struct {
	cmd *exec.Cmd
	err error
}

func (m *Model) key(msg tea.KeyMsg) tea.Cmd {
	k := msg.String()
	if k == "ctrl+c" {
		m.Close()
		return tea.Quit
	}
	if m.handoff && k != "q" {
		return nil
	}
	if m.searchMode != "" {
		return m.searchKey(msg)
	}
	if m.modal != "" {
		return m.modalKey(msg)
	}
	if m.onImages() {
		if cmd, ok := m.imageKey(k); ok {
			return cmd
		}
	}
	switch k {
	case "q":
		m.Close()
		return tea.Quit
	case "?":
		m.modal = "actions"
		m.modalIndex = 0
		m.menuQuery = ""
		return nil
	case "esc":
		m.errorText = ""
		m.opFailed = false
		m.sel = selection{}
		old := m.selectedKey()
		if m.query != "" {
			m.note(statusInfo, "Search cleared")
		}
		m.query = ""
		m.restoreSelection(old)
		if old != m.selectedKey() {
			return m.selectLogs(true)
		}
		if !m.follow && m.container() != nil {
			return m.dispatch("follow") // Esc also leaves paused mode
		}
		return nil
	case "tab", "shift+tab":
		// Panes are switched with ←→ / h l; Tab cycles the left pane's tabs.
		if m.focus == 0 {
			return m.dispatch("images")
		}
		return nil
	case "c", "y":
		if m.focus == 1 {
			return m.dispatch("copy-logs")
		}
		return m.dispatch("copy-details")
	case "left", "h":
		m.focus = 0
		return nil
	case "right", "l":
		m.focus = 1
		return nil
	case "/":
		m.searchMode = "containers"
		value := m.query
		if m.onImages() {
			m.searchMode = "images"
		} else if m.focus == 1 {
			m.searchMode = "logs"
			value = m.logQuery
			m.pauseLogs()
		}
		m.search.SetValue(value)
		m.search.CursorEnd()
		return m.search.Focus()
	case "enter":
		if m.welcoming() {
			return m.dispatch("create")
		}
		if p := m.current(); p != nil && !p.Running() && !m.busy {
			return m.dispatch("start-profile")
		}
		if r := m.selectedRow(); r != nil && r.Container == nil {
			m.collapsed[r.Group] = !m.collapsed[r.Group]
		} else if len(m.containerActions()) > 0 {
			m.modal = "container"
			m.modalIndex = 0
		}
		return nil
	case "up", "k", "down", "j", "pgup", "pgdown", "home", "end":
		if m.focus == 1 {
			return m.scrollLogs(k == "up" || k == "k" || k == "pgup" || k == "home", func() tea.Cmd {
				switch k {
				case "home": // the viewport has no Home/End bindings of its own
					m.logViewport.GotoTop()
				case "end":
					m.logViewport.GotoBottom()
				default:
					var cmd tea.Cmd
					m.logViewport, cmd = m.logViewport.Update(msg)
					return cmd
				}
				return nil
			})
		}
		old := m.selectedKey()
		switch k {
		case "up", "k":
			m.selected--
		case "down", "j":
			m.selected++
		case "pgup":
			m.selected -= 10
		case "pgdown":
			m.selected += 10
		case "home":
			m.selected = 0
		case "end":
			m.selected = len(m.rows()) - 1
		}
		m.selected = min(max(0, m.selected), max(0, len(m.rows())-1))
		if old != m.selectedKey() {
			return m.selectLogs(true)
		}
		return nil
	case "[", "]":
		if len(m.profiles) > 1 {
			index := 0
			for i, p := range m.profiles {
				if p.Name == m.profile {
					index = i
				}
			}
			if k == "[" {
				index = (index + len(m.profiles) - 1) % len(m.profiles)
			} else {
				index = (index + 1) % len(m.profiles)
			}
			return m.switchProfile(m.profiles[index].Name)
		}
		return nil
	}
	for _, a := range m.actions() {
		if a.Enabled && a.Key != "" && a.Key == k {
			return m.dispatch(a.ID)
		}
	}
	return nil
}
func (m *Model) searchKey(msg tea.KeyMsg) tea.Cmd {
	old := m.selectedKey()
	switch msg.String() {
	case "enter":
		m.noteSearch()
		m.searchMode = ""
		m.search.Blur()
		return nil
	case "esc": // always clears the search, whatever was there before
		if m.searchMode == "logs" {
			if m.logQuery != "" {
				m.note(statusInfo, "Log filter cleared")
			}
			m.logQuery = ""
			m.syncLogView()
		} else {
			if m.query != "" {
				m.note(statusInfo, "Search cleared")
			}
			m.query = ""
		}
		m.search.SetValue("")
		m.searchMode = ""
		m.search.Blur()
		m.restoreSelection(old)
		if old != m.selectedKey() {
			return m.selectLogs(true)
		}
		return nil
	}
	var cmd tea.Cmd
	m.search, cmd = m.search.Update(msg)
	if m.searchMode == "logs" {
		m.logQuery = m.search.Value()
		m.logViewport.GotoTop()
		m.syncLogView()
	} else {
		m.query = m.search.Value()
		if m.onImages() {
			m.imageSel = 0
			return tea.Batch(cmd, m.fetchHistory())
		}
		m.restoreSelection(old)
		if old != m.selectedKey() {
			return tea.Batch(cmd, m.selectLogs(true))
		}
	}
	return cmd
}
func (m *Model) filteredActions() []action {
	var result []action
	for _, a := range m.actions() {
		if strings.Contains(strings.ToLower(a.Label), strings.ToLower(m.menuQuery)) {
			result = append(result, a)
		}
	}
	return result
}
func (m *Model) modalKey(msg tea.KeyMsg) tea.Cmd {
	k := msg.String()
	switch m.modal {
	case "form":
		return m.formKey(msg)
	case "confirm":
		switch k {
		case "esc", "q", "n":
			m.modal = ""
			m.note(statusInfo, "Canceled")
		case "left", "right", "h", "l", "tab", "up", "down", "j", "k":
			m.modalIndex = 1 - m.modalIndex
		case "y":
			return m.startOperation(m.pending)
		case "enter":
			if m.modalIndex == 1 {
				return m.startOperation(m.pending)
			}
			m.modal = ""
			m.note(statusInfo, "Canceled")
		}
		return nil
	case "progress":
		rows := m.progressRows()
		top := m.progressStart()
		switch k {
		case "up", "k":
			top--
		case "down", "j":
			top++
		case "pgup", "b":
			top -= rows
		case "pgdown", " ":
			top += rows
		case "home", "g":
			top = 0
		case "end", "G":
			top = len(m.progress)
		case "c", "y":
			return m.copy(strings.Join(m.progress, "\n"), "Copied output · "+plural(len(m.progress), "line"))
		case "esc":
			if m.busy { // Esc cancels a running command, then closes once it's done
				if m.opCancel != nil {
					m.opCancel()
					m.opCancel = nil
					m.canceled = true
					m.progress = append(m.progress, "Canceling command… Check current state after cancellation.")
				}
				return nil
			}
			m.modal = ""
			return nil
		case "q", "enter":
			if !m.busy {
				m.modal = ""
			}
			return nil
		default:
			return nil
		}
		last := max(0, len(m.progress)-rows)
		m.progressTop = min(max(0, top), last)
		m.progressPinned = m.progressTop < last
		return nil
	case "profiles":
		if k == "esc" || k == "q" {
			m.modal = ""
			return nil
		}
		if k == "n" {
			m.openForm(true)
			return nil
		}
		if k == "enter" && len(m.profiles) > 0 {
			m.modalIndex = min(m.modalIndex, len(m.profiles)-1)
			name := m.profiles[m.modalIndex].Name
			m.modal = ""
			return m.switchProfile(name)
		}
		m.modalIndex = navigate(k, m.modalIndex, len(m.profiles))
		return nil
	case "container", "image":
		items := m.containerActions()
		if m.modal == "image" {
			items = m.imageActions()
		}
		if k == "esc" || k == "q" || len(items) == 0 {
			m.modal = ""
			return nil
		}
		m.modalIndex = min(m.modalIndex, len(items)-1)
		id := ""
		if k == "enter" {
			id = items[m.modalIndex].ID
		}
		for _, a := range items {
			if a.Key == k || k == "c" && (a.ID == "copy-details" || a.ID == "copy-image") {
				id = a.ID
			}
		}
		if id != "" {
			m.modal = ""
			return m.dispatch(id)
		}
		m.modalIndex = navigate(k, m.modalIndex, len(items))
		return nil
	case "actions":
		if k == "esc" || k == "?" {
			m.modal = ""
			return nil
		}
		items := m.filteredActions()
		m.modalIndex = min(m.modalIndex, max(0, len(items)-1))
		switch k {
		case "up", "down", "ctrl+p", "ctrl+n":
			m.modalIndex = navigate(k, m.modalIndex, len(items))
		case "enter":
			if len(items) > 0 && items[m.modalIndex].Enabled {
				id := items[m.modalIndex].ID
				m.modal = ""
				return m.dispatch(id)
			}
		case "backspace":
			r := []rune(m.menuQuery)
			if len(r) > 0 {
				m.menuQuery = string(r[:len(r)-1])
			}
			m.modalIndex = 0
		default:
			if msg.Type == tea.KeyRunes && len(m.menuQuery) < 80 {
				m.menuQuery += string(msg.Runes)
				m.modalIndex = 0
			}
			if msg.Type == tea.KeySpace {
				m.menuQuery += " "
			}
		}
	}
	return nil
}
func navigate(k string, index, count int) int {
	if count == 0 {
		return 0
	}
	switch k {
	case "up", "k", "ctrl+p":
		index--
	case "down", "j", "ctrl+n", "tab":
		index++
	case "home":
		index = 0
	case "end":
		index = count - 1
	}
	return (index + count) % count
}
func (m *Model) confirm(op engine.Operation, title, text string) {
	m.pending = op
	m.confirmTitle = title
	m.confirmText = text
	m.modal = "confirm"
	m.modalIndex = 0
}
func (m *Model) startOperation(op engine.Operation) tea.Cmd {
	if m.busy {
		m.note(statusWarn, "Another operation is still running")
		return nil
	}
	m.modal = "" // runs in the background; o opens its output
	m.opFailed = false
	m.running, m.canceled = op, false
	doing, _, _ := opText(op)
	m.note(statusInfo, doing)
	m.progress = []string{op.Kind + " · " + op.Profile.Name}
	m.progressTop, m.progressPinned = 0, false
	m.busy = true
	m.stopLogs()
	m.errorText = ""
	ctx, cancel := context.WithCancel(m.ctx)
	m.opCancel = cancel
	events := make(chan tea.Msg, 64)
	m.opEvents = events
	return func() tea.Msg {
		go func() {
			emit := func(line string) {
				select {
				case events <- progressMsg(line):
				case <-ctx.Done():
				}
			}
			err := m.backend.Run(ctx, op, emit)
			select {
			case events <- operationDone{err: err, profile: op.Resources.Name, kind: op.Kind}:
			case <-m.ctx.Done():
			}
		}()
		return <-events
	}
}
func waitOperation(events chan tea.Msg) tea.Cmd { return func() tea.Msg { return <-events } }
func (m *Model) openForm(create bool) {
	m.modal = "form"
	m.formNew = create
	m.formIndex = 0
	m.errorText = ""
	r := engine.Resources{CPUs: 2, Memory: 2, Disk: 100}
	if p := m.current(); p != nil && !create {
		m.formProfile = *p
		r.Name = p.Name
		r.CPUs = p.CPUs
		r.Memory = float64(p.Memory) / (1 << 30)
		r.Disk = int(p.Disk / (1 << 30))
	}
	vals := []string{r.Name, strconv.Itoa(r.CPUs), strconv.FormatFloat(r.Memory, 'f', -1, 64), strconv.Itoa(r.Disk)}
	m.form = nil
	for i, v := range vals {
		input := textinput.New()
		input.Prompt = ""
		input.CharLimit = 63
		input.Width = 30
		input.SetValue(v)
		if i == 0 {
			input.Placeholder = "e.g. work"
		}
		m.form = append(m.form, input)
	}
	m.resize()
	if !create {
		m.formIndex = 1
	}
	m.form[m.formIndex].Focus()
}
func (m *Model) formKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		m.modal = ""
		return nil
	case "tab", "down", "shift+tab", "up":
		m.form[m.formIndex].Blur()
		delta := 1
		if msg.String() == "shift+tab" || msg.String() == "up" {
			delta = -1
		}
		m.formIndex = (m.formIndex + delta + 4) % 4
		if !m.formNew && m.formIndex == 0 {
			if delta < 0 {
				m.formIndex = 3
			} else {
				m.formIndex = 1
			}
		}
		return m.form[m.formIndex].Focus()
	case "enter":
		r := engine.Resources{Name: strings.TrimSpace(m.form[0].Value())}
		var err error
		r.CPUs, err = strconv.Atoi(m.form[1].Value())
		if err == nil {
			r.Memory, err = strconv.ParseFloat(m.form[2].Value(), 64)
		}
		if err == nil {
			r.Disk, err = strconv.Atoi(m.form[3].Value())
		}
		if err != nil {
			m.errorText = "Enter numeric CPU, RAM and disk values"
			return nil
		}
		var old *engine.Profile
		if !m.formNew {
			old = &m.formProfile
		}
		if err = r.Validate(old); err != nil {
			m.errorText = err.Error()
			return nil
		}
		for _, p := range m.profiles {
			if m.formNew && p.Name == r.Name {
				m.errorText = "A profile with that name already exists"
				return nil
			}
		}
		op := engine.Operation{Kind: "configure", Resources: r, Profile: m.formProfile}
		title := "Apply resources?"
		detail := "Start " + r.Name + " with the new resources."
		if m.formNew {
			op.Kind = "create"
			op.Profile = engine.Profile{Name: r.Name}
			title = "Create profile?"
			detail = "Create and start a Docker profile named " + r.Name + "."
		} else if m.formProfile.Running() {
			detail = "Restart " + r.Name + " to apply resources.\nAll containers in this profile will be interrupted."
		}
		m.confirm(op, title, detail+fmt.Sprintf("\n\n%d CPUs · %.1f GiB RAM · %d GiB disk", r.CPUs, r.Memory, r.Disk))
		return nil
	}
	var cmd tea.Cmd
	m.form[m.formIndex], cmd = m.form[m.formIndex].Update(msg)
	return cmd
}

func (m *Model) noteSearch() {
	switch {
	case m.searchMode == "logs" && m.logQuery == "":
		m.note(statusInfo, "Log filter cleared")
	case m.searchMode == "logs":
		n := 0
		if text := m.logText(); text != "" {
			n = strings.Count(text, "\n") + 1
		}
		m.note(statusInfo, plural(n, "log line")+matches(n)+"“"+m.logQuery+"”")
	case m.query == "":
		m.note(statusInfo, "Search cleared")
	case m.searchMode == "images":
		m.note(statusInfo, plural(len(m.imageRows()), "image")+matches(len(m.imageRows()))+"“"+m.query+"”")
	default:
		n := 0
		for _, r := range m.rows() {
			if r.Container != nil {
				n++
			}
		}
		m.note(statusInfo, plural(n, "container")+matches(n)+"“"+m.query+"”")
	}
}
func (m *Model) pbcopy(text string) error {
	ctx, cancel := context.WithTimeout(m.ctx, 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "pbcopy")
	cmd.Stdin = strings.NewReader(text)
	return cmd.Run()
}
func matches(n int) string {
	if n == 1 {
		return " matches "
	}
	return " match "
}
func onOff(on bool) string {
	if on {
		return "on"
	}
	return "off"
}
func (m *Model) copy(text, label string) tea.Cmd {
	return func() tea.Msg {
		if err := m.clipboard(text); err == nil {
			return clipDone{label: label}
		}
		// OSC52 supports remote terminals when a local clipboard tool is unavailable.
		return tea.Sequence(tea.Printf("%s", osc52.New(text).String()), func() tea.Msg { return clipDone{label: label} })()
	}
}
