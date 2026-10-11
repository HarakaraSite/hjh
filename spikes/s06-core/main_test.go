package main

import "testing"

func TestBlockedSSEAllowsIndependentRunPersistenceAndRecovery(t *testing.T) {
	for _, cancelRun := range []bool{false, true} {
		name := "completion"
		if cancelRun {
			name = "cancellation"
		}
		t.Run(name, func(t *testing.T) {
			r, err := experiment(16, cancelRun, false)
			if err != nil {
				t.Fatal(err)
			}
			if !r.SSEBlockedAtTerminal || !r.TerminalAfterRelease || !r.SSEBlockedAtShutdown || !r.ShutdownStreamExited || r.PersistedObservations != 0 {
				t.Fatalf("Core relied on SSE consumer: %+v", r)
			}
		})
	}
}
