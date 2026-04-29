// Package tamagotchiweb embeds the tamagotchi web UI (HTML templates + CSS).
package tamagotchiweb

import "embed"

//go:embed index.html.tmpl widget.html.tmpl style.css
var FS embed.FS
