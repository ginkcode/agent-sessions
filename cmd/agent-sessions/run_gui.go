//go:build webkit2_41 || desktop

package main

import (
	"io/fs"
	"log"
	"runtime"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/linux"

	"github.com/ginkcode/agent-sessions/frontend"
	"github.com/ginkcode/agent-sessions/internal/app"
)

// run launches the Wails v2 desktop application. It is only compiled with
// the webkit2_41 (Linux WebKitGTK 4.1) or desktop (macOS) build tag so that
// default builds and tests never require GTK development headers.
func run() {
	application := app.NewApp()

	// The embedded FS is rooted at "dist"; the asset server expects
	// index.html at the FS root, so serve the "dist" subtree.
	assets, err := fs.Sub(frontend.Dist, "dist")
	if err != nil {
		log.Fatal(err)
	}

	// On Linux, Wails turns a zero max size into a GTK hint equal to the
	// monitor size seen at startup and never refreshes it. After a scale or
	// monitor change that stale hint stops maximize short of the screen, so
	// pass a limit no display reaches.
	maxSize := 0
	if runtime.GOOS == "linux" {
		maxSize = 16384
	}

	err = wails.Run(&options.App{
		Title:       "Agent Sessions",
		Width:       1280,
		Height:      820,
		MinWidth:    960,
		MinHeight:   600,
		MaxWidth:    maxSize,
		MaxHeight:   maxSize,
		AssetServer: &assetserver.Options{Assets: assets},
		OnStartup:   application.OnStartup,
		OnShutdown:  application.OnShutdown,
		Bind:        []interface{}{application},
		Linux: &linux.Options{
			Icon:             frontend.AppIcon,
			WebviewGpuPolicy: linux.WebviewGpuPolicyOnDemand,
			ProgramName:      "agent-sessions",
		},
	})
	if err != nil {
		log.Fatal(err)
	}
}
