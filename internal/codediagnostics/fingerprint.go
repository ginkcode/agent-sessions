package codediagnostics

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
)

const maxIdentityFile = 256 << 20

// ToolInput is fixed adapter-owned metadata, never arbitrary client commands.
// Policy includes the adapter version, fixed argv policy, environment allowlist
// values, toolchain selectors and dependency identity policy. ConfigPaths must
// enumerate relevant config/lock/plugin entry files; discovery executes no code.
type ToolInput struct {
	ProviderID  string
	Executable  string
	ConfigPaths []string
	Policy      string
}
type fileIdentity struct {
	Path     string
	Size     int64
	Mode     uint32
	Modified int64
	Hash     string
}
type toolIdentity struct {
	ProviderID, Policy string
	Executable         fileIdentity
	Configs            []fileIdentity
}

// Fingerprint reads only bounded regular-file identities; it does not run --version,
// load plugins or evaluate configuration. All inputs must be verifiable now.
func Fingerprint(root AuthorizedRoot, tools []ToolInput) (string, error) {
	if err := root.Revalidate(); err != nil {
		return "", err
	}
	if len(tools) == 0 || len(tools) > 32 {
		return "", failure("invalidArgument", "Expected 1 to 32 tools")
	}
	inputs := append([]ToolInput(nil), tools...)
	sort.Slice(inputs, func(i, j int) bool { return inputs[i].ProviderID < inputs[j].ProviderID })
	identities := make([]toolIdentity, 0, len(inputs))
	var total int64
	for i, t := range inputs {
		if !bounded(t.ProviderID, 256) || !bounded(t.Policy, 8192) || (i > 0 && inputs[i-1].ProviderID == t.ProviderID) || len(t.ConfigPaths) > 128 {
			return "", failure("invalidArgument", "Invalid adapter identity metadata")
		}
		executable, err := identifyFile(t.Executable, &total, nil, "")
		if err != nil {
			return "", err
		}
		configs := append([]string(nil), t.ConfigPaths...)
		sort.Strings(configs)
		identity := toolIdentity{ProviderID: t.ProviderID, Policy: t.Policy, Executable: executable, Configs: []fileIdentity{}}
		for j, path := range configs {
			if j > 0 && configs[j-1] == path {
				return "", failure("invalidArgument", "Duplicate configuration identity")
			}
			// Config discovery may not read through an escaping link.
			native, err := root.ResolvePath(root.project.HostID, path)
			if err != nil {
				return "", err
			}
			config, err := identifyFile(native, &total, &root, path)
			if err != nil {
				return "", err
			}
			identity.Configs = append(identity.Configs, config)
		}
		identities = append(identities, identity)
	}
	payload := struct {
		Version int
		Project Project
		Tools   []toolIdentity
	}{1, root.project, identities}
	data, _ := json.Marshal(payload)
	sum := sha256.Sum256(data)
	return "sha256-v1:" + hex.EncodeToString(sum[:]), nil
}

// inProcessPolicies are the reviewed, fixed policies of backend-owned analyzers
// that execute nothing. Only listed IDs can receive an analyzer-only identity.
var inProcessPolicies = map[string]string{
	GoSyntaxProviderID: "go-syntax-v1:go/parser:contained-os-root:saved-files:no-exec",
}

// InProcessFingerprint is the approval identity of a proposal that contains
// only in-process analyzers and no executable. It binds the host/project and
// the exact analyzer policies; it is a separate payload kind, so it can never
// equal an executable tool fingerprint. Nothing is read beyond root identity.
func InProcessFingerprint(root AuthorizedRoot, providers []string) (string, error) {
	if err := root.Revalidate(); err != nil {
		return "", err
	}
	if len(providers) == 0 || len(providers) > 32 {
		return "", failure("invalidArgument", "Expected 1 to 32 analyzers")
	}
	ids := slices.Clone(providers)
	slices.Sort(ids)
	type analyzer struct{ ProviderID, Policy string }
	analyzers := make([]analyzer, 0, len(ids))
	for i, id := range ids {
		policy, ok := inProcessPolicies[id]
		if !ok || (i > 0 && ids[i-1] == id) {
			return "", failure("invalidArgument", "Unknown or duplicated in-process analyzer")
		}
		analyzers = append(analyzers, analyzer{id, policy})
	}
	payload := struct {
		Version   int
		Kind      string
		Project   Project
		Analyzers []analyzer
	}{1, "in-process-analyzers-v1", root.project, analyzers}
	data, _ := json.Marshal(payload)
	sum := sha256.Sum256(data)
	return "sha256-v1:" + hex.EncodeToString(sum[:]), nil
}

func identifyFile(path string, total *int64, root *AuthorizedRoot, rel string) (fileIdentity, error) {
	if !bounded(path, 4096) || !filepath.IsAbs(path) || hasParent(path) {
		return fileIdentity{}, failure("invalidArgument", "Identity file must be absolute")
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return fileIdentity{}, failure("approvalChanged", "Identity file cannot be resolved")
	}
	info, err := os.Stat(real)
	if err != nil || !info.Mode().IsRegular() {
		return fileIdentity{}, failure("approvalChanged", "Identity file is not regular")
	}
	var f *os.File
	if root != nil {
		// Rooted open prevents a swapped config symlink from reading outside.
		r, openErr := os.OpenRoot(root.project.Root)
		if openErr != nil {
			return fileIdentity{}, failure("approvalChanged", "Configuration root cannot be opened")
		}
		f, err = r.Open(filepath.FromSlash(rel))
		_ = r.Close()
	} else {
		f, err = os.Open(real)
	}
	if err != nil {
		return fileIdentity{}, failure("approvalChanged", "Identity file cannot be read")
	}
	defer func() { _ = f.Close() }()
	before, err := f.Stat()
	if err != nil || !before.Mode().IsRegular() || before.Size() > maxIdentityFile || *total+before.Size() > 512<<20 {
		return fileIdentity{}, failure("limitExceeded", "Identity file is not regular or exceeds hashing limits")
	}
	hash := sha256.New()
	n, err := io.Copy(hash, io.LimitReader(f, maxIdentityFile+1))
	after, statErr := f.Stat()
	current, pathErr := os.Stat(path)
	if err != nil || statErr != nil || pathErr != nil || n != before.Size() || !os.SameFile(before, current) || before.Size() != after.Size() || before.ModTime() != after.ModTime() || before.Mode() != after.Mode() {
		return fileIdentity{}, failure("approvalChanged", "Identity file changed during discovery")
	}
	*total += n
	return fileIdentity{real, n, uint32(before.Mode()), before.ModTime().UnixNano(), hex.EncodeToString(hash.Sum(nil))}, nil
}

func validFingerprint(s string) bool {
	if len(s) != 74 || s[:10] != "sha256-v1:" {
		return false
	}
	_, err := hex.DecodeString(s[10:])
	return err == nil
}
