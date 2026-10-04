package app

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"

	"github.com/ginkcode/agent-sessions/internal/configfile"
	"github.com/ginkcode/agent-sessions/internal/launch"
	"github.com/ginkcode/agent-sessions/internal/paths"
	"github.com/ginkcode/agent-sessions/internal/provider"
)

// TerminalOption is a terminal app Settings offers.
type TerminalOption struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// TerminalSettings is the terminal this computer opens sessions in. It is
// local: a remote host being selected does not change it.
type TerminalSettings struct {
	// Choose is false where the terminal is fixed: Windows PowerShell.
	Choose bool `json:"choose"`
	// Selected is the chosen terminal id; empty means Automatic.
	Selected     string `json:"selected"`
	SelectedName string `json:"selectedName"`
	// Missing is true when the chosen terminal is no longer installed.
	Missing bool `json:"missing"`
	// Auto is what Automatic opens; nil when no terminal is installed.
	Auto *TerminalOption `json:"auto"`
	// Options are the installed terminals.
	Options []TerminalOption `json:"options"`
	// Hint is a note for this platform, such as macOS asking for permission.
	Hint string `json:"hint"`
}

const macTerminalHint = "The first time, macOS asks whether Agent Sessions may control the terminal app. Allow it, or the window cannot open."

// TerminalSettings lists the installed terminals and the one chosen.
func (a *App) TerminalSettings() (TerminalSettings, error) {
	if !launch.ChooseTerminal() {
		return TerminalSettings{Options: []TerminalOption{}}, nil
	}
	id, err := a.savedTerminal()
	if err != nil {
		return TerminalSettings{}, err
	}
	env := a.terminalEnvironment()
	return terminalSettings(env, env.Detect(), id), nil
}

// SetTerminal chooses the terminal sessions open in: an installed terminal's
// id, or "" for Automatic.
func (a *App) SetTerminal(id string) (TerminalSettings, error) {
	if !launch.ChooseTerminal() {
		return TerminalSettings{}, launch.ErrUnsupported
	}
	env := a.terminalEnvironment()
	apps := env.Detect()
	if id != "" && !slices.ContainsFunc(apps, func(t launch.TerminalApp) bool { return t.ID == id }) {
		return TerminalSettings{}, fmt.Errorf("terminal %q is not installed on this computer", id)
	}
	path, err := a.localConfigPath()
	if err != nil {
		return TerminalSettings{}, err
	}
	err = configfile.Update(path, func(data []byte) []byte {
		return configfile.SetValue(data, "terminal", "app", strconv.Quote(id))
	})
	if err != nil {
		return TerminalSettings{}, fmt.Errorf("save terminal: %w", err)
	}
	return terminalSettings(env, apps, id), nil
}

func terminalSettings(env launch.TerminalEnv, apps []launch.TerminalApp, id string) TerminalSettings {
	s := TerminalSettings{Choose: true, Selected: id, Options: make([]TerminalOption, 0, len(apps))}
	for _, t := range apps {
		s.Options = append(s.Options, TerminalOption{ID: t.ID, Name: t.Name})
	}
	if t, ok := env.Auto(apps); ok {
		s.Auto = &TerminalOption{ID: t.ID, Name: t.Name}
	}
	if id != "" {
		s.SelectedName = env.Name(id)
		s.Missing = !slices.ContainsFunc(apps, func(t launch.TerminalApp) bool { return t.ID == id })
	}
	if env.GOOS == "darwin" {
		s.Hint = macTerminalHint
	}
	return s
}

// terminalAvailable reports whether Open in terminal can work. A saved
// choice, even a missing one, or an unreadable config counts: the click then
// explains what is wrong.
func (a *App) terminalAvailable() bool {
	if a.terminalOverride != nil {
		return true
	}
	if !launch.ChooseTerminal() {
		return launch.Supported()
	}
	if id, err := a.savedTerminal(); err != nil || id != "" {
		return true
	}
	env := a.terminalEnvironment()
	_, ok := env.Auto(env.Detect())
	return ok
}

// terminalOpener resolves what opens a command on this computer. It runs
// before the command is built, so a missing terminal writes no handoff files.
func (a *App) terminalOpener() (func(provider.Command) error, error) {
	if a.terminalOverride != nil {
		return a.terminalOverride, nil
	}
	if !launch.ChooseTerminal() {
		return launch.OpenTerminal, nil
	}
	id, err := a.savedTerminal()
	if err != nil {
		return nil, err
	}
	env := a.terminalEnvironment()
	t, err := env.Resolve(env.Detect(), id)
	if err != nil {
		return nil, err
	}
	openIn := launch.OpenIn
	if a.openInOverride != nil {
		openIn = a.openInOverride
	}
	return func(cmd provider.Command) error { return openIn(t, cmd) }, nil
}

func (a *App) terminalEnvironment() launch.TerminalEnv {
	if a.terminalEnv != nil {
		return *a.terminalEnv
	}
	return launch.SystemTerminalEnv()
}

// savedTerminal reads the chosen terminal id from the local config file,
// which the engine's manage settings share.
func (a *App) savedTerminal() (string, error) {
	path, err := a.localConfigPath()
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read settings: %w", err)
	}
	id, _ := configfile.Value(data, "terminal", "app")
	return id, nil
}

func (a *App) localConfigPath() (string, error) {
	dir := a.roots.Config
	if dir == "" {
		r, err := paths.Default()
		if err != nil {
			return "", fmt.Errorf("resolve config dir: %w", err)
		}
		dir = r.Config
	}
	return filepath.Join(dir, "config.toml"), nil
}
