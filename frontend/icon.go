package frontend

import _ "embed"

// AppIcon is the transparent window icon used outside macOS, where the app
// bundle's icns supplies the icon instead.
//
//go:embed assets/Icon-universal.png
var AppIcon []byte
