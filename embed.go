package trasmetto

import "embed"

//go:embed static/html static/css static/js static/img static/fonts/unifont-ui.woff2
var StaticFiles embed.FS
