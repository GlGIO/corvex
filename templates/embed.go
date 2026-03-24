package templates

import "embed"

//go:embed config.yaml agents/default.md context/README.md
var FS embed.FS
