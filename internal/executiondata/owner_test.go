package executiondata

import (
	"context"
	"errors"
	"testing"

	"hjh/internal/contracts"
)

func TestRootAndChildCommitsShareDurablePrefixAndCaptureGate(t *testing.T) {
	owner := NewOwner()
	started := make(chan uint64, 1)
	release := make(chan struct{})
	rootDone := make(chan error, 1)
	go func() {
		rootDone <- owner.Commit(context.Background(), contracts.ExecutionFact{RootExecutionID: "root", ExecutionID: "root"},
			func(sequence uint64) (contracts.ExecutionCommit, error) {
				started <- sequence
				<-release
				return contracts.ExecutionCommit{SequenceCount: 1, RootExecutionID: "root"}, nil
			})
	}()
	if sequence := <-started; sequence != 1 {
		t.Fatalf("root candidate sequence = %d, want 1", sequence)
	}

	type captured struct {
		rootID  string
		through uint64
	}
	captureResult := make(chan captured, 1)
	go func() {
		_, _, err := CaptureAndSubscribe(owner, func(rootID string, through uint64) (struct{}, error) {
			captureResult <- captured{rootID: rootID, through: through}
			return struct{}{}, nil
		}, func(contracts.ExecutionCommit) {})
		if err != nil {
			captureResult <- captured{rootID: "capture error: " + err.Error()}
		}
	}()
	close(release)
	if err := <-rootDone; err != nil {
		t.Fatal(err)
	}
	initial := <-captureResult
	if initial.rootID != "root" || initial.through != 1 {
		t.Fatalf("captured root/prefix = %q/%d, want root/1", initial.rootID, initial.through)
	}

	commits := make(chan contracts.ExecutionCommit, 1)
	_, subscription, err := CaptureAndSubscribe(owner, func(rootID string, through uint64) (struct{}, error) {
		if rootID != "root" || through != 1 {
			t.Fatalf("child capture sees root/prefix %q/%d", rootID, through)
		}
		return struct{}{}, nil
	}, func(commit contracts.ExecutionCommit) { commits <- commit })
	if err != nil {
		t.Fatal(err)
	}
	defer subscription.Close()
	if err := owner.Commit(context.Background(), contracts.ExecutionFact{RootExecutionID: "root", ExecutionID: "child"},
		func(sequence uint64) (contracts.ExecutionCommit, error) {
			if sequence != 2 {
				t.Fatalf("child candidate sequence = %d, want 2", sequence)
			}
			return contracts.ExecutionCommit{Facts: []contracts.ExecutionFact{{ExecutionID: "child"}}, SequenceCount: 1}, nil
		}); err != nil {
		t.Fatal(err)
	}
	commit := <-commits
	if commit.RootExecutionID != "root" || commit.ThroughSequence != 2 || owner.ThroughSequence() != 2 {
		t.Fatalf("published root/prefix = %q/%d, owner prefix %d", commit.RootExecutionID, commit.ThroughSequence, owner.ThroughSequence())
	}
}

func TestFailedCommitReusesCandidateSequence(t *testing.T) {
	owner := NewOwner()
	wantErr := errors.New("storage failed")
	if err := owner.Commit(context.Background(), contracts.ExecutionFact{RootExecutionID: "root"},
		func(sequence uint64) (contracts.ExecutionCommit, error) {
			if sequence != 1 {
				t.Fatalf("first candidate = %d, want 1", sequence)
			}
			return contracts.ExecutionCommit{}, wantErr
		}); !errors.Is(err, wantErr) {
		t.Fatalf("failed commit error = %v, want %v", err, wantErr)
	}
	if owner.ThroughSequence() != 0 {
		t.Fatalf("prefix advanced after failed save: %d", owner.ThroughSequence())
	}
	if err := owner.Commit(context.Background(), contracts.ExecutionFact{RootExecutionID: "root"},
		func(sequence uint64) (contracts.ExecutionCommit, error) {
			if sequence != 1 {
				t.Fatalf("retry candidate = %d, want reused sequence 1", sequence)
			}
			return contracts.ExecutionCommit{SequenceCount: 1}, nil
		}); err != nil {
		t.Fatal(err)
	}
	if owner.ThroughSequence() != 1 {
		t.Fatalf("prefix after successful retry = %d, want 1", owner.ThroughSequence())
	}
}
