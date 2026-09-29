//go:build webkit2_41 || desktop

package frontend

import "embed"

// Dist holds the built frontend assets (the output of `npm run build` in
// frontend/). The embed pattern requires frontend/dist to exist at compile
// time; `wails build` populates it via the configured frontend:build command.
//
//go:embed all:dist
var Dist embed.FS
