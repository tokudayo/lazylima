package ui

import (
	"fmt"
	"time"

	"github.com/charmbracelet/lipgloss"
	"lazylima/internal/engine"
)

// statusKind picks the color of the status pill in the top-right corner.
type statusKind int

const (
	statusPlain statusKind = iota // background state, not something the user did
	statusInfo
	statusOK
	statusWarn
	statusErr
)

// statusFresh is how long a new status keeps its filled pill before it settles
// into plain colored text.
const statusFresh = 8 * time.Second

// note records the outcome of what the user just did for the top-right corner.
func (m *Model) note(kind statusKind, text string) {
	m.status, m.statusKind, m.statusAt = text, kind, time.Now()
}

func (m *Model) statusView() string {
	if m.status == "" {
		return ""
	}
	text := fit(clean(m.status), 44)
	color := map[statusKind]lipgloss.Color{statusInfo: sky, statusOK: mint, statusWarn: peach, statusErr: red}[m.statusKind]
	if m.statusKind == statusPlain {
		return muted.Render(text)
	}
	if time.Since(m.statusAt) > statusFresh {
		return paint(color, text)
	}
	return pill(color, text)
}

// opText names an operation for the status line: what it is doing, what it
// did, and the bare verb for failures ("Couldn't stop redis").
func opText(op engine.Operation) (doing, done, verb string) {
	target := op.Profile.Name
	switch op.Kind {
	case "create", "configure":
		target = op.Resources.Name
	case "start-container", "stop-container", "restart-container", "delete-container":
		target = op.Container.Names
	case "pull-image", "delete-image":
		target = op.Image.Ref()
	case "start-project", "stop-project":
		target = op.Project
	}
	words := map[string][3]string{
		"start-project":     {"Starting stack", "Started stack", "start stack"},
		"stop-project":      {"Stopping stack", "Stopped stack", "stop stack"},
		"start-container":   {"Starting", "Started", "start"},
		"stop-container":    {"Stopping", "Stopped", "stop"},
		"restart-container": {"Restarting", "Restarted", "restart"},
		"delete-container":  {"Deleting", "Deleted", "delete"},
		"start":             {"Starting profile", "Started profile", "start profile"},
		"stop-profile":      {"Stopping profile", "Stopped profile", "stop profile"},
		"restart-profile":   {"Restarting profile", "Restarted profile", "restart profile"},
		"configure":         {"Resizing", "Resized", "resize"},
		"create":            {"Creating profile", "Created profile", "create profile"},
		"pull-image":        {"Pulling", "Pulled", "pull"},
		"delete-image":      {"Deleting image", "Deleted image", "delete image"},
	}[op.Kind]
	if words[0] == "" {
		words = [3]string{"Running " + op.Kind, "Finished " + op.Kind, op.Kind}
	}
	return words[0] + " " + target + "…", words[1] + " " + target, words[2] + " " + target
}

// inFlight is what the current operation is doing to the container with this
// ID ("stopping…"), or "". It holds until the refresh after the operation
// lands, so the row never flashes back to its old state.
func (m *Model) inFlight(containerID string) string {
	if (m.busy || m.settling) && m.running.Profile.Name == m.profile && m.running.Project != "" {
		for _, c := range m.containers {
			if c.ID == containerID && c.Project == m.running.Project {
				return map[string]string{"start-project": "starting…", "stop-project": "stopping…"}[m.running.Kind]
			}
		}
	}
	if !m.busy && !m.settling || m.running.Container.ID != containerID {
		return ""
	}
	return map[string]string{
		"start-container":   "starting…",
		"stop-container":    "stopping…",
		"restart-container": "restarting…",
		"delete-container":  "deleting…",
	}[m.running.Kind]
}

// profileInFlight is the same for VM operations on the named profile.
func (m *Model) profileInFlight(name string) string {
	if !m.busy && !m.settling || m.running.Profile.Name != name {
		return ""
	}
	return map[string]string{
		"start":           "starting…",
		"stop-profile":    "stopping…",
		"restart-profile": "restarting…",
		"configure":       "resizing…",
	}[m.running.Kind]
}

func plural(n int, word string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, word)
	}
	return fmt.Sprintf("%d %ss", n, word)
}
