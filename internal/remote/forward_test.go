package remote

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

func TestForwardTargetValidation(t *testing.T) {
	for _, host := range []string{"localhost", "db.internal", "127.0.0.1", "::1", "example.com."} {
		if err := ValidateForwardTarget(host, 80, 8080); err != nil {
			t.Errorf("%s: %v", host, err)
		}
	}
	for _, host := range []string{"", "-option", "a:b:c", "$(whoami)", "a b", "a\nb", "a/b", "a..b", "[::1]"} {
		if ValidateForwardTarget(host, 80, 8080) == nil {
			t.Errorf("accepted %q", host)
		}
	}
	for _, port := range []int{-1, 0, 65536} {
		if ValidateForwardTarget("localhost", port, 8080) == nil || ValidateForwardTarget("localhost", 80, port) == nil {
			t.Errorf("accepted port %d", port)
		}
	}
}
func TestForwardCommand(t *testing.T) {
	cmd, err := BuildForwardCmd(context.Background(), "server", "::1", 5432, 15432, SSHOptions{Binary: "ssh", ConfigFile: "/test/config"})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(cmd.Args, " ")
	for _, want := range []string{"-N -v -L 127.0.0.1:15432:[::1]:5432 -- server", "GatewayPorts=no", "ControlPath=none", "ExitOnForwardFailure=yes", "ForkAfterAuthentication=no", "-F /test/config"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %s in %s", want, joined)
		}
	}
	if !reflect.DeepEqual(cmd.Args[len(cmd.Args)-2:], []string{"--", "server"}) {
		t.Fatal("unexpected remote command", cmd.Args)
	}
	if _, err := BuildForwardCmd(context.Background(), "wsl:Ubuntu", "localhost", 80, 8080, SSHOptions{}); err == nil {
		t.Fatal("accepted WSL")
	}
}
