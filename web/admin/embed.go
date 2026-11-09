package admin

import "embed"

//go:embed templates/*.html static/*.css
var Files embed.FS
