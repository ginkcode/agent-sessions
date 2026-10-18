package main

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Fontconfig's mono (100) and dual-width (90) classes both suit terminals.
// Use the primary family name rather than a comma-separated list of aliases
// (commas can themselves be part of a font family name).
func nativeTerminalFonts() ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var names []string
	for _, spacing := range []string{"100", "90"} {
		out, err := exec.CommandContext(ctx, "fc-list", ":spacing="+spacing, "--format=%{family[0]}\n").Output()
		if err != nil {
			return nil, fmt.Errorf("list local fontconfig fonts (spacing %s): %w", spacing, err)
		}
		names = append(names, strings.Split(string(out), "\n")...)
	}
	return names, nil
}
