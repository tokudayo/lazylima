package engine

import (
	"context"
	"fmt"
	"math"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

type invocation struct {
	name      string
	args, env []string
	deadline  bool
}
type recorder struct {
	calls     []invocation
	responses func(string, []string) (string, error)
}

func TestProjectOperationsResolveAllMembersOnSelectedProfile(t *testing.T) {
	for _, verb := range []string{"start", "stop"} {
		t.Run(verb, func(t *testing.T) {
			r := &recorder{responses: func(name string, args []string) (string, error) {
				if name == "colima" {
					return `{"docker_socket":"unix:///tmp/work.sock"}`, nil
				}
				if len(args) > 2 && args[2] == "ps" {
					return `{"ID":"shop-running","State":"running","Labels":"com.docker.compose.project=shop"}
{"ID":"shop-stopped","State":"exited","Labels":"com.docker.compose.project=shop"}
{"ID":"other","State":"running","Labels":"com.docker.compose.project=shopping"}
{"ID":"standalone","State":"exited"}`, nil
				}
				return "", nil
			}}
			b := &Exec{Runner: r}
			err := b.Run(context.Background(), Operation{Kind: verb + "-project", Project: "shop", Profile: Profile{Name: "work", Runtime: "docker", Status: "Running"}}, func(string) {})
			if err != nil {
				t.Fatal(err)
			}
			id := "shop-stopped"
			if verb == "stop" {
				id = "shop-running"
			}
			want := []string{"--host", "unix:///tmp/work.sock", verb, id}
			if len(r.calls) != 3 || !reflect.DeepEqual(r.calls[2].args, want) {
				t.Fatalf("wrong project target: %+v", r.calls)
			}
		})
	}
}

func TestProjectOperationDoesNotMutateWithoutMembers(t *testing.T) {
	for _, project := range []string{"", "missing"} {
		r := &recorder{responses: func(name string, args []string) (string, error) {
			if name == "colima" {
				return `{"docker_socket":"unix:///tmp/work.sock"}`, nil
			}
			return "", nil
		}}
		b := &Exec{Runner: r}
		err := b.Run(context.Background(), Operation{Kind: "stop-project", Project: project, Profile: Profile{Name: "work", Runtime: "docker", Status: "Running"}}, func(string) {})
		if project == "" && err == nil {
			t.Fatal("empty project accepted")
		}
		if project != "" && err != nil {
			t.Fatal(err)
		}
		for _, c := range r.calls {
			if c.name == "docker" && c.args[2] != "ps" {
				t.Fatalf("unexpected mutation: %+v", c)
			}
		}
	}
}

func (r *recorder) Output(ctx context.Context, name string, args, env []string) ([]byte, error) {
	_, deadline := ctx.Deadline()
	r.calls = append(r.calls, invocation{name, args, env, deadline})
	if r.responses != nil {
		s, err := r.responses(name, args)
		return []byte(s), err
	}
	return nil, nil
}
func (r *recorder) Stream(ctx context.Context, name string, args, env []string, emit func(string)) error {
	_, err := r.Output(ctx, name, args, env)
	emit("progress")
	return err
}
func TestProfileFormats(t *testing.T) {
	for _, data := range []string{`[{"name":"z"},{"name":"a"}]`, "{\"name\":\"z\"}\n{\"name\":\"a\"}", `{"name":"a"}`, ""} {
		ps, err := ParseProfiles([]byte(data))
		if err != nil {
			t.Fatal(err)
		}
		if len(ps) > 0 && ps[0].Name != "a" {
			t.Fatalf("profiles not sorted: %v", ps)
		}
	}
	if _, err := ParseProfiles([]byte("garbage")); err == nil {
		t.Fatal("expected parse error")
	}
}
func TestTargetOverridesInheritedDockerEnvironment(t *testing.T) {
	t.Setenv("DOCKER_CONTEXT", "production")
	t.Setenv("DOCKER_HOST", "tcp://remote")
	t.Setenv("DOCKER_TLS_VERIFY", "1")
	r := &recorder{responses: func(name string, args []string) (string, error) {
		if name == "colima" {
			return `{"docker_socket":"unix:///tmp/test.sock"}`, nil
		}
		return `{"ID":"abc","Names":"redis","State":"running","Networks":"dev","Labels":"com.docker.compose.project=shop,com.docker.compose.service=cache"}`, nil
	}}
	b := &Exec{Runner: r}
	cs, err := b.Containers(context.Background(), Profile{Name: "dev", Runtime: "docker", Status: "Running"})
	if err != nil {
		t.Fatal(err)
	}
	call := r.calls[len(r.calls)-1]
	if !reflect.DeepEqual(call.args[:2], []string{"--host", "unix:///tmp/test.sock"}) {
		t.Fatalf("wrong endpoint: %v", call.args)
	}
	for _, v := range call.env {
		if strings.HasPrefix(v, "DOCKER_CONTEXT=") || strings.HasPrefix(v, "DOCKER_HOST=") || strings.HasPrefix(v, "DOCKER_TLS_VERIFY=") {
			t.Fatalf("inherited endpoint: %s", v)
		}
	}
	if len(cs) != 1 || cs[0].Project != "shop" || cs[0].Service != "cache" {
		t.Fatalf("labels: %+v", cs)
	}
	for _, call := range r.calls {
		if !call.deadline {
			t.Fatal("unbounded command")
		}
	}
}
func TestSocketResolutionFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name, data string
		err        error
	}{
		{"status error", "", fmt.Errorf("status failed")},
		{"invalid JSON", "invalid", nil},
		{"missing socket", `{}`, nil},
		{"empty socket", `{"docker_socket":""}`, nil},
		{"relative socket", `{"docker_socket":"unix://relative"}`, nil},
		{"remote socket", `{"docker_socket":"tcp://example.com:2375"}`, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &recorder{responses: func(string, []string) (string, error) { return tc.data, tc.err }}
			b := &Exec{Runner: r}
			p := Profile{Name: "work", Status: "Running", Runtime: "docker"}
			err := b.Run(context.Background(), Operation{Kind: "stop-container", Profile: p, Container: Container{ID: "abc"}}, func(string) {})
			if err == nil || !strings.Contains(err.Error(), "could not resolve Docker socket") {
				t.Fatalf("expected socket error, got %v", err)
			}
			if len(r.calls) != 1 || r.calls[0].name != "colima" {
				t.Fatalf("unexpected command: %+v", r.calls)
			}
		})
	}
}

func TestStoppedProfilesAreNotQueried(t *testing.T) {
	r := &recorder{}
	b := &Exec{Runner: r}
	_, err := b.Containers(context.Background(), Profile{Name: "work", Status: "Stopped", Runtime: "docker"})
	if err != nil || len(r.calls) > 0 {
		t.Fatalf("stopped profile: error %v, calls %+v", err, r.calls)
	}
}
func TestResourceValidation(t *testing.T) {
	p := Profile{Name: "dev", Disk: 100 << 30}
	good := Resources{Name: "dev", CPUs: 4, Memory: 8, Disk: 100}
	if err := good.Validate(&p); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*Resources){func(r *Resources) { r.Name = "../escape" }, func(r *Resources) { r.CPUs = 0 }, func(r *Resources) { r.Memory = math.NaN() }, func(r *Resources) { r.Memory = math.Inf(1) }, func(r *Resources) { r.Disk = 99 }} {
		r := good
		mutate(&r)
		if err := r.Validate(&p); err == nil {
			t.Fatalf("accepted invalid %+v", r)
		}
	}
}
func TestConfigureStopThenStartWithoutActivatingContext(t *testing.T) {
	r := &recorder{responses: func(name string, args []string) (string, error) {
		if args[0] == "list" {
			return `{"name":"dev","status":"Running","disk":107374182400}`, nil
		}
		return "", nil
	}}
	b := &Exec{Runner: r}
	err := b.Run(context.Background(), Operation{Kind: "configure", Profile: Profile{Name: "dev"}, Resources: Resources{Name: "dev", CPUs: 4, Memory: 8, Disk: 120}}, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.calls) != 3 || r.calls[1].args[0] != "stop" || r.calls[2].args[0] != "start" {
		t.Fatalf("wrong sequence %+v", r.calls)
	}
	if !strings.Contains(strings.Join(r.calls[2].args, " "), "--activate=false") {
		t.Fatal("changed global context")
	}
}
func TestShrinkRejectedBeforeStoppingVM(t *testing.T) {
	r := &recorder{responses: func(string, []string) (string, error) {
		return `{"name":"dev","status":"Running","disk":107374182400}`, nil
	}}
	b := &Exec{Runner: r}
	err := b.Run(context.Background(), Operation{Kind: "configure", Resources: Resources{Name: "dev", CPUs: 2, Memory: 2, Disk: 20}}, func(string) {})
	if err == nil || len(r.calls) != 1 {
		t.Fatalf("shrink made changes: %v %+v", err, r.calls)
	}
}
func TestDeletionRechecksStateAndNeverForces(t *testing.T) {
	for _, state := range []string{"running", "paused", "exited"} {
		t.Run(state, func(t *testing.T) {
			r := &recorder{responses: func(name string, args []string) (string, error) {
				if name == "colima" {
					return `{"docker_socket":"unix:///tmp/dev.sock"}`, nil
				}
				if args[2] == "inspect" {
					return state, nil
				}
				return "", nil
			}}
			b := &Exec{Runner: r}
			err := b.Run(context.Background(), Operation{Kind: "delete-container", Profile: Profile{Name: "dev", Runtime: "docker"}, Container: Container{ID: "abc"}}, func(string) {})
			deleted := false
			for _, call := range r.calls {
				if call.name == "docker" && call.args[2] == "rm" {
					deleted = true
					if strings.Contains(strings.Join(call.args, " "), "force") {
						t.Fatal("forced removal")
					}
				}
			}
			if state == "exited" {
				if err != nil || !deleted {
					t.Fatalf("did not delete stopped container: %v", err)
				}
			} else if err == nil || deleted {
				t.Fatal("deleted active container")
			}
		})
	}
}
func TestQueryCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := (OSRunner{}).Output(ctx, "sleep", []string{"5"}, os.Environ())
	if err == nil || time.Since(start) > time.Second {
		t.Fatalf("cancellation failed: %v", err)
	}
}
func TestSocketIsCachedUntilFailureOrOperation(t *testing.T) {
	fail := false
	r := &recorder{responses: func(name string, args []string) (string, error) {
		if name == "colima" {
			return `{"docker_socket":"unix:///tmp/work.sock"}`, nil
		}
		if fail {
			return "", fmt.Errorf("connection refused")
		}
		return "", nil
	}}
	b := &Exec{Runner: r}
	p := Profile{Name: "work", Status: "Running", Runtime: "docker"}
	statusCalls := func() int {
		n := 0
		for _, c := range r.calls {
			if c.name == "colima" && c.args[0] == "status" {
				n++
			}
		}
		return n
	}
	b.Containers(context.Background(), p)
	b.Containers(context.Background(), p)
	if n := statusCalls(); n != 1 {
		t.Fatalf("asked Colima for the socket %d times, want 1", n)
	}
	if got := r.calls[len(r.calls)-1].args[:2]; !reflect.DeepEqual(got, []string{"--host", "unix:///tmp/work.sock"}) {
		t.Fatal(got)
	}
	fail = true
	b.Containers(context.Background(), p)
	fail = false
	b.Containers(context.Background(), p)
	if n := statusCalls(); n != 2 {
		t.Fatalf("socket not looked up again after a failure (%d lookups)", n)
	}
	b.Run(context.Background(), Operation{Kind: "stop-profile", Profile: p}, func(string) {})
	b.Containers(context.Background(), p)
	if n := statusCalls(); n != 3 {
		t.Fatalf("socket not looked up again after an operation (%d lookups)", n)
	}
}
func TestImageCommands(t *testing.T) {
	r := &recorder{responses: func(name string, args []string) (string, error) {
		if name == "colima" {
			return `{"docker_socket":"unix:///tmp/d.sock"}`, nil
		}
		if len(args) > 3 && args[2] == "image" && args[3] == "ls" {
			return `{"ID":"b","Repository":"<none>","Tag":"<none>","Size":"1MB","Containers":"0"}
{"ID":"a","Repository":"redis","Tag":"alpine","Size":"40MB","Containers":"1"}`, nil
		}
		return "", nil
	}}
	b := &Exec{Runner: r}
	p := Profile{Name: "default", Status: "Running", Runtime: "docker"}
	images, err := b.Images(context.Background(), p)
	if err != nil || len(images) != 2 || images[0].Ref() != "redis:alpine" || images[1].Ref() != "b" {
		t.Fatalf("images %+v %v", images, err)
	}
	if _, err := b.History(context.Background(), p, "--evil"); err == nil {
		t.Fatal("history accepted a flag as an image ID")
	}
	last := func() []string { return r.calls[len(r.calls)-1].args[2:] }
	b.Run(context.Background(), Operation{Kind: "pull-image", Profile: p, Image: images[0]}, func(string) {})
	if got := last(); !reflect.DeepEqual(got, []string{"pull", "redis:alpine"}) {
		t.Fatalf("pull ran %v", got)
	}
	if err := b.Run(context.Background(), Operation{Kind: "pull-image", Profile: p, Image: images[1]}, func(string) {}); err == nil {
		t.Fatal("pulled an untagged image")
	}
	b.Run(context.Background(), Operation{Kind: "delete-image", Profile: p, Image: images[0]}, func(string) {})
	if got := last(); !reflect.DeepEqual(got, []string{"image", "rm", "a"}) {
		t.Fatalf("delete ran %v (must not force)", got)
	}
}
