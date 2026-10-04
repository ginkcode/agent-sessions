//go:build windows

package remote

import (
	"errors"
	"sort"
	"strings"

	"golang.org/x/sys/windows/registry"
)

// lxssKey is where WSL registers each distribution for the current user.
const lxssKey = `Software\Microsoft\Windows\CurrentVersion\Lxss`

// ListWSLDistros returns the current user's installed WSL distributions,
// read from the registry so that listing starts no process and no VM.
// Docker Desktop's internal distributions are left out.
func ListWSLDistros() ([]WSLDistro, error) {
	if _, err := wslBinary(); err != nil {
		return nil, err
	}
	k, err := registry.OpenKey(registry.CURRENT_USER, lxssKey, registry.READ)
	if errors.Is(err, registry.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer k.Close()
	def, _, _ := k.GetStringValue("DefaultDistribution")
	ids, err := k.ReadSubKeyNames(-1)
	if err != nil {
		return nil, err
	}
	var out []WSLDistro
	for _, id := range ids {
		d, ok := readDistro(k, id)
		if !ok {
			continue
		}
		d.Default = strings.EqualFold(id, def)
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out, nil
}

// readDistro reads one registered distribution. One still installing or
// being removed (State is not 1) is skipped.
func readDistro(lxss registry.Key, id string) (WSLDistro, bool) {
	k, err := registry.OpenKey(lxss, id, registry.QUERY_VALUE)
	if err != nil {
		return WSLDistro{}, false
	}
	defer k.Close()
	name, _, err := k.GetStringValue("DistributionName")
	if err != nil || ValidateDistro(name) != nil || strings.HasPrefix(strings.ToLower(name), "docker-desktop") {
		return WSLDistro{}, false
	}
	if state, _, err := k.GetIntegerValue("State"); err == nil && state != 1 {
		return WSLDistro{}, false
	}
	return WSLDistro{Name: name}, true
}
