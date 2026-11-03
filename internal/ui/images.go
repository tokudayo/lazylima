package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"lazylima/internal/engine"
)

// The left pane has two tabs: containers (the default) and images. The images
// tab lists what's pulled into the profile's VM; the right pane then shows the
// selected image's details and layers instead of logs.

type imagesMsg struct {
	profile string
	images  []engine.Image
	err     error
}

type historyMsg struct {
	id     string
	layers []engine.Layer
	err    error
}

func (m *Model) onImages() bool { return m.tab == "images" }

// imageRows is the image list after the search filter.
func (m *Model) imageRows() []engine.Image {
	query := strings.ToLower(m.query)
	var rows []engine.Image
	for _, img := range m.images {
		if query == "" || strings.Contains(strings.ToLower(img.Ref()), query) {
			rows = append(rows, img)
		}
	}
	return rows
}

func (m *Model) image() *engine.Image {
	if !m.onImages() {
		return nil
	}
	rows := m.imageRows()
	if m.imageSel < 0 || m.imageSel >= len(rows) {
		return nil
	}
	return &rows[m.imageSel]
}

// usedBy names the containers created from img.
func (m *Model) usedBy(img engine.Image) []string {
	var names []string
	for _, c := range m.containers {
		ref := img.Ref()
		if c.Image == ref || c.Image+":latest" == ref || strings.HasPrefix(img.ID, strings.TrimPrefix(c.Image, "sha256:")) {
			names = append(names, c.Names)
		}
	}
	return names
}

// inUse is true when any container, running or not, was created from img;
// Docker refuses to delete those without force.
func (m *Model) inUse(img engine.Image) bool {
	if n := img.Containers; n != "" && n != "N/A" {
		return n != "0"
	}
	return len(m.usedBy(img)) > 0
}

func (m *Model) toggleImages() tea.Cmd {
	m.query, m.focus, m.layerTop = "", 0, 0
	if m.onImages() {
		m.tab = ""
		return m.selectLogs(true)
	}
	m.tab = "images"
	m.imageSel = 0
	m.stopLogs()
	return m.fetchImages()
}

func (m *Model) fetchImages() tea.Cmd {
	p := m.current()
	if m.imagesBusy || p == nil || !p.Running() || !p.Docker() {
		return nil
	}
	m.imagesBusy = true
	profile := *p
	return func() tea.Msg {
		images, err := m.backend.Images(m.ctx, profile)
		return imagesMsg{profile: profile.Name, images: images, err: err}
	}
}

// fetchHistory loads the selected image's layers when the selection moves.
func (m *Model) fetchHistory() tea.Cmd {
	img, p := m.image(), m.current()
	if img == nil || p == nil || img.ID == m.layersFor {
		return nil
	}
	m.layersFor, m.layers, m.layerTop = img.ID, nil, 0
	profile, id := *p, img.ID
	return func() tea.Msg {
		layers, err := m.backend.History(m.ctx, profile, id)
		return historyMsg{id: id, layers: layers, err: err}
	}
}

func (m *Model) gotImages(msg imagesMsg) tea.Cmd {
	m.imagesBusy = false
	if msg.profile != m.profile {
		return nil
	}
	m.settling = false
	if msg.err != nil {
		m.errorText = clean(msg.err.Error())
		return nil
	}
	selected := ""
	if img := m.image(); img != nil {
		selected = img.ID
	}
	m.images = msg.images
	m.imageSel = 0
	for i, img := range m.imageRows() {
		if img.ID == selected {
			m.imageSel = i
		}
	}
	return m.fetchHistory()
}

// imageKey handles keys that mean something different on the images tab.
func (m *Model) imageKey(k string) (tea.Cmd, bool) {
	switch k {
	case "enter":
		if m.image() == nil {
			return nil, false // e.g. a stopped profile: Enter starts it
		}
		m.modal = "image"
		m.modalIndex = 0
		return nil, true
	case "u":
		return m.dispatch("pull-image"), true
	case "d":
		return m.dispatch("delete-image"), true
	case "c", "y":
		return m.dispatch("copy-image"), true
	case "up", "k", "down", "j", "pgup", "pgdown", "home", "end":
		step := map[string]int{"up": -1, "k": -1, "down": 1, "j": 1, "pgup": -10, "pgdown": 10, "home": -1 << 20, "end": 1 << 20}[k]
		if m.focus == 1 { // scroll the layer list
			m.layerTop = min(max(0, m.layerTop+step), max(0, len(m.layers)-1))
			return nil, true
		}
		m.imageSel = min(max(0, m.imageSel+step), max(0, len(m.imageRows())-1))
		return m.fetchHistory(), true
	}
	return nil, false
}

// imageActions lists what can be done to the selected image, for its dialog.
func (m *Model) imageActions() []action {
	var items []action
	for _, a := range m.actions() {
		key := map[string]string{"pull-image": "u", "delete-image": "d", "copy-image": "y"}[a.ID]
		if key != "" && a.Enabled {
			a.Key = key
			items = append(items, a)
		}
	}
	return items
}

func (m *Model) dispatchImage(id string, p *engine.Profile) tea.Cmd {
	img := *m.image()
	switch id {
	case "pull-image":
		return m.startOperation(engine.Operation{Kind: id, Profile: *p, Image: img})
	case "delete-image":
		m.confirm(engine.Operation{Kind: id, Profile: *p, Image: img}, "Delete image?",
			fmt.Sprintf("Remove %s (%s) from %s.\nYou can pull it again later.", img.Ref(), img.Size, p.Name))
	case "copy-image":
		return m.copy(img.Ref(), "Copied "+img.Ref())
	}
	return nil
}

// imageInFlight is what the current operation is doing to this image.
func (m *Model) imageInFlight(id string) string {
	if !m.busy && !m.settling || m.running.Image.ID != id {
		return ""
	}
	return map[string]string{"pull-image": "pulling…", "delete-image": "deleting…"}[m.running.Kind]
}

// tabsTitle names both left-pane tabs, the active one bright with its count.
func (m *Model) tabsTitle() string {
	if m.onImages() {
		return muted.Render("containers  ") + heading.Render("images") + muted.Render(fmt.Sprintf(" %d", len(m.images)))
	}
	return heading.Render("containers") + muted.Render(fmt.Sprintf(" %d", len(m.containers))) + muted.Render("  images")
}

func (m *Model) imageListPane(width, height int) string {
	available := max(1, height-2)
	lines, start := m.imagePaneRows(width-4, available)
	chips, scroll := "", ""
	if m.query != "" {
		chips = paint(pink, "⌕ "+clean(m.query))
	}
	if start > 0 {
		scroll = muted.Render(fmt.Sprintf("↑%d", start))
	}
	return box(m.tabsTitle(), "", chips, scroll, lines, width, height, m.focus == 0)
}

func (m *Model) imagePaneRows(inner, available int) (lines []string, start int) {
	rows := m.imageRows()
	start = max(0, m.imageSel-available+1)
	for i := start; i < len(rows) && i < start+available; i++ {
		img := rows[i]
		icon, tint := "○", muted
		if m.inUse(img) {
			icon, tint = "●", accent
		}
		name, right := img.Ref(), img.Size
		if img.Dangling() {
			name = "untagged " + fit(img.ID, 12)
		}
		if busy := m.imageInFlight(img.ID); busy != "" {
			icon, tint, right = "◌", warning, busy
		}
		marker := "  "
		if i == m.imageSel {
			marker = "❯ "
		}
		label := fit(marker+icon+" "+clean(name), max(1, inner-len(right)-1))
		line := label + strings.Repeat(" ", max(1, inner-len([]rune(label))-len([]rune(right)))) + right
		if i == m.imageSel {
			line = selected.Render(pad(line, inner))
		} else {
			line = tint.Render(line)
		}
		lines = append(lines, line)
	}
	if len(rows) == 0 {
		message := "(・ω・) no images yet"
		if len(m.images) > 0 {
			message = "(・_・;) no matches"
		} else if m.imagesBusy {
			message = "loading…"
		}
		lines = append(lines, "", muted.Render(message))
	}
	return lines, start
}

func (m *Model) imageInfo(inner int) []string {
	half := inner / 2
	field := func(k, v string) string { return muted.Render(pad(k, 7)+" ") + v }
	row := func(l, r string) string { return pad(fit(l, half-2), half) + fit(r, inner-half) }
	rows := []string{"", "", ""}
	if img := m.image(); img != nil {
		users := strings.Join(m.usedBy(*img), ", ")
		if users == "" && m.inUse(*img) {
			users = img.Containers + " container(s)"
		}
		rows = []string{
			row(field("id", muted.Render(fit(img.ID, 12))), field("size", paint(lavender, img.Size))),
			row(field("created", img.Created), field("used by", orDash(clean(users)))),
			row(field("layers", fmt.Sprint(len(m.layers))), ""),
		}
	}
	if m.detailRows() == 2 {
		return []string{rows[0], pill(sky, "layers") + paint(dim, " "+strings.Repeat("─", max(0, inner-9)))}
	}
	return append(rows, "", pill(sky, "layers")+paint(dim, " "+strings.Repeat("─", max(0, inner-9))), "")
}

func (m *Model) imagePane(width, height int) string {
	title := heading.Render("images")
	if img := m.image(); img != nil {
		dot := muted.Render(" ○")
		if m.imageInFlight(img.ID) != "" {
			dot = warning.Render(" ◌")
		} else if m.inUse(*img) {
			dot = accent.Render(" ●")
		}
		title = heading.Render(clean(img.Ref())) + dot
	}
	inner := width - 4
	lines := m.imageInfo(inner)
	if m.image() == nil {
		lines = append(lines, "", muted.Render("pick an image"))
	} else if len(m.layers) == 0 {
		lines = append(lines, "", muted.Render("reading layers…"))
	}
	for _, l := range m.layers[min(m.layerTop, len(m.layers)):] {
		lines = append(lines, paint(lavender, pad(clean(l.Size), 9))+muted.Render(clean(strings.Join(strings.Fields(l.CreatedBy), " "))))
	}
	return box(title, "", "", "", lines, width, height, m.focus == 1)
}
