package engine

import (
	"context"
	"fmt"
	"io"
	"math"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

type Profile struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	CPUs    int    `json:"cpus"`
	Memory  int64  `json:"memory"`
	Disk    int64  `json:"disk"`
	Arch    string `json:"arch"`
	Runtime string `json:"runtime"`
}

func (p Profile) Running() bool { return strings.EqualFold(p.Status, "running") }
func (p Profile) Docker() bool  { return p.Runtime == "docker" }

type Container struct {
	ID       string `json:"ID"`
	Names    string `json:"Names"`
	Image    string `json:"Image"`
	State    string `json:"State"`
	Status   string `json:"Status"`
	Ports    string `json:"Ports"`
	Networks string `json:"Networks"`
	Labels   string `json:"Labels"`
	Project  string
	Service  string
}

func (c Container) Active() bool {
	return c.State == "running" || c.State == "restarting" || c.State == "paused"
}

// Image is one row of `docker image ls`.
type Image struct {
	ID         string `json:"ID"`
	Repository string `json:"Repository"`
	Tag        string `json:"Tag"`
	Size       string `json:"Size"`
	Created    string `json:"CreatedSince"`
	Containers string `json:"Containers"` // how many containers use it; "N/A" on old Docker
}

// Dangling images have no name; they can only be addressed by ID.
func (i Image) Dangling() bool { return i.Repository == "" || i.Repository == "<none>" }

// Ref is how the image is shown and pulled: repo:tag, or its ID when dangling.
func (i Image) Ref() string {
	if i.Dangling() {
		return i.ID
	}
	if i.Tag == "" || i.Tag == "<none>" {
		return i.Repository
	}
	return i.Repository + ":" + i.Tag
}

// Layer is one row of `docker image history`.
type Layer struct {
	CreatedBy string `json:"CreatedBy"`
	Size      string `json:"Size"`
	Created   string `json:"CreatedSince"`
}

type Stats struct {
	ID      string `json:"ID"`
	CPU     string `json:"CPUPerc"`
	Memory  string `json:"MemUsage"`
	Network string `json:"NetIO"`
}

type Resources struct {
	Name   string
	CPUs   int
	Memory float64
	Disk   int
}

var profileName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,62}$`)

func (r Resources) Validate(old *Profile) error {
	if !profileName.MatchString(r.Name) {
		return fmt.Errorf("profile name: use 1–63 letters, numbers, hyphens or underscores; start with a letter or number")
	}
	if r.CPUs < 1 || r.CPUs > 256 {
		return fmt.Errorf("CPU count must be between 1 and 256")
	}
	if math.IsNaN(r.Memory) || math.IsInf(r.Memory, 0) || r.Memory < 0.5 || r.Memory > 4096 {
		return fmt.Errorf("RAM must be between 0.5 and 4096 GiB")
	}
	if r.Disk < 1 || r.Disk > 65536 {
		return fmt.Errorf("disk must be between 1 and 65536 GiB")
	}
	if old != nil && int64(r.Disk)*(1<<30) < old.Disk {
		return fmt.Errorf("Colima disks can grow, but cannot shrink")
	}
	return nil
}
func (r Resources) Args() []string {
	return []string{"start", "--profile", r.Name, "--cpus", strconv.Itoa(r.CPUs), "--memory", strconv.FormatFloat(r.Memory, 'f', -1, 64), "--disk", strconv.Itoa(r.Disk), "--activate=false"}
}

type Operation struct {
	Kind      string
	Project   string
	Profile   Profile
	Container Container
	Resources Resources
	New       bool
	Image     Image
}

// All mutations are explicit operations; queries and streams have separate APIs.
type Backend interface {
	Profiles(context.Context) ([]Profile, error)
	Containers(context.Context, Profile) ([]Container, error)
	Stats(context.Context, Profile) ([]Stats, error)
	Images(context.Context, Profile) ([]Image, error)
	History(ctx context.Context, p Profile, imageID string) ([]Layer, error)
	Run(context.Context, Operation, func(string)) error
	Logs(context.Context, Profile, string, string) (io.ReadCloser, error)
	Shell(Profile, string) (*exec.Cmd, error)
}
