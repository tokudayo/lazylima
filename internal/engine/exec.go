package engine

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"
)

type Runner interface {
	Output(context.Context, string, []string, []string) ([]byte, error)
	Stream(context.Context, string, []string, []string, func(string)) error
}
type OSRunner struct{}

func (OSRunner) Output(ctx context.Context, name string, args, env []string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = env
	cmd.WaitDelay = 2 * time.Second
	out, err := cmd.Output()
	if ctx.Err() != nil {
		return nil, fmt.Errorf("%s: %w", name, ctx.Err())
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return nil, fmt.Errorf("%s: %s (%w)", name, strings.TrimSpace(string(exit.Stderr)), err)
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	return out, nil
}

type Exec struct {
	Runner Runner

	// sockets caches each running profile's Docker socket; asking Colima
	// costs ~0.3 s per call and the answer only changes across VM restarts.
	mu      sync.Mutex
	sockets map[string]string
}

func (b *Exec) socket(name string) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.sockets[name]
}
func (b *Exec) remember(name, socket string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.sockets == nil {
		b.sockets = map[string]string{}
	}
	b.sockets[name] = socket
}
func (b *Exec) forget(names ...string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, n := range names {
		delete(b.sockets, n)
	}
}

func New() *Exec { return &Exec{Runner: OSRunner{}} }
func (b *Exec) output(ctx context.Context, timeout time.Duration, name string, args, env []string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return b.Runner.Output(ctx, name, args, env)
}

const QueryTimeout = 12 * time.Second

func (b *Exec) Profiles(ctx context.Context) ([]Profile, error) {
	data, err := b.output(ctx, QueryTimeout, "colima", []string{"list", "--json"}, os.Environ())
	if err != nil {
		return nil, err
	}
	ps, err := ParseProfiles(data)
	for _, p := range ps {
		if !p.Running() {
			b.forget(p.Name) // stopped outside LazyLima; its socket may change
		}
	}
	return ps, err
}
func ParseProfiles(data []byte) ([]Profile, error) {
	result := []Profile{}
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return result, nil
	}
	if data[0] == '[' {
		if err := json.Unmarshal(data, &result); err != nil {
			return nil, fmt.Errorf("invalid Colima profile list: %w", err)
		}
	} else {
		dec := json.NewDecoder(bytes.NewReader(data))
		for {
			var p Profile
			err := dec.Decode(&p)
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, fmt.Errorf("invalid Colima profile list: %w", err)
			}
			result = append(result, p)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result, nil
}

// Strip all inherited endpoint/TLS overrides before explicitly targeting a profile.
func DockerEnv(env []string) []string {
	result := []string{}
	for _, v := range env {
		k, _, _ := strings.Cut(v, "=")
		switch k {
		case "DOCKER_HOST", "DOCKER_CONTEXT", "DOCKER_TLS", "DOCKER_TLS_VERIFY", "DOCKER_CERT_PATH", "DOCKER_API_VERSION":
			continue
		}
		result = append(result, v)
	}
	return result
}
func (b *Exec) dockerArgs(ctx context.Context, p Profile, args ...string) ([]string, error) {
	if !p.Docker() {
		return nil, fmt.Errorf("profile %s uses %s; container management currently supports Docker", p.Name, p.Runtime)
	}
	if socket := b.socket(p.Name); socket != "" {
		return append([]string{"--host", socket}, args...), nil
	}
	data, err := b.output(ctx, QueryTimeout, "colima", []string{"status", "--json", "--profile", p.Name}, os.Environ())
	var status struct {
		Socket string `json:"docker_socket"`
	}
	if err != nil {
		return nil, fmt.Errorf("could not resolve Docker socket for profile %q: %w", p.Name, err)
	}
	if err := json.Unmarshal(data, &status); err != nil {
		return nil, fmt.Errorf("could not resolve Docker socket for profile %q: invalid Colima status: %w", p.Name, err)
	}
	if !strings.HasPrefix(status.Socket, "unix:///") || len(status.Socket) <= len("unix:///") {
		return nil, fmt.Errorf("could not resolve Docker socket for profile %q: missing or invalid Unix socket", p.Name)
	}
	b.remember(p.Name, status.Socket)
	return append([]string{"--host", status.Socket}, args...), nil
}
func (b *Exec) docker(ctx context.Context, p Profile, timeout time.Duration, args ...string) ([]byte, error) {
	argv, err := b.dockerArgs(ctx, p, args...)
	if err != nil {
		return nil, err
	}
	out, err := b.output(ctx, timeout, "docker", argv, DockerEnv(os.Environ()))
	if err != nil {
		b.forget(p.Name) // the VM may have moved; look the socket up again next time
	}
	return out, err
}
func decodeLines[T any](data []byte) ([]T, error) {
	var result []T
	dec := json.NewDecoder(bytes.NewReader(data))
	for {
		var v T
		err := dec.Decode(&v)
		if err == io.EOF {
			return result, nil
		}
		if err != nil {
			return nil, err
		}
		result = append(result, v)
	}
}
func (b *Exec) Containers(ctx context.Context, p Profile) ([]Container, error) {
	if !p.Running() {
		return nil, nil
	}
	out, err := b.docker(ctx, p, QueryTimeout, "ps", "--all", "--no-trunc", "--format", "{{json .}}")
	if err != nil {
		return nil, err
	}
	cs, err := decodeLines[Container](out)
	if err != nil {
		return nil, fmt.Errorf("invalid container list: %w", err)
	}
	for i := range cs {
		for _, label := range strings.Split(cs[i].Labels, ",") {
			k, v, _ := strings.Cut(label, "=")
			switch k {
			case "com.docker.compose.project":
				cs[i].Project = v
			case "com.docker.compose.service":
				cs[i].Service = v
			}
		}
	}
	sort.Slice(cs, func(i, j int) bool {
		if cs[i].Active() != cs[j].Active() {
			return cs[i].Active()
		}
		return cs[i].Names < cs[j].Names
	})
	return cs, nil
}
func (b *Exec) Images(ctx context.Context, p Profile) ([]Image, error) {
	if !p.Running() || !p.Docker() {
		return nil, nil
	}
	out, err := b.docker(ctx, p, QueryTimeout, "image", "ls", "--format", "{{json .}}")
	if err != nil {
		return nil, err
	}
	images, err := decodeLines[Image](out)
	if err != nil {
		return nil, fmt.Errorf("invalid image list: %w", err)
	}
	sort.SliceStable(images, func(i, j int) bool {
		if images[i].Dangling() != images[j].Dangling() {
			return !images[i].Dangling()
		}
		return images[i].Ref() < images[j].Ref()
	})
	return images, nil
}
func (b *Exec) History(ctx context.Context, p Profile, imageID string) ([]Layer, error) {
	if strings.HasPrefix(imageID, "-") {
		return nil, fmt.Errorf("invalid image ID")
	}
	out, err := b.docker(ctx, p, QueryTimeout, "image", "history", "--format", "{{json .}}", imageID)
	if err != nil {
		return nil, err
	}
	return decodeLines[Layer](out)
}
func (b *Exec) Stats(ctx context.Context, p Profile) ([]Stats, error) {
	if !p.Running() || !p.Docker() {
		return nil, nil
	}
	out, err := b.docker(ctx, p, 8*time.Second, "stats", "--no-stream", "--format", "{{json .}}")
	if err != nil {
		return nil, err
	}
	return decodeLines[Stats](out)
}
func (b *Exec) Run(ctx context.Context, op Operation, emit func(string)) error {
	// Profile operations can restart the VM, so drop cached sockets on both sides.
	b.forget(op.Profile.Name, op.Resources.Name)
	defer b.forget(op.Profile.Name, op.Resources.Name)
	// Long VM operations get a larger deadline than daemon operations.
	timeout := 2 * time.Minute
	if op.Kind == "create" || op.Kind == "configure" || op.Kind == "start" || op.Kind == "restart-profile" || op.Kind == "pull-image" {
		timeout = 10 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	docker := func(args ...string) error {
		emit("docker " + strings.Join(args, " "))
		out, err := b.docker(ctx, op.Profile, timeout, args...)
		if len(out) > 0 {
			emit(string(out))
		}
		return err
	}
	colima := func(args ...string) error {
		emit("colima " + strings.Join(args, " "))
		return b.stream(ctx, "colima", args, os.Environ(), emit)
	}
	switch op.Kind {
	case "start":
		return colima("start", "--profile", op.Profile.Name, "--activate=false")
	case "stop-profile":
		return colima("stop", "--profile", op.Profile.Name)
	case "restart-profile":
		if err := colima("stop", "--profile", op.Profile.Name); err != nil {
			return err
		}
		return colima("start", "--profile", op.Profile.Name, "--activate=false")
	case "create", "configure":
		profiles, err := b.Profiles(ctx)
		if err != nil {
			return err
		}
		var existing *Profile
		for i := range profiles {
			if profiles[i].Name == op.Resources.Name {
				existing = &profiles[i]
			}
		}
		if op.Kind == "create" && existing != nil {
			return fmt.Errorf("profile %s already exists", op.Resources.Name)
		}
		if op.Kind == "configure" && existing == nil {
			return fmt.Errorf("profile disappeared; refresh and try again")
		}
		if err := op.Resources.Validate(existing); err != nil {
			return err
		}
		if existing != nil && existing.Running() {
			if err := colima("stop", "--profile", existing.Name); err != nil {
				return err
			}
		}
		args := op.Resources.Args()
		if op.Kind == "create" {
			args = append(args, "--runtime", "docker")
		}
		return colima(args...)
	case "start-container", "stop-container", "restart-container":
		verb := strings.TrimSuffix(op.Kind, "-container")
		return docker(verb, op.Container.ID)
	case "start-project", "stop-project":
		if op.Project == "" {
			return fmt.Errorf("no Compose project selected")
		}
		// Resolve membership again at execution time, independently of UI filters.
		cs, err := b.Containers(ctx, op.Profile)
		if err != nil {
			return err
		}
		args := []string{strings.TrimSuffix(op.Kind, "-project")}
		for _, c := range cs {
			if c.Project == op.Project && (op.Kind == "start-project" && !c.Active() || op.Kind == "stop-project" && c.Active()) {
				args = append(args, c.ID)
			}
		}
		if len(args) == 1 {
			emit("No containers need changing in Compose project " + op.Project)
			return nil
		}
		return docker(args...)
	case "delete-container":
		out, err := b.docker(ctx, op.Profile, QueryTimeout, "inspect", "--format", "{{.State.Status}}", op.Container.ID)
		if err != nil {
			return err
		}
		state := strings.TrimSpace(string(out))
		if state != "exited" && state != "created" && state != "dead" {
			return fmt.Errorf("container is %s; stop it before deleting", state)
		}
		return docker("rm", op.Container.ID)
	case "pull-image":
		if op.Image.Dangling() {
			return fmt.Errorf("an untagged image can't be pulled")
		}
		// Stream it: pulls are slow and Docker reports progress per layer.
		argv, err := b.dockerArgs(ctx, op.Profile, "pull", op.Image.Ref())
		if err != nil {
			return err
		}
		emit("docker pull " + op.Image.Ref())
		return b.stream(ctx, "docker", argv, DockerEnv(os.Environ()), emit)
	case "delete-image":
		// No --force: Docker refuses images any container (even a stopped one) uses.
		return docker("image", "rm", op.Image.ID)
	}
	return fmt.Errorf("unknown operation %q", op.Kind)
}

// Stream progress in bounded chunks. Scanner errors (including oversized output)
// terminate the process rather than leaving it blocked on a pipe.
func (b *Exec) stream(ctx context.Context, name string, args, env []string, emit func(string)) error {
	return b.Runner.Stream(ctx, name, args, env, emit)
}
func (OSRunner) Stream(ctx context.Context, name string, args, env []string, emit func(string)) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = env
	cmd.WaitDelay = 2 * time.Second
	reader, writer := io.Pipe()
	cmd.Stdout = writer
	cmd.Stderr = writer
	if err := cmd.Start(); err != nil {
		reader.Close()
		writer.Close()
		return err
	}
	done := make(chan error, 1)
	go func() { err := cmd.Wait(); writer.Close(); done <- err }()
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	for scanner.Scan() {
		emit(scanner.Text())
	}
	scanErr := scanner.Err()
	reader.Close()
	if scanErr != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	err := <-done
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if scanErr != nil {
		return scanErr
	}
	return err
}

type processReader struct {
	io.ReadCloser
	cancel context.CancelFunc
	once   sync.Once
}

func (r *processReader) Close() error { r.once.Do(func() { r.cancel() }); return r.ReadCloser.Close() }
func (b *Exec) Logs(ctx context.Context, p Profile, id, since string) (io.ReadCloser, error) {
	ctx, cancel := context.WithCancel(ctx)
	args := []string{"logs", "--timestamps", "--follow", "--tail", "200"}
	if since != "" {
		args = []string{"logs", "--timestamps", "--follow", "--since", since}
	}
	args = append(args, id)
	argv, err := b.dockerArgs(ctx, p, args...)
	if err != nil {
		cancel()
		return nil, err
	}
	cmd := exec.CommandContext(ctx, "docker", argv...)
	cmd.Env = DockerEnv(os.Environ())
	cmd.WaitDelay = 2 * time.Second
	reader, writer := io.Pipe()
	cmd.Stdout = writer
	cmd.Stderr = writer
	if err := cmd.Start(); err != nil {
		cancel()
		reader.Close()
		writer.Close()
		return nil, err
	}
	go func() { err := cmd.Wait(); writer.CloseWithError(err) }()
	return &processReader{ReadCloser: reader, cancel: cancel}, nil
}
func (b *Exec) Shell(p Profile, id string) (*exec.Cmd, error) {
	ctx, cancel := context.WithTimeout(context.Background(), QueryTimeout)
	defer cancel()
	args, err := b.dockerArgs(ctx, p, "exec", "-it", id, "sh", "-c", "command -v bash >/dev/null 2>&1 && exec bash || exec sh")
	if err != nil {
		return nil, err
	}
	cmd := exec.Command("docker", args...)
	cmd.Env = DockerEnv(os.Environ())
	cmd.WaitDelay = 2 * time.Second
	return cmd, nil
}
