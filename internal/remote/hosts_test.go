package remote

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestValidateHostAlias(t *testing.T) {
	valid := []string{
		"myserver",
		"dev-box",
		"prod_1",
		"node.local",
		"alpha123",
		"user@box", // '@' is not forbidden by the charset
	}
	for _, a := range valid {
		if err := ValidateHostAlias(a); err != nil {
			t.Errorf("expected %q to be valid, got: %v", a, err)
		}
	}

	invalid := []string{
		"",
		"-flag-injection",
		"-oProxyCommand=evil",
		"has space",
		"has\ttab",
		"has\nnewline",
		"semi;colon",
		"pipe|cmd",
		"amp&ersand",
		"back`tick`",
		"dollar$var",
		"double\"quote",
		"single'quote",
		"*",
		"*.local",
		"?wildcard",
		"!negated",
		"<redirect>",
		"(subshell)",
		"#comment",
	}
	for _, a := range invalid {
		if err := ValidateHostAlias(a); !errors.Is(err, ErrInvalidHostAlias) {
			t.Errorf("expected %q to be rejected with ErrInvalidHostAlias, got: %v", a, err)
		}
	}
}

func TestParseConfigFile_BasicAndDedupe(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config")

	content := `
# Top-level comment
Host web-prod
    HostName 192.168.1.10
    User ubuntu
    Port 2222

Host db-prod cache-prod
    User dbadmin

# Wildcard should be skipped
Host *.internal
    User fallback

# Negation should be skipped
Host !forbidden allowed-host
    User allowed

# Duplicate host name should be deduped
Host web-prod
    Port 9999

# Flag-like alias should be skipped
Host -bad-flag
    User evil

# wsl: is reserved for WSL distributions
Host wsl:box
    HostName box.example
`
	if err := os.WriteFile(cfgPath, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}

	aliases, err := ParseConfigFile(cfgPath)
	if err != nil {
		t.Fatalf("ParseConfigFile failed: %v", err)
	}

	expectedNames := []string{"web-prod", "db-prod", "cache-prod", "allowed-host"}
	if len(aliases) != len(expectedNames) {
		t.Fatalf("got %d aliases, want %d: %+v", len(aliases), len(expectedNames), aliases)
	}

	for i, name := range expectedNames {
		if aliases[i].Name != name {
			t.Errorf("alias %d: got %q, want %q", i, aliases[i].Name, name)
		}
	}

	// Verify details on web-prod
	if aliases[0].HostName != "192.168.1.10" || aliases[0].User != "ubuntu" || aliases[0].Port != 2222 {
		t.Errorf("web-prod details mismatch: %+v", aliases[0])
	}

	// Verify details on db-prod and cache-prod
	if aliases[1].User != "dbadmin" || aliases[2].User != "dbadmin" {
		t.Errorf("multi-host details mismatch: db=%+v cache=%+v", aliases[1], aliases[2])
	}
}

func TestParseConfigFile_IncludeAndRecursion(t *testing.T) {
	dir := t.TempDir()
	confDir := filepath.Join(dir, "conf.d")
	if err := os.MkdirAll(confDir, 0700); err != nil {
		t.Fatal(err)
	}

	mainConfig := filepath.Join(dir, "config")
	inc1 := filepath.Join(confDir, "web.conf")
	inc2 := filepath.Join(confDir, "db.conf")

	// inc1 includes mainConfig back to test recursion prevention
	if err := os.WriteFile(inc1, []byte("Host web1\n  User web\nInclude "+mainConfig+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(inc2, []byte("Host db1\n  User db\n"), 0600); err != nil {
		t.Fatal(err)
	}

	mainContent := "Include " + filepath.Join(confDir, "*.conf") + "\nHost bastion\n  User root\n"
	if err := os.WriteFile(mainConfig, []byte(mainContent), 0600); err != nil {
		t.Fatal(err)
	}

	aliases, err := ParseConfigFile(mainConfig)
	if err != nil {
		t.Fatalf("ParseConfigFile with recursion failed: %v", err)
	}

	names := make(map[string]bool)
	for _, a := range aliases {
		names[a.Name] = true
	}

	if !names["web1"] || !names["db1"] || !names["bastion"] {
		t.Errorf("missing expected hosts from includes: %+v", aliases)
	}
}

func TestResolveHost_WithTempConfig(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config")

	content := `
Host mytest
    HostName 10.0.0.99
    User testuser
    Port 22222
`
	if err := os.WriteFile(cfgPath, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	res, err := ResolveHost(ctx, "ssh", "mytest", cfgPath)
	if err != nil {
		t.Fatalf("ResolveHost failed: %v", err)
	}

	if res.HostName != "10.0.0.99" {
		t.Errorf("got HostName %q, want 10.0.0.99", res.HostName)
	}
	if res.User != "testuser" {
		t.Errorf("got User %q, want testuser", res.User)
	}
	if res.Port != 22222 {
		t.Errorf("got Port %d, want 22222", res.Port)
	}
}
