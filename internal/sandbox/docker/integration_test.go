//go:build integration

package docker

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/moby/moby/client"
)

const testImage = "golang:1.23-bookworm"

func newClient(t *testing.T) *client.Client {
	t.Helper()
	cli, err := client.New(client.FromEnv)
	if err != nil {
		t.Fatalf("docker client: %v", err)
	}
	t.Cleanup(func() { _ = cli.Close() }) // best effort
	return cli
}

func options(t *testing.T) Options {
	return Options{Image: testImage, SessionID: t.Name(), Log: slog.New(slog.DiscardHandler),
		OnDiskLimit: func(err error) { t.Errorf("OnDiskLimit(%v)", err) }}
}

func newSandbox(t *testing.T, opts Options) *Sandbox {
	t.Helper()
	s, err := New(t.Context(), newClient(t), opts)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Destroy(context.Background()); err != nil { // t.Context is done by now
			t.Errorf("Destroy() error: %v", err)
		}
	})
	return s
}

func mustExec(t *testing.T, s *Sandbox, cmd string) ExecResult {
	t.Helper()
	res, err := s.Exec(t.Context(), cmd, time.Minute)
	if err != nil {
		t.Fatalf("Exec(%q) error: %v", cmd, err)
	}
	return res
}

// sandboxes returns the IDs of containers belonging to a session.
func sandboxes(t *testing.T, cli *client.Client, session string) []string {
	t.Helper()
	found, err := cli.ContainerList(context.Background(), client.ContainerListOptions{
		All: true, Filters: make(client.Filters).Add("label", "boxagent.session="+session),
	})
	if err != nil {
		t.Fatalf("list containers: %v", err)
	}
	var ids []string
	for _, c := range found.Items {
		ids = append(ids, c.ID)
	}
	return ids
}

func TestIntegrationExec(t *testing.T) {
	s := newSandbox(t, options(t))
	tests := []struct {
		name, cmd string
		want      ExecResult
	}{
		{"separates streams", "echo out; echo err >&2", ExecResult{Stdout: "out\n", Stderr: "err\n"}},
		{"reports the exit code", "exit 3", ExecResult{ExitCode: 3}},
		{"runs in the workspace", "pwd", ExecResult{Stdout: "/workspace\n"}},
		{"runs as a non-root user", "id -u", ExecResult{Stdout: "1000\n"}},
		{"can write to workspace and home", "touch /workspace/x ~/y && echo ok", ExecResult{Stdout: "ok\n"}},
		{"cannot write elsewhere", "touch /etc/x 2>/dev/null", ExecResult{ExitCode: 1}},
		{"has no capabilities", "grep CapEff /proc/self/status", ExecResult{Stdout: "CapEff:\t0000000000000000\n"}},
		{"has only loopback", "ls /sys/class/net | grep -vE '^(lo|tunl0|sit0|gre0|gretap0|erspan0|ip_vti0|ip6_vti0|ip6tnl0|ip6gre0)$'", ExecResult{ExitCode: 1}},
		{"a self-kill is not a timeout", "kill -KILL $$", ExecResult{ExitCode: 137}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if diff := cmp.Diff(tc.want, mustExec(t, s, tc.cmd)); diff != "" {
				t.Errorf("Exec(%q) mismatch (-want +got):\n%s", tc.cmd, diff)
			}
		})
	}
}

func TestIntegrationGoToolchainWorks(t *testing.T) {
	s := newSandbox(t, options(t))
	src := "package main\nfunc main() { println(\"hi\") }\n"
	if err := s.CopyIn(t.Context(), "main.go", []byte(src)); err != nil {
		t.Fatalf("CopyIn() error: %v", err)
	}

	res := mustExec(t, s, "go mod init x && go run .")

	if res.ExitCode != 0 || !strings.HasSuffix(res.Stderr, "hi\n") {
		t.Errorf("go run = %+v, want hi", res)
	}
}

func TestIntegrationNoNetworkByDefault(t *testing.T) {
	s := newSandbox(t, options(t))

	res := mustExec(t, s, "curl -sS --max-time 5 http://1.1.1.1")

	if res.ExitCode == 0 {
		t.Errorf("curl = %+v, want a failure", res)
	}
}

func TestIntegrationExecTimeout(t *testing.T) {
	s := newSandbox(t, options(t))

	res, err := s.Exec(t.Context(), "echo before; sleep 1001 & sleep 1002", 2*time.Second)

	if err != nil {
		t.Fatalf("Exec() error: %v", err)
	}
	if diff := cmp.Diff(ExecResult{Stdout: "before\n", ExitCode: 137, TimedOut: true}, res); diff != "" {
		t.Errorf("Exec() mismatch (-want +got):\n%s", diff)
	}
	ps := mustExec(t, s, "cat /proc/[0-9]*/cmdline | tr '\\0' ' '")
	if strings.Contains(ps.Stdout, "sleep 100"+"1") || strings.Contains(ps.Stdout, "sleep 100"+"2") {
		t.Errorf("processes survived the timeout: %s", ps.Stdout)
	}
}

func TestIntegrationExecBackgroundProcessDoesNotHang(t *testing.T) {
	s := newSandbox(t, options(t))
	began := time.Now()

	res := mustExec(t, s, "sleep 1000 & echo started")

	if res.Stdout != "started\n" || res.TimedOut || time.Since(began) > 20*time.Second {
		t.Errorf("Exec() = %+v after %v, want a prompt return", res, time.Since(began))
	}
}

func TestIntegrationExecFloodIsBounded(t *testing.T) {
	s := newSandbox(t, options(t))

	res := mustExec(t, s, "yes | head -c 50000000; echo end")

	if len(res.Stdout) > maxStreamBytes+100 || !strings.HasSuffix(res.Stdout, "end\n") {
		t.Errorf("got %d bytes of stdout, want about %d ending in end", len(res.Stdout), maxStreamBytes)
	}
}

func TestIntegrationExecCancelled(t *testing.T) {
	s := newSandbox(t, options(t))
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	began := time.Now()

	_, err := s.Exec(ctx, "sleep 60", time.Minute)

	if !errors.Is(err, context.DeadlineExceeded) || time.Since(began) > 10*time.Second {
		t.Errorf("Exec() error = %v after %v, want a prompt context error", err, time.Since(began))
	}
}

func TestIntegrationLimits(t *testing.T) {
	s := newSandbox(t, options(t))

	res, err := s.cli.ContainerInspect(t.Context(), s.id, client.ContainerInspectOptions{})

	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	info := res.Container
	h := info.HostConfig
	if h.NanoCPUs != 1e9 || h.Memory != 1<<30 || h.MemorySwap != 1<<30 || h.PidsLimit == nil || *h.PidsLimit != 256 {
		t.Errorf("limits = %+v, want 1 CPU, 1 GiB, no swap, 256 pids", h.Resources)
	}
	if h.Privileged || len(h.Binds) != 0 || len(info.Mounts) != 0 || h.NetworkMode != "none" {
		t.Errorf("privileged %v, binds %v, mounts %v, network %s", h.Privileged, h.Binds, info.Mounts, h.NetworkMode)
	}
	if !slices.Equal(h.CapDrop, []string{"ALL"}) || !slices.Contains(h.SecurityOpt, "no-new-privileges") {
		t.Errorf("cap drop %v, security opts %v", h.CapDrop, h.SecurityOpt)
	}
}

func TestIntegrationNetworkOption(t *testing.T) {
	opts := options(t)
	opts.Network = true
	s := newSandbox(t, opts)

	res, err := s.cli.ContainerInspect(t.Context(), s.id, client.ContainerInspectOptions{})

	if err != nil || res.Container.HostConfig.NetworkMode != "bridge" {
		t.Errorf("inspect = %+v, %v, want network mode bridge", res.Container.HostConfig, err)
	}
}

func TestIntegrationCopyRoundTrip(t *testing.T) {
	s := newSandbox(t, options(t))
	for name, content := range map[string][]byte{
		"a.txt":        []byte("hi"),
		"deep/dir/b":   {0, 0xff, '\n'},
		"deep/empty":   {},
		"é sp/big.bin": bytes.Repeat([]byte("x"), 5<<20),
	} {
		if err := s.CopyIn(t.Context(), name, content); err != nil {
			t.Fatalf("CopyIn(%q) error: %v", name, err)
		}
		got, err := s.CopyOut(t.Context(), name)
		if err != nil || !bytes.Equal(got, content) {
			t.Errorf("CopyOut(%q) = %d bytes, %v, want %d bytes", name, len(got), err, len(content))
		}
	}
	res := mustExec(t, s, "echo more >> a.txt && touch deep/dir/new && cat a.txt")
	if res.ExitCode != 0 || res.Stdout != "himore\n" {
		t.Errorf("agent cannot edit copied files: %+v", res)
	}
}

func TestIntegrationCopyOutRejects(t *testing.T) {
	s := newSandbox(t, options(t))
	if res := mustExec(t, s, "ln -s /etc/passwd link; mkdir dir; mkfifo fifo; truncate -s 5242881 big"); res.ExitCode != 0 {
		t.Fatalf("setup: %+v", res)
	}
	for name, want := range map[string]error{"link": ErrNotRegular, "dir": ErrNotRegular, "fifo": ErrNotRegular, "big": ErrTooLarge} {
		if _, err := s.CopyOut(t.Context(), name); !errors.Is(err, want) {
			t.Errorf("CopyOut(%q) error = %v, want %v", name, err, want)
		}
	}
	if _, err := s.CopyOut(t.Context(), "missing"); err == nil {
		t.Error("CopyOut(missing) = nil, want an error")
	}
}

func TestIntegrationDestroy(t *testing.T) {
	s := newSandbox(t, options(t))
	for range 2 {
		if err := s.Destroy(t.Context()); err != nil {
			t.Errorf("Destroy() error: %v", err)
		}
	}
	if ids := sandboxes(t, s.cli, t.Name()); len(ids) != 0 {
		t.Errorf("containers left: %v", ids)
	}
}

func TestIntegrationNewCleansUpWhenStartFails(t *testing.T) {
	cli := newClient(t)
	opts := options(t)
	opts.Image = "hello-world" // has no sleep, timeout or sh
	if _, err := New(t.Context(), cli, opts); !errors.Is(err, errUnusableImage) {
		t.Errorf("New() error = %v, want errUnusableImage", err)
	}
	if ids := sandboxes(t, cli, t.Name()); len(ids) != 0 {
		t.Errorf("containers left: %v", ids)
	}
}

func TestIntegrationCancelDuringNewLeavesNothing(t *testing.T) {
	cli := newClient(t)
	for _, after := range []time.Duration{0, 5, 20, 50, 100, 200} {
		ctx, cancel := context.WithTimeout(t.Context(), after*time.Millisecond)
		s, err := New(ctx, cli, options(t))
		cancel()
		if err == nil {
			if err := s.Destroy(t.Context()); err != nil {
				t.Errorf("Destroy() error: %v", err)
			}
		}
		if ids := sandboxes(t, cli, t.Name()); len(ids) != 0 {
			t.Errorf("cancel after %dms left containers: %v", after, ids)
		}
	}
}

func TestIntegrationMissingImage(t *testing.T) {
	opts := options(t)
	opts.Image = "boxagent.invalid/no-such-image:1"

	if _, err := New(t.Context(), newClient(t), opts); err == nil {
		t.Error("New() = nil error, want a pull failure")
	}
}

func TestIntegrationPrune(t *testing.T) {
	s := newSandbox(t, options(t))

	removed, err := Prune(t.Context(), s.cli)

	if err != nil || !slices.Contains(removed, s.id) {
		t.Errorf("Prune() = %v, %v, want it to include %s", removed, err, s.id)
	}
	if ids := sandboxes(t, s.cli, t.Name()); len(ids) != 0 {
		t.Errorf("containers left: %v", ids)
	}
}

func TestIntegrationDiskLimitKillsSandbox(t *testing.T) {
	s := newSandbox(t, options(t))
	s.stopWatch() // replace the real limit with a small one
	hit := make(chan error, 1)
	s.watchDisk(t.Context(), 50<<20, func(err error) { hit <- err })

	_, execErr := s.Exec(t.Context(), "head -c 100000000 /dev/zero > big; sleep 30", time.Minute)

	select {
	case err := <-hit:
		if !errors.Is(err, ErrDiskLimit) {
			t.Errorf("OnDiskLimit(%v), want ErrDiskLimit", err)
		}
	case <-time.After(10 * time.Second):
		t.Errorf("limit was not enforced; Exec() error = %v", execErr)
	}
	if res, err := s.Exec(t.Context(), "true", time.Second); err == nil {
		t.Errorf("Exec() after the limit = %+v, want an error", res)
	}
}

func TestIntegrationDestroyStopsDiskWatch(t *testing.T) {
	s := newSandbox(t, options(t))
	s.stopWatch()
	s.watchDisk(t.Context(), 1<<40, func(error) {})

	if err := s.Destroy(t.Context()); err != nil {
		t.Fatalf("Destroy() error: %v", err)
	}
	s.stopWatch() // returns only once the goroutine has ended
}

func TestIntegrationCopyInSetsModTime(t *testing.T) {
	s := newSandbox(t, options(t))
	if err := s.CopyIn(t.Context(), "f", nil); err != nil {
		t.Fatalf("CopyIn() error: %v", err)
	}

	if res := mustExec(t, s, "find f -mmin -10"); res.Stdout != "f\n" {
		t.Errorf("find = %+v, want f to be recent", res)
	}
}
