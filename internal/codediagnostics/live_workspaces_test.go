package codediagnostics

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLiveWorkspaceCompositeOwnershipAndApproval(t *testing.T) {
	e := newEnv(t, SessionOptions{})
	root, tools, _ := fixture(t)
	for _, dir := range []string{"api", "api/deep", "web"} {
		if err := os.MkdirAll(filepath.Join(root.Project().Root, dir), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root.Project().Root, dir, "a.py"), []byte("x=1"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	state := ProviderState{ProviderID: "pyright", Availability: "available", Coverage: Coverage{Kind: "openedDocuments"}}
	var workspaces []LiveWorkspace
	for _, dir := range []string{"api", "api/deep", "web"} {
		cfg := ServerConfig{ProviderID: "pyright", Executable: tools[0].Executable, Directory: dir, AdapterPolicy: "workspace-test", Toolchain: []ToolInput{{ProviderID: strings.Repeat("r", 256), Executable: tools[0].Executable, Policy: "runtime"}}}
		workspaces = append(workspaces, LiveWorkspace{Server: cfg, Resolve: func(file string) (string, bool) { return "python", strings.HasSuffix(file, ".py") }})
	}
	regs := []Registration{{ProviderID: "pyright", Availability: "available", Live: &LiveRegistration{State: state, Workspaces: workspaces}}}
	docs, err := DiscoveryDocuments(root, regs, e.mgr)
	if err != nil {
		t.Fatal(err)
	}
	server, ok := docs.Resolve(root, "api/deep/a.py")
	if !ok || server.Config.Directory != "api/deep" {
		t.Fatal("deepest workspace not selected", server, ok)
	}
	if _, ok := docs.Resolve(root, "config"); ok {
		t.Fatal("unowned root file accepted")
	}
	all := liveTools(regs[0].Live)
	if len(all) != 6 {
		t.Fatal(len(all))
	}
	seen := map[string]bool{}
	for _, tool := range all {
		if len(tool.ProviderID) > 256 || seen[tool.ProviderID] {
			t.Fatal("unbounded or duplicate tool ID", tool.ProviderID)
		}
		seen[tool.ProviderID] = true
	}
	// A subset grant must never activate even one workspace of the attachment.
	fp, err := Fingerprint(root, server.Config.Tools())
	if err != nil {
		t.Fatal(err)
	}
	approve(t, e.trust, root, server.Config.Tools(), fp, "session")
	_, err = e.mgr.Attach(context.Background(), root, server.Config)
	code(t, err, "approvalRequired")
	fp, err = Fingerprint(root, all)
	if err != nil {
		t.Fatal(err)
	}
	approve(t, e.trust, root, all, fp, "session")
	if _, err = e.mgr.Attach(context.Background(), root, server.Config); err != nil {
		t.Fatal(err)
	}
	params := (&lspSession{root: root, config: server.Config}).initializeParams()
	want := fileURI(filepath.Join(root.Project().Root, "api/deep"))
	if params["rootUri"] != want {
		t.Fatal(params)
	}
	folders := params["workspaceFolders"].([]map[string]string)
	if len(folders) != 1 || folders[0]["uri"] != want {
		t.Fatal(folders)
	}
	if err := os.MkdirAll(filepath.Join(root.Project().Root, "api/unapproved"), 0700); err != nil {
		t.Fatal(err)
	}
	changed := server.Config
	changed.Directory = "api/unapproved"
	if _, err = e.mgr.Attach(context.Background(), root, changed); err == nil {
		t.Fatal("directory mutation reused approval")
	}
}

func TestCompositeToolBudgetSupportsManyWorkspaces(t *testing.T) {
	root, tools, _ := fixture(t)
	many := make([]ToolInput, 40)
	for i := range many {
		many[i] = tools[0]
		many[i].ProviderID = string(rune('A' + i))
	}
	if _, err := Fingerprint(root, many); err != nil {
		t.Fatal(err)
	}
	many = make([]ToolInput, maxProposalTools+1)
	if _, err := Fingerprint(root, many); err == nil {
		t.Fatal("unbounded proposal accepted")
	}
}

func TestLiveWorkspaceDirectoryCannotEscape(t *testing.T) {
	root, tools, _ := fixture(t)
	cfg := ServerConfig{ProviderID: "ruff", Executable: tools[0].Executable, Directory: "../outside"}
	state := ProviderState{ProviderID: "ruff", Availability: "available", Coverage: Coverage{Kind: "openedDocuments"}}
	r := Registration{ProviderID: "ruff", Availability: "available", Live: &LiveRegistration{State: state, Workspaces: []LiveWorkspace{{Server: cfg, Resolve: func(string) (string, bool) { return "python", true }}}}}
	if _, err := DiscoveryDocuments(root, []Registration{r}, nil); err == nil {
		t.Fatal("escaping workspace accepted")
	}
}
