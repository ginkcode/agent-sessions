package app

import (
	"reflect"
	"testing"

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

func TestDialogFilters(t *testing.T) {
	bundles := wruntime.FileFilter{DisplayName: "Bundles", Pattern: "*.agent-session.zip;*.zip"}
	bundleOnly := wruntime.FileFilter{DisplayName: "Bundles", Pattern: "*.agent-session.zip"}
	markdown := wruntime.FileFilter{DisplayName: "Markdown", Pattern: "*.md"}
	all := wruntime.FileFilter{DisplayName: "All Files", Pattern: "*.*"}

	tests := []struct {
		name    string
		goos    string
		filters []wruntime.FileFilter
		want    []wruntime.FileFilter
	}{
		{
			name:    "linux keeps globs",
			goos:    "linux",
			filters: []wruntime.FileFilter{bundles, all},
			want:    []wruntime.FileFilter{bundles, all},
		},
		{
			name:    "darwin drops dotted and wildcard patterns",
			goos:    "darwin",
			filters: []wruntime.FileFilter{bundles, all},
			want:    []wruntime.FileFilter{{DisplayName: "Bundles", Pattern: "*.zip"}},
		},
		{
			name:    "darwin keeps plain extensions",
			goos:    "darwin",
			filters: []wruntime.FileFilter{markdown, all},
			want:    []wruntime.FileFilter{markdown},
		},
		{
			name:    "darwin with nothing usable allows every file",
			goos:    "darwin",
			filters: []wruntime.FileFilter{bundleOnly},
			want:    nil,
		},
		{
			name: "darwin trims spaces and skips odd patterns",
			goos: "darwin",
			filters: []wruntime.FileFilter{
				{DisplayName: "Mixed", Pattern: " *.json ; *; *.; report.txt; *.tar.gz ;*.JSONL"},
			},
			want: []wruntime.FileFilter{{DisplayName: "Mixed", Pattern: "*.json;*.JSONL"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := dialogFilters(tt.goos, tt.filters)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("dialogFilters(%q) = %#v, want %#v", tt.goos, got, tt.want)
			}
		})
	}
}
