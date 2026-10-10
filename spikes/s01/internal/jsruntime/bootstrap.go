package jsruntime

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"path/filepath"
)

//go:embed bootstrap.js
var bootstrapSource string

// Options selects an external hook module for this fresh Worker.
// Empty Hooks is the S-01/S-02 execution path without lifecycle hooks.
type Options struct {
	Hooks string
}

func bootstrap(moduleDir, executionID string, options Options) string {
	path, _ := json.Marshal(filepath.Join(moduleDir, "index.js"))
	execution, _ := json.Marshal(executionID)
	hooks := "const hooks = {};"
	if options.Hooks != "" {
		path, _ := json.Marshal(options.Hooks)
		hooks = fmt.Sprintf("import * as hooks from %s;", path)
	}
	return fmt.Sprintf(bootstrapSource, path, hooks, execution)
}
