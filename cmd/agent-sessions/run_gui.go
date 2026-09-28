//go:build webkit2_41

package main

import (
	"io/fs"
	"log"

	"github.com/ginkcode/agent-sessions/frontend"
	"github.com/ginkcode/agent-sessions/internal/app"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/linux"
)

// run launches the Wails v2 desktop application. It is only compiled with
// the webkit2_41 build tag (Linux WebKitGTK 4.1) so that default builds
// and tests never require GTK development headers.
func run() {
	application := app.NewApp()

	// The embedded FS is rooted at "dist"; the asset server expects
	// index.html at the FS root, so serve the "dist" subtree.
	assets, err := fs.Sub(frontend.Dist, "dist")
	if err != nil {
		log.Fatal(err)
	}

	err = wails.Run(&options.App{
		Title:       "Agent Sessions",
		Width:       1280,
		Height:      820,
		MinWidth:    960,
		MinHeight:   600,
		AssetServer: &assetserver.Options{Assets: assets},
		OnStartup:   application.OnStartup,
		Bind:        []interface{}{application},
		Linux: &linux.Options{
			WebviewGpuPolicy: linux.WebviewGpuPolicyOnDemand,
			ProgramName:      "agent-sessions",
		},
	})
	if err != nil {
		log.Fatal(err)
	}
}
