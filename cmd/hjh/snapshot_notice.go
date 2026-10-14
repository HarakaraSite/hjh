package main

import (
	"fmt"
	"io"

	"hjh/internal/contracts"
)

func writeSnapshotNotice(stderr io.Writer, root contracts.SnapshotStatus, failedChildren []string) error {
	if root.State == contracts.SnapshotSaveFailed {
		detail := root.Error
		if detail == "" {
			detail = "latest request or budget detail remains available in Core memory"
		}
		if _, err := fmt.Fprintf(stderr, "snapshot save failed for execution %s: %s\n", root.ExecutionID, detail); err != nil {
			return err
		}
	}
	for _, executionID := range failedChildren {
		if _, err := fmt.Fprintf(stderr, "snapshot save failed for child execution %s\n", executionID); err != nil {
			return err
		}
	}
	return nil
}
