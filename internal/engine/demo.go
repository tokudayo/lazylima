package engine

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"
)

type Demo struct {
	mu         sync.Mutex
	profiles   []Profile
	containers map[string][]Container
	images     map[string][]Image
}

func NewDemo() *Demo {
	return &Demo{profiles: []Profile{
		{Name: "default", Status: "Running", CPUs: 4, Memory: 8 << 30, Disk: 100 << 30, Arch: "aarch64", Runtime: "docker"},
		{Name: "sandbox", Status: "Stopped", CPUs: 2, Memory: 2 << 30, Disk: 60 << 30, Arch: "aarch64", Runtime: "docker"},
	}, containers: map[string][]Container{"default": {
		{ID: "a1b2c3d4e5f6", Names: "shop-api-1", Image: "shop/api:dev", State: "running", Status: "Up 42 minutes (healthy)", Ports: "0.0.0.0:8080->8080/tcp", Networks: "shop_default", Project: "shop", Service: "api"},
		{ID: "b2c3d4e5f6a1", Names: "shop-postgres-1", Image: "postgres:17-alpine", State: "running", Status: "Up 42 minutes (healthy)", Ports: "127.0.0.1:5432->5432/tcp", Networks: "shop_default", Project: "shop", Service: "postgres"},
		{ID: "c3d4e5f6a1b2", Names: "redis", Image: "redis:7-alpine", State: "running", Status: "Up 2 hours", Ports: "127.0.0.1:6379->6379/tcp", Networks: "dev"},
		{ID: "d4e5f6a1b2c3", Names: "mailpit", Image: "axllent/mailpit:latest", State: "exited", Status: "Exited (0) 3 hours ago", Ports: "", Networks: "dev"},
	}}, images: map[string][]Image{"default": {
		{ID: "9f8e7d6c5b4a", Repository: "axllent/mailpit", Tag: "latest", Size: "31.2MB", Created: "3 weeks ago", Containers: "1"},
		{ID: "0a1b2c3d4e5f", Repository: "node", Tag: "22-alpine", Size: "156MB", Created: "2 months ago", Containers: "0"},
		{ID: "1b2c3d4e5f6a", Repository: "postgres", Tag: "17-alpine", Size: "278MB", Created: "6 days ago", Containers: "1"},
		{ID: "2c3d4e5f6a1b", Repository: "redis", Tag: "7-alpine", Size: "41.4MB", Created: "12 days ago", Containers: "1"},
		{ID: "3d4e5f6a1b2c", Repository: "shop/api", Tag: "dev", Size: "212MB", Created: "42 minutes ago", Containers: "1"},
		{ID: "4e5f6a1b2c3d", Repository: "<none>", Tag: "<none>", Size: "209MB", Created: "2 days ago", Containers: "0"},
	}}}
}
func (d *Demo) Profiles(context.Context) ([]Profile, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]Profile(nil), d.profiles...), nil
}
func (d *Demo) Containers(_ context.Context, p Profile) ([]Container, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !p.Running() {
		return nil, nil
	}
	return append([]Container(nil), d.containers[p.Name]...), nil
}
func (d *Demo) Images(_ context.Context, p Profile) ([]Image, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !p.Running() {
		return nil, nil
	}
	return append([]Image(nil), d.images[p.Name]...), nil
}
func (d *Demo) History(context.Context, Profile, string) ([]Layer, error) {
	return []Layer{
		{CreatedBy: `CMD ["docker-entrypoint.sh"]`, Size: "0B", Created: "12 days ago"},
		{CreatedBy: "EXPOSE map[6379/tcp:{}]", Size: "0B", Created: "12 days ago"},
		{CreatedBy: "COPY docker-entrypoint.sh /usr/local/bin/ # buildkit", Size: "6.2kB", Created: "12 days ago"},
		{CreatedBy: "RUN /bin/sh -c apk add --no-cache su-exec tzdata && make install", Size: "34.1MB", Created: "12 days ago"},
		{CreatedBy: "ADD alpine-minirootfs-3.22.2-aarch64.tar.gz / # buildkit", Size: "7.2MB", Created: "3 weeks ago"},
	}, nil
}
func (d *Demo) Stats(_ context.Context, p Profile) ([]Stats, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	var s []Stats
	for _, c := range d.containers[p.Name] {
		if c.State == "running" {
			s = append(s, Stats{ID: c.ID, CPU: "0.12%", Memory: "42 MiB / 8 GiB", Network: "12 kB / 8 kB"})
		}
	}
	return s, nil
}
func (d *Demo) Run(ctx context.Context, op Operation, emit func(string)) error {
	emit("Demo: " + op.Kind + " on " + op.Profile.Name)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(350 * time.Millisecond):
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	switch op.Kind {
	case "create":
		for _, p := range d.profiles {
			if p.Name == op.Resources.Name {
				return fmt.Errorf("profile already exists")
			}
		}
		d.profiles = append(d.profiles, Profile{Name: op.Resources.Name, Status: "Running", CPUs: op.Resources.CPUs, Memory: int64(op.Resources.Memory * (1 << 30)), Disk: int64(op.Resources.Disk) << 30, Runtime: "docker", Arch: "aarch64"})
	case "start", "stop-profile", "restart-profile", "configure":
		for i := range d.profiles {
			p := &d.profiles[i]
			if p.Name != op.Profile.Name {
				continue
			}
			p.Status = "Running"
			if op.Kind == "stop-profile" {
				p.Status = "Stopped"
			}
			if op.Kind == "configure" {
				p.CPUs = op.Resources.CPUs
				p.Memory = int64(op.Resources.Memory * (1 << 30))
				p.Disk = int64(op.Resources.Disk) << 30
			}
		}
	case "pull-image":
		emit(op.Image.Tag + ": Pulling from " + op.Image.Repository)
		emit("Digest: sha256:4f2a…e91c")
		emit("Status: Image is up to date for " + op.Image.Ref())
	case "delete-image":
		images := d.images[op.Profile.Name]
		for i, img := range images {
			if img.ID == op.Image.ID {
				if img.Containers != "0" {
					return fmt.Errorf("image is being used by a container")
				}
				d.images[op.Profile.Name] = append(images[:i], images[i+1:]...)
				break
			}
		}
	case "delete-container":
		cs := d.containers[op.Profile.Name]
		for i, c := range cs {
			if c.ID == op.Container.ID {
				d.containers[op.Profile.Name] = append(cs[:i], cs[i+1:]...)
				break
			}
		}
	case "start-container", "stop-container", "restart-container", "start-project", "stop-project":
		cs := d.containers[op.Profile.Name]
		for i := range cs {
			if cs[i].ID == op.Container.ID || op.Project != "" && cs[i].Project == op.Project && (op.Kind == "start-project" || op.Kind == "stop-project") {
				cs[i].State = "running"
				cs[i].Status = "Up a moment"
				if op.Kind == "stop-container" || op.Kind == "stop-project" {
					cs[i].State = "exited"
					cs[i].Status = "Exited (0)"
				}
			}
		}
	}
	emit("Demo operation complete; no system resources changed.")
	return nil
}
func (d *Demo) Logs(_ context.Context, _ Profile, id, since string) (io.ReadCloser, error) {
	var lines []string
	for i := 0; i < 35; i++ {
		lines = append(lines, fmt.Sprintf("%s INFO [%s] request=%03d status=200 duration=12ms", time.Now().Add(time.Duration(i-35)*time.Second).UTC().Format(time.RFC3339Nano), id[:6], i))
	}
	return io.NopCloser(strings.NewReader(strings.Join(lines, "\n") + "\n")), nil
}
func (d *Demo) Shell(Profile, string) (*exec.Cmd, error) {
	return nil, fmt.Errorf("shell access is unavailable in demo mode")
}
