package templates

import "embed"

// FS contains email templates.
//
//go:embed *.tmpl
var FS embed.FS
