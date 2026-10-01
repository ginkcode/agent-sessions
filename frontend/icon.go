package frontend

import _ "embed"

// AppIcon is the transparent window icon used outside macOS. The macOS app
// bundle's icns is generated from the same image.
//
//go:embed assets/Icon-universal.png
var AppIcon []byte
