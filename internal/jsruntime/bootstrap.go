package jsruntime

import _ "embed"

//go:embed bootstrap.js
var bridgeSource string

func bootstrapSource() (string, error) { return bridgeSource, nil }
