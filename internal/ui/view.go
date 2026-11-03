package ui

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// logo is the app's name mark, painted with the rainbow wherever it appears.
const logo = "☾ lazylima"

// Uplift palette: pastel rainbow stops, cycled for gradients and key chips.
var rainbowStops = []string{"#FF8FC7", "#FFB38A", "#FFE38A", "#8FF0C0", "#8FD3FF", "#B9A3FF"}

var (
	pink     = lipgloss.Color("#FF8FC7")
	lemon    = lipgloss.Color("#FFE38A")
	mint     = lipgloss.Color("#8FF0C0")
	sky      = lipgloss.Color("#8FD3FF")
	lavender = lipgloss.Color("#B9A3FF")
	peach    = lipgloss.Color("#FFB38A")
	red      = lipgloss.Color("#FF8A9A")
	ink      = lipgloss.Color("#F4EEFF")
	night    = lipgloss.Color("#1E1A2E")
	dim      = lipgloss.Color("#4A4466")

	muted    = lipgloss.NewStyle().Foreground(lipgloss.Color("#9A93B8"))
	accent   = lipgloss.NewStyle().Foreground(mint)
	heading  = lipgloss.NewStyle().Bold(true).Foreground(ink)
	warning  = lipgloss.NewStyle().Foreground(lemon)
	danger   = lipgloss.NewStyle().Foreground(red)
	selected = lipgloss.NewStyle().Foreground(lipgloss.Color("#FFF0FA")).Background(lipgloss.Color("#3B2D5C")).Bold(true)
)

func fit(s string, width int) string { return ansi.Truncate(s, max(0, width), "…") }
func pad(s string, width int) string {
	return s + strings.Repeat(" ", max(0, width-ansi.StringWidth(s)))
}
func paint(c lipgloss.Color, s string) string { return lipgloss.NewStyle().Foreground(c).Render(s) }
func pill(bg lipgloss.Color, s string) string {
	return lipgloss.NewStyle().Background(bg).Foreground(night).Bold(true).Padding(0, 1).Render(s)
}

// hue samples the cyclic rainbow at t, blending between neighbouring stops.
func hue(t float64) lipgloss.Color {
	t -= math.Floor(t)
	x := t * float64(len(rainbowStops))
	i := int(x) % len(rainbowStops)
	a, b := hexRGB(rainbowStops[i]), hexRGB(rainbowStops[(i+1)%len(rainbowStops)])
	f := x - math.Floor(x)
	var mix [3]int
	for k := range mix {
		mix[k] = int(math.Round(a[k] + (b[k]-a[k])*f))
	}
	return lipgloss.Color(fmt.Sprintf("#%02X%02X%02X", mix[0], mix[1], mix[2]))
}
func hexRGB(h string) [3]float64 {
	v, _ := strconv.ParseUint(h[1:], 16, 32)
	return [3]float64{float64(v >> 16 & 0xFF), float64(v >> 8 & 0xFF), float64(v & 0xFF)}
}
func rainbow(s string, offset, span float64) string {
	runes := []rune(s)
	var b strings.Builder
	for i, r := range runes {
		b.WriteString(lipgloss.NewStyle().Bold(true).Foreground(hue(offset + span*float64(i)/float64(max(1, len(runes))))).Render(string(r)))
	}
	return b.String()
}

// keys renders "key label · key label" hints as rainbow keys with muted labels.
func keys(hint string) string {
	parts := strings.Split(hint, " · ")
	for i, part := range parts {
		key, label, _ := strings.Cut(part, " ")
		parts[i] = paint(hue(float64(i)/7), key) + muted.Render(" "+label)
	}
	return strings.Join(parts, muted.Render("  "))
}

// box draws a rounded panel with the title and footers set into the border.
// Lit panels get a rainbow border that flows around the perimeter.
func box(title, badge, footLeft, footRight string, lines []string, width, height int, lit bool) string {
	inner := max(1, width-4)
	body := max(0, height-2)
	perimeter := float64(2*width + 2*body)
	edge := func(n int, ch string, pos func(int) int) string {
		if n <= 0 {
			return ""
		}
		if !lit {
			return paint(dim, strings.Repeat(ch, n))
		}
		var b strings.Builder
		for i := 0; i < n; i++ {
			b.WriteString(paint(hue(float64(pos(i))/perimeter), ch))
		}
		return b.String()
	}
	label := func(s string) string {
		if s == "" {
			return ""
		}
		return " " + s + " "
	}
	line := func(left, right, l, r string, pos func(int) int) string {
		left, right = label(left), label(right)
		right = fit(right, max(0, width-4))
		left = fit(left, max(0, width-4-ansi.StringWidth(right)))
		fill := width - 4 - ansi.StringWidth(left) - ansi.StringWidth(right)
		at := 2 + ansi.StringWidth(left)
		return edge(1, l, func(int) int { return pos(0) }) + edge(1, "─", func(int) int { return pos(1) }) + left +
			edge(fill, "─", func(i int) int { return pos(at + i) }) + right +
			edge(1, "─", func(int) int { return pos(width - 2) }) + edge(1, r, func(int) int { return pos(width - 1) })
	}
	out := []string{line(title, badge, "╭", "╮", func(i int) int { return i })}
	for r := 0; r < body; r++ {
		text := ""
		if r < len(lines) {
			text = lines[r]
		}
		out = append(out, edge(1, "│", func(int) int { return 2*width + 2*body - 1 - r })+" "+pad(fit(text, inner), inner)+" "+edge(1, "│", func(int) int { return width + r }))
	}
	out = append(out, line(footLeft, footRight, "╰", "╯", func(i int) int { return width + body + width - 1 - i }))
	return strings.Join(out, "\n")
}

func (m *Model) View() string {
	if m.width == 0 {
		return rainbow(logo, 0, 1) + muted.Render("  waking up…")
	}
	if m.width < 40 || m.height < 12 {
		lines := []string{rainbow(logo, 0, 1), "needs 40 × 12 cells", "resize · q quit"}
		for i := range lines {
			lines[i] = fit(lines[i], m.width)
		}
		return strings.Join(lines[:min(len(lines), max(0, m.height))], "\n")
	}
	if m.modal == "progress" {
		return m.progressView()
	}
	if m.modal != "" {
		return m.modalView()
	}
	width := m.width
	top := " " + rainbow(logo, 0, 1) + "  "
	if p := m.current(); p != nil {
		top += pill(pink, clean(p.Name)) + " "
		if busy := m.profileInFlight(p.Name); busy != "" {
			top += warning.Render("◌ " + busy)
		} else if p.Running() {
			top += accent.Render("● running")
		} else {
			top += muted.Render("○ " + strings.ToLower(clean(p.Status)))
		}
		if !m.narrow() {
			top += muted.Render(fmt.Sprintf("   %d cpu · %s ram · %s disk", p.CPUs, human(p.Memory), human(p.Disk)))
		}
	} else {
		top += muted.Render("no profile yet")
	}
	right := m.statusView()
	if m.demo {
		right += " " + pill(lemon, "demo")
	}
	right = fit(right, max(0, width-20))
	right += " "
	top = fit(top, max(1, width-ansi.StringWidth(right)-2))
	top = pad(top, width-ansi.StringWidth(right)) + right
	rule := rainbow(strings.Repeat("─", width), 0, 1)

	if m.welcoming() {
		footer := " " + keys("enter create profile · ? more · q quit")
		if m.errorText != "" {
			footer = " " + m.errorBar(width)
		}
		return strings.Join([]string{fit(top, width), rule, m.welcomeView(width, m.height-3), fit(footer, width)}, "\n")
	}
	leftWidth := m.leftWidth()
	rightWidth := width - leftWidth
	if m.narrow() {
		leftWidth, rightWidth = width, width
	}
	bodyHeight := m.height - 3
	left, right := m.containerPane(leftWidth, bodyHeight), m.logsPane(rightWidth, bodyHeight)
	hints := "hjkl move · tab images · enter actions · / search · y copy · ? more"
	if m.onImages() {
		left, right = m.imageListPane(leftWidth, bodyHeight), m.imagePane(rightWidth, bodyHeight)
		hints = "hjkl move · tab containers · enter actions · u pull · d delete · y copy · ? more"
	}
	body := lipgloss.JoinHorizontal(lipgloss.Top, left, right)
	if m.narrow() {
		if m.focus == 0 {
			body = left
			hints = "l details · tab images · ? actions · q quit"
			if m.onImages() {
				hints = "l details · tab containers · ? actions · q quit"
			}
		} else {
			body = right
			hints = "h list · / search · y copy · ? more"
		}
	}

	footer := " " + keys(hints)
	if m.busy {
		footer = " " + keys("o watch · "+hints)
	}
	if m.searchMode != "" {
		footer = " " + paint(pink, "⌕ "+m.searchMode+" ") + m.search.View() + "  " + keys("enter apply · esc cancel")
	} else if m.errorText != "" {
		footer = " " + m.errorBar(width)
	}
	return strings.Join([]string{fit(top, width), rule, body, fit(footer, width)}, "\n")
}

// welcomeView greets a Colima install with no profiles, with a rainbow wave
// rolling through the call to action.
// errorBar shows the latest error; failed operations also offer their output.
func (m *Model) errorBar(width int) string {
	hint := "esc dismiss"
	if m.opFailed {
		hint = "o details · esc dismiss"
	}
	return danger.Render("✗ "+fit(clean(m.errorText), width-ansi.StringWidth(hint)-8)) + "  " + keys(hint)
}

func (m *Model) welcomeView(width, height int) string {
	cta := "press enter to create your first profile"
	if m.busy {
		cta = "creating your profile · press o to watch"
	}
	var b strings.Builder
	for i, r := range []rune(cta) {
		style := lipgloss.NewStyle().Foreground(hue(float64(i)*0.025 - float64(m.wave)*0.02))
		if crest := math.Sin(float64(i)*0.35 - float64(m.wave)*0.3); crest > 0.6 {
			style = style.Bold(true)
		}
		b.WriteString(style.Render(string(r)))
	}
	sparkles := []string{"✿", "❀", "✾", "❁"}
	spark := func(off int) string {
		return paint(hue(float64(m.wave)*0.02+float64(off)*0.5), sparkles[(m.wave/6+off)%len(sparkles)])
	}
	content := lipgloss.JoinVertical(lipgloss.Center,
		muted.Render("(｡•ᴗ•｡)"),
		"",
		heading.Render("no Colima profile yet"),
		muted.Render("a profile is a small Linux VM that runs your containers"),
		"",
		spark(0)+"  "+b.String()+"  "+spark(1),
	)
	lines := strings.Split(content, "\n")
	for i := range lines {
		lines[i] = fit(lines[i], width)
	}
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, strings.Join(lines, "\n"))
}

// progressView shows operation output full-screen and borderless, so long
// output scrolls and drag-selecting copies clean lines.
func (m *Model) progressView() string {
	width, rows := m.width, m.progressRows()
	title := warning.Render("working…")
	hint := "↑↓ scroll · y copy all · esc cancel"
	if !m.busy {
		title, hint = accent.Render("done ✿"), "↑↓ scroll · y copy all · esc close"
		if n := len(m.progress); n > 0 && strings.HasPrefix(m.progress[n-1], "FAILED") {
			title = danger.Render("failed ✗")
		}
	}
	start := m.progressStart()
	end := min(len(m.progress), start+rows)
	where := muted.Render(fmt.Sprintf("%d–%d of %d ", start+1, end, len(m.progress)))
	if !m.progressPinned {
		where = accent.Render("● ") + where
	}
	top := " " + rainbow(logo, 0, 1) + "  " + title
	top = pad(fit(top, width-ansi.StringWidth(where)-1), width-ansi.StringWidth(where)) + where
	out := []string{top, rainbow(strings.Repeat("─", width), 0, 1)}
	for i := start; i < end; i++ {
		out = append(out, severity(i, m.progress[i]).Render(fit(m.progress[i], width)))
	}
	for len(out) < rows+2 {
		out = append(out, "")
	}
	foot := " " + keys(hint)
	foot = pad(foot, width-15) + muted.Render("drag to select")
	return strings.Join(append(out, rainbow(strings.Repeat("─", width), 0.5, 1), fit(foot, width)), "\n")
}

func severity(i int, line string) lipgloss.Style {
	l := strings.ToLower(line)
	switch {
	case i == 0:
		return heading
	case strings.Contains(l, "error") || strings.Contains(l, "fatal") || strings.HasPrefix(line, "FAILED"):
		return danger
	case strings.Contains(l, "warn"):
		return warning
	case strings.HasPrefix(line, "Completed"):
		return accent
	}
	return lipgloss.NewStyle()
}

func (m *Model) containerPane(width, height int) string {
	title := m.tabsTitle()
	var chips []string
	if m.query != "" {
		chips = append(chips, paint(pink, "⌕ "+clean(m.query)))
	}
	rows := m.rows()
	available := max(1, height-2)
	start := max(0, m.selected-available+1)
	scroll := ""
	if start > 0 {
		scroll = muted.Render(fmt.Sprintf("↑%d", start))
	}
	inner := width - 4
	var lines []string
	for i := start; i < len(rows) && i < start+available; i++ {
		r := rows[i]
		on := i == m.selected
		marker := "  "
		if on {
			marker = "❯ "
		}
		var label, state string
		var tint lipgloss.Style
		if r.Container == nil {
			arrow := "▾"
			if m.collapsed[r.Group] && m.query == "" {
				arrow = "▸"
			}
			label, state = arrow+" "+clean(r.Group), fmt.Sprintf("%d/%d", r.Running, r.Count)
			tint = lipgloss.NewStyle().Foreground(lavender).Bold(true)
		} else {
			c := r.Container
			name := c.Names
			if r.Group != "" && c.Service != "" {
				name = c.Service
			}
			icon, prefix := "○", ""
			if r.Group != "" {
				prefix = "  "
			}
			tint = muted
			state = clean(c.State)
			if c.Active() {
				icon, tint = "●", accent
			}
			if busy := m.inFlight(c.ID); busy != "" {
				icon, tint, state = "◌", warning, busy
			}
			label = prefix + icon + " " + clean(name)
		}
		label = fit(marker+label, max(1, inner-ansi.StringWidth(state)-1))
		line := label + strings.Repeat(" ", max(1, inner-ansi.StringWidth(label)-ansi.StringWidth(state))) + state
		if on {
			line = selected.Render(pad(line, inner))
		} else {
			line = tint.Render(line)
		}
		lines = append(lines, line)
	}
	if len(rows) == 0 {
		message := "(・_・;) no matches"
		if len(m.containers) == 0 {
			message = "(・ω・) nothing here yet"
		}
		if p := m.current(); p != nil {
			if !p.Running() {
				message = "(－ω－) zzz · enter to start"
			} else if !p.Docker() {
				message = "runtime " + clean(p.Runtime) + " isn't supported yet"
			}
		} else {
			message = "(｡•ᴗ•｡) n to make a profile"
		}
		lines = append(lines, "")
		for _, line := range strings.Split(ansi.Hardwrap(message, max(1, inner), true), "\n") {
			lines = append(lines, muted.Render(line))
		}
	}
	return box(title, "", strings.Join(chips, " "), scroll, lines, width, height, m.focus == 0)
}

// infoRows is the borderless details strip atop the logs pane: three rows of
// fields, then a divider padded by a blank row on each side. It is a caption
// for the logs, not a focus stop.
const infoRows = 6

func (m *Model) info(inner int) []string {
	half := inner / 2
	field := func(k, v string) string { return muted.Render(pad(k, 7)+" ") + v }
	row := func(l, r string) string { return pad(fit(l, half-2), half) + fit(r, inner-half) }
	var rows []string
	if c := m.container(); c != nil {
		usage := muted.Render("measuring…")
		for _, s := range m.stats {
			if strings.HasPrefix(c.ID, s.ID) || strings.HasPrefix(s.ID, c.ID) {
				usage = paint(sky, s.CPU) + muted.Render(" cpu  ") + paint(lavender, s.Memory)
				break
			}
		}
		if !c.Active() {
			usage = muted.Render("—")
		} else if m.statsError != "" {
			usage = muted.Render("unavailable")
		} else if !m.sampled.IsZero() && time.Since(m.sampled) > 12*time.Second {
			usage = muted.Render("stale")
		}
		rows = []string{
			row(field("status", orBusy(m.inFlight(c.ID), clean(c.Status))), field("image", clean(c.Image))),
			row(field("ports", paint(pink, orDash(clean(c.Ports)))), field("network", orDash(clean(c.Networks)))),
			row(field("usage", usage), field("id", muted.Render(fit(clean(c.ID), 12)))),
		}
	} else if r := m.selectedRow(); r != nil {
		rows = []string{row(field("services", strconv.Itoa(r.Count)), field("active", accent.Render(strconv.Itoa(r.Running)))), "", ""}
	} else {
		rows = []string{"", "", ""}
	}
	if m.detailRows() == 2 {
		return []string{rows[0], pill(sky, "logs") + paint(dim, " "+strings.Repeat("─", max(0, inner-7)))}
	}
	// A colored chip and a solid rule mark where the logs start, so the label
	// doesn't read as one more muted field.
	return append(rows, "", pill(sky, "logs")+paint(dim, " "+strings.Repeat("─", max(0, inner-7))), "")
}

// logsPane is the one right-hand pane: the selected container's details as a
// caption, then its logs. Its title names what the logs belong to.
func (m *Model) logsPane(width, height int) string {
	title := heading.Render("logs")
	if c := m.container(); c != nil {
		dot := muted.Render(" ○")
		if m.inFlight(c.ID) != "" {
			dot = warning.Render(" ◌")
		} else if c.Active() {
			dot = accent.Render(" ●")
		}
		title = heading.Render(clean(c.Names)) + dot
	} else if r := m.selectedRow(); r != nil {
		title = lipgloss.NewStyle().Foreground(lavender).Bold(true).Render(clean(r.Group))
	}
	mode := accent.Render("● live")
	if m.container() == nil {
		mode = ""
	} else if !m.follow {
		mode = warning.Bold(true).Render("❚❚ paused") + "  " + lipgloss.NewStyle().Foreground(pink).Bold(true).Render("f") + lipgloss.NewStyle().Foreground(ink).Render(" resume")
	} else if m.logEnded {
		mode = muted.Render("■ ended")
	}
	filter := ""
	if m.logQuery != "" {
		filter = paint(pink, "⌕ "+clean(m.logQuery))
	}
	lines := m.info(width - 4)
	if len(m.logs) == 0 {
		hint := "pick a container"
		if m.container() != nil {
			hint = "listening…"
		} else if m.selectedRow() != nil {
			hint = "s start stack · x stop stack · enter expand"
		}
		lines = append(lines, "", muted.Render(hint))
	} else {
		lines = append(lines, m.visibleLogs()...)
	}
	return box(title, mode, filter, "", lines, width, height, m.focus == 1)
}

func orBusy(busy, s string) string {
	if busy != "" {
		return warning.Render(busy)
	}
	return s
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func (m *Model) modalView() string {
	width := min(92, m.width-6)
	if m.narrow() {
		width = m.width - 2
	}
	if m.modal == "container" || m.modal == "image" {
		width = min(52, width) // a short list reads better in a small card
	}
	inner := width - 6
	maxRows := m.height - 12
	compact := m.height < 24
	if compact {
		maxRows = m.height - 7
	}
	title := ""
	hint := "esc close"
	var lines []string
	switch m.modal {
	case "actions":
		title = "actions"
		hint = "↑↓ select · enter run · esc close"
		lines = append(lines, paint(pink, "⌕ ")+m.menuQuery+paint(pink, "▏"), "")
		items := m.filteredActions()
		start := max(0, m.modalIndex-(maxRows-3))
		for i := start; i < len(items) && len(lines) < maxRows; i++ {
			a := items[i]
			key := paint(hue(float64(i)/9), pad(a.Key, 5))
			if !a.Enabled || i == m.modalIndex {
				key = pad(a.Key, 5)
			}
			lines = append(lines, menuLine(key+a.Label, i == m.modalIndex, a.Enabled, inner))
		}
		if len(items) == 0 {
			lines = append(lines, muted.Render("(・_・;) no matches"))
		}
	case "profiles":
		title = "profiles"
		hint = "↑↓ select · enter switch · n new · esc close"
		start := max(0, m.modalIndex-maxRows+1)
		for i := start; i < len(m.profiles) && len(lines) < maxRows; i++ {
			p := m.profiles[i]
			mark := " "
			if p.Name == m.profile {
				mark = paint(pink, "●")
			}
			state := muted.Render(pad(strings.ToLower(clean(p.Status)), 9))
			if p.Running() {
				state = accent.Render(pad("running", 9))
			}
			if i == m.modalIndex {
				state = pad(strings.ToLower(clean(p.Status)), 9)
			}
			label := fmt.Sprintf("%s %s %s%s", mark, pad(clean(p.Name), 16), state, fmt.Sprintf("%d cpu · %s", p.CPUs, human(p.Memory)))
			lines = append(lines, menuLine(label, i == m.modalIndex, true, inner))
		}
	case "container":
		c := m.container()
		title = clean(c.Names)
		hint = "↑↓ select · enter run · esc close"
		items := m.containerActions()
		start := max(0, m.modalIndex-maxRows+1)
		for i := start; i < len(items); i++ {
			a := items[i]
			key := paint(hue(float64(i)/9), pad(a.Key, 4))
			if i == m.modalIndex {
				key = pad(a.Key, 4)
			}
			lines = append(lines, menuLine(key+a.Label, i == m.modalIndex, true, inner))
		}
	case "image":
		img := m.image()
		if img == nil { // the image vanished on refresh
			title = "image gone"
			break
		}
		title = clean(img.Ref())
		hint = "↑↓ select · enter run · esc close"
		items := m.imageActions()
		start := max(0, m.modalIndex-maxRows+1)
		for i := start; i < len(items); i++ {
			a := items[i]
			key := paint(hue(float64(i)/9), pad(a.Key, 4))
			if i == m.modalIndex {
				key = pad(a.Key, 4)
			}
			lines = append(lines, menuLine(key+a.Label, i == m.modalIndex, true, inner))
		}
		if m.inUse(*img) {
			lines = append(lines, "", muted.Render("in use by a container, so it can't be deleted"))
		}
	case "form":
		title = "configure profile"
		if m.formNew {
			title = "new profile"
		}
		hint = "tab next · enter review · esc cancel"
		labels := []string{"name", "cpu cores", "ram (GiB)", "disk (GiB)"}
		for i, input := range m.form {
			label := muted.Render(pad("  "+labels[i], 14))
			if i == m.formIndex {
				label = paint(hue(float64(i)/6), pad("❯ "+labels[i], 14))
			}
			lines = append(lines, label+input.View())
			if !compact {
				lines = append(lines, "")
			}
		}
		if !m.formNew && (!compact || m.errorText == "") {
			lines = append(lines, warning.Render("⚠ applying restarts this VM · disk can only grow"))
		}
		if m.errorText != "" {
			if !compact {
				lines = append(lines, "")
			}
			lines = append(lines, danger.Render("✗ "+fit(clean(m.errorText), inner-2)))
		}
	case "confirm":
		title = strings.ToLower(m.confirmTitle)
		hint = "←→ choose · enter select · y confirm · esc cancel"
		for _, line := range strings.Split(m.confirmText, "\n") {
			lines = append(lines, strings.Split(ansi.Hardwrap(clean(line), inner, true), "\n")...)
		}
		lines = append(lines, "", menuLine("cancel", m.modalIndex == 0, true, inner), menuLine("confirm", m.modalIndex == 1, true, inner))
	}
	if len(lines) > maxRows { // Keep the confirmation buttons reachable in small terminals.
		if m.modal == "confirm" {
			tail := lines[len(lines)-3:]
			lines = append(append(lines[:maxRows-4], muted.Render("…")), tail...)
		} else {
			lines = lines[:maxRows]
		}
	}
	content := []string{""}
	if compact {
		content = nil
	}
	for _, line := range lines {
		content = append(content, " "+fit(line, inner))
	}
	if !compact {
		content = append(content, "")
	}
	content = append(content, " "+fit(keys(hint), inner))
	card := box(rainbow(title, 0.2, 0.6), "", "", "", content, width, len(content)+2, true)
	logo := rainbow(logo, 0, 1)
	if m.demo {
		logo += " " + pill(lemon, "demo")
	}
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, logo+"\n\n"+card)
}

func menuLine(text string, on, enabled bool, width int) string {
	prefix := "  "
	if on {
		prefix = "❯ "
	}
	text = fit(prefix+text, width)
	if on {
		return selected.Render(pad(ansi.Strip(text), width))
	}
	if !enabled {
		return muted.Render(ansi.Strip(text))
	}
	return text
}
