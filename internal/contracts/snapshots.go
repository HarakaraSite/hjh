package contracts

// SnapshotStatus is the save state for the latest detail attached to one
// execution or metadata record. Detail availability is independent from the
// save state so retained memory and unavailable detail are distinguishable.
type SnapshotStatus struct {
	ExecutionID     string            `json:"executionId"`
	State           SnapshotSaveState `json:"state"`
	DetailAvailable bool              `json:"detailAvailable"`
	Frozen          bool              `json:"frozen"`
	Error           string            `json:"error,omitempty"`
}

// SnapshotSaveState reports whether the latest request/budget detail is held
// by the current process, persisted, or unavailable.
type SnapshotSaveState string

const (
	SnapshotSaveMemory      SnapshotSaveState = "memory"
	SnapshotSaveSaved       SnapshotSaveState = "saved"
	SnapshotSaveFailed      SnapshotSaveState = "failed"
	SnapshotSaveUnavailable SnapshotSaveState = "unavailable"
)
