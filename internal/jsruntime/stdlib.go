package jsruntime

import (
	"net/http"
	"os"
	"strings"

	"github.com/go-quickjs/go-quickjs/stdlib"
)

func (o *vmOwner) installStdlib() error {
	env := o.host.CommandEnv
	client := o.host.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	return stdlib.Install(o.rt, stdlib.Config{
		Loop:    o.loop,
		FS:      &stdlib.FS{},
		Process: &stdlib.Process{Cwd: o.host.Workspace, Env: env},
		Fetch:   &stdlib.Fetch{Client: client, MaxBodyBytes: -1},
		Run: &stdlib.Run{
			Dir: o.host.Workspace, Env: env,
			Allow:          func(string, []string) error { return nil },
			MaxOutputBytes: 32 << 20,
		},
	})
}

// Credential configuration supplies names only. Do not resolve credentials or
// hand the unfiltered environment to JavaScript or either command path.
func commandEnvironment(credentialNames []string) map[string]string {
	excluded := map[string]bool{"OPENROUTER_API_KEY": true}
	for _, name := range credentialNames {
		excluded[name] = true
	}
	env := map[string]string{}
	for _, entry := range os.Environ() {
		name, value, ok := strings.Cut(entry, "=")
		if ok && !excluded[name] {
			env[name] = value
		}
	}
	return env
}
