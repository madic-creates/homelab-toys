// Package webclustertv exposes the static assets for cluster-tv as an
// embed.FS. Keeping the embed in this package — rather than in cmd/cluster-tv —
// means the asset directory tree stays under web/, matching the layout of
// every other tool in this repo.
package webclustertv

import "embed"

//go:embed index.html.tmpl crt.css modern.css
var FS embed.FS
