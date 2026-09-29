//go:build darwin

package manage

import (
	"bytes"
	"errors"
	"os/exec"
	"strconv"
)

// darwinProcFS reads process arguments through ps, avoiding the Linux-only
// /proc filesystem. It implements the same ProcFS contract used by the live guard.
type darwinProcFS struct{}

// Cmdlines returns pid -> argv for processes visible to the current user.
// ps separates arguments by whitespace, matching the tokens the live guard compares.
func (darwinProcFS) Cmdlines() (map[int][]string, error) {
	cmd := exec.Command("ps", "-axo", "pid=,args=", "-ww")
	out, err := cmd.Output()
	if err != nil {
		return nil, errors.New("manage: cannot inspect process table")
	}
	result := make(map[int][]string)
	for _, line := range bytes.Split(out, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		pidBytes, args, ok := bytes.Cut(line, []byte(" "))
		if !ok {
			continue
		}
		pid, err := strconv.Atoi(string(pidBytes))
		if err != nil || pid <= 0 {
			continue
		}
		fields := bytes.Fields(args)
		if len(fields) == 0 {
			continue
		}
		argv := make([]string, len(fields))
		for i, field := range fields {
			argv[i] = string(field)
		}
		result[pid] = argv
	}
	return result, nil
}

// defaultProcFS returns the macOS process-table implementation.
func defaultProcFS() ProcFS { return darwinProcFS{} }
