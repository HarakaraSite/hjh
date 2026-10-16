package jsruntime

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-quickjs/go-quickjs"
	"github.com/go-quickjs/go-quickjs/stdlib"
	"hjh/internal/contracts"
)

func TestStdlibOuterRejectionDoesNotFireDueTimer(t *testing.T) {
	workspace := t.TempDir()
	filename := filepath.Join(workspace, "must-not-fire.txt")
	w := newStdlibWorker(t, Options{Snapshot: stdlibSnapshot(t, "", ""), Workspace: workspace})
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	code := fmt.Sprintf(`const fs=await import('fs');
 globalThis.failedCallState=true;
 setTimeout(()=>fs.writeFileSync(%q,'timer fired'),0);
 throw new Error('rejected-before-timer');`, filename)
	_, err := w.Call(ctx, "run_js", contracts.JSON(map[string]string{"code": code}))
	if err == nil || !strings.Contains(err.Error(), "rejected-before-timer") || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("outer rejection lost or classified as cancellation: %v", err)
	}
	if _, err := os.Stat(filename); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("due timer ran after outer rejection: %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ready"))
	}))
	defer server.Close()
	_, err = w.Call(ctx, "run_js", contracts.JSON(map[string]string{"code": fmt.Sprintf(`const fs=await import('fs');await tools.http({url:%q});setTimeout(()=>fs.writeFileSync(%q,'late timer fired'),0);throw new Error('late-rejection');`, server.URL, filename)}))
	if err == nil || !strings.Contains(err.Error(), "late-rejection") {
		t.Fatalf("late rejection lost: %v", err)
	}
	if _, err := os.Stat(filename); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("due timer ran after late outer rejection: %v", err)
	}
	if !w.Available() {
		t.Fatal("outer rejection retired Worker instead of VM")
	}
	if got := stdlibRun(t, w, `await new Promise(resolve=>setTimeout(resolve,0));return typeof failedCallState;`); string(got) != `"undefined"` {
		t.Fatalf("next call did not use a fresh working VM: %s", got)
	}
}

func TestStdlibLifecycleRootOutcomeAfterDrain(t *testing.T) {
	for _, name := range []string{"beforeTurn", "projectRequest"} {
		t.Run(name, func(t *testing.T) {
			var observations []contracts.Observation
			source := fmt.Sprintf(`export function %s({failure}) {
 if(failure==='root') throw new Error('lifecycle-root-error');
 if(failure==='timer') setTimeout(()=>{throw new Error('lifecycle-timer-error');},0);
 return 'PRIVATE_RETURN';
}`, name)
			w := newStdlibWorker(t, Options{Snapshot: stdlibSnapshot(t, "", source), Workspace: t.TempDir(), Observer: func(o contracts.Observation) error {
				observations = append(observations, o)
				return nil
			}})
			for _, failure := range []string{"timer", "root", "none"} {
				observations = nil
				value, err := w.Hook(t.Context(), name, contracts.JSON(map[string]string{"failure": failure}))
				phase := "completed"
				if failure != "none" {
					phase = "failed"
					if err == nil || !strings.Contains(err.Error(), "lifecycle-"+failure+"-error") {
						t.Fatalf("%s error lost: %v", failure, err)
					}
				} else {
					want := `"PRIVATE_RETURN"`
					if name == "projectRequest" {
						want = "null"
					}
					if err != nil || string(value) != want {
						t.Fatalf("successful hook: %s %v", value, err)
					}
				}
				if len(observations) != 2 || observations[0].Phase != "started" || observations[1].Phase != phase || observations[0].ID != observations[1].ID {
					t.Fatalf("%s root must end once as %s: %+v", failure, phase, observations)
				}
				end := observations[1]
				if failure != "none" && !strings.Contains(end.Error, "lifecycle-"+failure+"-error") {
					t.Fatalf("failure observation lost error: %+v", end)
				}
				if name == "projectRequest" && string(end.Result) != "null" {
					t.Fatalf("projectRequest return duplicated in history: %+v", end)
				}
			}
		})
	}
}

func TestRequestSettlementJoinsCancellationBeforeLoopReuse(t *testing.T) {
	rt := quickjs.New()
	defer rt.Close()
	loop := stdlib.NewLoop(rt)
	defer loop.Close()
	requestCtx, requestCancel := context.WithCancel(t.Context())
	defer requestCancel()
	callCtx, callCancel := context.WithCancel(t.Context())
	defer callCancel()
	entered, release := make(chan struct{}), make(chan struct{})
	// Pause the real AfterFunc callback immediately before it cancels the
	// call and closes its captured Loop, the former success-settlement race.
	stopRequest := cancelLoopOnRequest(requestCtx, func() {
		close(entered)
		<-release
		callCancel()
	}, loop)
	requestCancel()
	<-entered
	settled := make(chan struct{})
	go func() {
		stopRequest()
		close(settled)
	}()
	select {
	case <-settled:
		close(release)
		t.Fatal("settlement did not join the running cancellation callback")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	select {
	case <-settled:
	case <-time.After(3 * time.Second):
		t.Fatal("settlement did not finish after cancellation callback")
	}
	if !errors.Is(callCtx.Err(), context.Canceled) || !errors.Is(loop.Context().Err(), context.Canceled) {
		t.Fatal("settlement returned before call cancellation and Loop closure")
	}
	// runCall checks these contexts after stopRequest before emitting success;
	// its existing cancellation path retires the worker rather than reusing it.
	stopRequest() // Deferred settlement is also safe after the explicit join.
}
