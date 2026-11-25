package session

import (
	"context"
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/aaryan-purohit/boxagent/internal/agent"
)

var (
	errCreate  = errors.New("create failed")
	errRead    = errors.New("read failed")
	errCopy    = errors.New("copy failed")
	errAgent   = errors.New("agent failed")
	errRun     = errors.New("run failed")
	errDestroy = errors.New("destroy failed")
)

// fake plays the sandbox and the agent, failing where told to.
type fake struct {
	createErr, readErr, copyErr, agentErr, runErr, destroyErr error

	onRun          func()
	log            []string
	destroyCtxErr  error
	destroyHasTime bool
}

func (f *fake) CopyIn(_ context.Context, path string, content []byte) error {
	f.log = append(f.log, "copy "+path+"="+string(content))
	return f.copyErr
}

func (f *fake) Destroy(ctx context.Context) error {
	f.log = append(f.log, "destroy")
	f.destroyCtxErr = ctx.Err()
	_, f.destroyHasTime = ctx.Deadline()
	return f.destroyErr
}

func (f *fake) Run(_ context.Context, task string) (agent.Result, error) {
	f.log = append(f.log, "run "+task)
	if f.onRun != nil {
		f.onRun()
	}
	return agent.Result{Text: "done"}, f.runErr
}

func (f *fake) options() Options[*fake] {
	return Options[*fake]{
		Task: "task",
		Seed: []SeedFile{{Path: "a", Read: func(context.Context) ([]byte, error) { return []byte("1"), f.readErr }}},
		Create: func(context.Context) (*fake, error) {
			f.log = append(f.log, "create")
			return f, f.createErr
		},
		NewAgent: func(*fake) (Runner, error) { return f, f.agentErr },
	}
}

func TestRun(t *testing.T) {
	t.Parallel()
	all := []string{"create", "copy a=1", "run task", "destroy"}
	tests := []struct {
		name    string
		fake    fake
		wantErr []error
		wantLog []string
	}{
		{"runs in order", fake{}, nil, all},
		{"destroys nothing if create fails", fake{createErr: errCreate}, []error{errCreate}, []string{"create"}},
		{"destroys after a seed read fails", fake{readErr: errRead}, []error{errRead}, []string{"create", "destroy"}},
		{"destroys after a seed copy fails", fake{copyErr: errCopy}, []error{errCopy}, []string{"create", "copy a=1", "destroy"}},
		{"destroys after agent setup fails", fake{agentErr: errAgent}, []error{errAgent}, []string{"create", "copy a=1", "destroy"}},
		{"destroys after the run fails", fake{runErr: errRun}, []error{errRun}, all},
		{"reports a destroy failure", fake{destroyErr: errDestroy}, []error{errDestroy}, all},
		{"reports run and destroy failures", fake{runErr: errRun, destroyErr: errDestroy}, []error{errRun, errDestroy}, all},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			res, err := Run(t.Context(), tc.fake.options())

			if len(tc.wantErr) == 0 && (err != nil || res.Text != "done") {
				t.Errorf("Run() = %+v, %v, want done", res, err)
			}
			for _, want := range tc.wantErr {
				if !errors.Is(err, want) {
					t.Errorf("Run() error = %v, want %v", err, want)
				}
			}
			if diff := cmp.Diff(tc.wantLog, tc.fake.log); diff != "" {
				t.Errorf("calls mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestRunDestroysWithLiveContextAfterCancel(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	f := &fake{onRun: cancel, runErr: context.Canceled}

	_, err := Run(ctx, f.options())

	if !errors.Is(err, context.Canceled) {
		t.Errorf("Run() error = %v, want context.Canceled", err)
	}
	if f.destroyCtxErr != nil || !f.destroyHasTime {
		t.Errorf("destroy context: error %v, has deadline %v, want a live context with a deadline", f.destroyCtxErr, f.destroyHasTime)
	}
}

func TestRunDestroysAfterPanic(t *testing.T) {
	t.Parallel()
	f := &fake{onRun: func() { panic("boom") }}
	defer func() {
		if recover() == nil {
			t.Error("panic was swallowed")
		}
		if f.log[len(f.log)-1] != "destroy" {
			t.Errorf("calls = %q, want destroy last", f.log)
		}
	}()

	_, _ = Run(t.Context(), f.options()) // panics
}

func TestNewIDIsUnique(t *testing.T) {
	t.Parallel()
	a, b := NewID(), NewID()
	if a == b || len(a) != 16 {
		t.Errorf("NewID() = %q then %q, want distinct 16-character IDs", a, b)
	}
}
