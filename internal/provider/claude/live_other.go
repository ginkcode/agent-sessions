//go:build !darwin && !windows

package claude

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// processStartTime reads /proc/<pid>/stat and returns field 22 (starttime).
// The comm field may contain spaces and parentheses, so we split after the
// last ')'. Returns 0 when the process does not exist or the field is absent.
func processStartTime(procFS string, pid int) int64 {
	data, err := os.ReadFile(filepath.Join(procFS, strconv.Itoa(pid), "stat"))
	if err != nil {
		return 0
	}
	s := string(data)
	idx := strings.LastIndex(s, ")")
	if idx < 0 || idx+2 > len(s) {
		return 0
	}
	fields := strings.Fields(s[idx+2:]) // fields[0] is state (field 3)
	if len(fields) < 20 {
		return 0
	}
	starttime, err := strconv.ParseInt(fields[19], 10, 64) // field 22
	if err != nil {
		return 0
	}
	return starttime
}

// pidExists reports whether a process with the given pid exists. Linux uses
// procfs; macOS overrides this with a signal-zero check.
func (p *Provider) pidExists(pid int) bool {
	_, err := os.Stat(filepath.Join(p.procFS, strconv.Itoa(pid)))
	return err == nil
}
