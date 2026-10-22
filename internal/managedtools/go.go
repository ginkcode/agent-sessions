package managedtools

import (
	"bytes"
	"context"
	"debug/buildinfo"
	"encoding/json"
	"errors"
	goversion "go/version"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Commands have exact argv and an allowlisted environment; never a shell,
// inherited GOFLAGS/GOENV/GOPROXY, project cwd, package script or toolchain fetch.
func buildGo(ctx context.Context, stage string, tool TrustedGo) error {
	return buildGoWith(ctx, stage, tool, runGo)
}

func buildGoWith(ctx context.Context, stage string, tool TrustedGo, command func(context.Context, string, string, []string, ...string) ([]byte, error)) error {
	if err := noLinks(filepath.Dir(tool.Path)); err != nil {
		return errors.New("untrusted Go installation")
	}
	hash, err := fileDigest(tool.Path)
	if err != nil || hash != tool.SHA256 {
		return errors.New("untrusted Go")
	}
	work := filepath.Join(stage, "work")
	if err = os.Mkdir(work, 0o700); err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(work) }()
	for _, name := range []string{"home", "tmp", "mod", "cache", "path"} {
		if err = os.Mkdir(filepath.Join(work, name), 0o700); err != nil {
			return err
		}
	}
	env := []string{
		"GOENV=off", "GOTOOLCHAIN=local", "GOWORK=off", "GO111MODULE=on", "GOFLAGS=",
		"GOPROXY=" + source, "GOSUMDB=sum.golang.org", "GONOSUMDB=", "GOPRIVATE=", "GONOPROXY=", "GOINSECURE=",
		"GOVCS=*:off", "CGO_ENABLED=0", "GOTELEMETRY=off",
		"GOOS=" + runtime.GOOS, "GOARCH=" + runtime.GOARCH,
		"HOME=" + filepath.Join(work, "home"), "TMPDIR=" + filepath.Join(work, "tmp"), "TMP=" + filepath.Join(work, "tmp"), "TEMP=" + filepath.Join(work, "tmp"),
		"GOPATH=" + filepath.Join(work, "path"), "GOMODCACHE=" + filepath.Join(work, "mod"), "GOCACHE=" + filepath.Join(work, "cache"), "GOBIN=" + stage,
		"PATH=" + filepath.Dir(tool.Path),
	}
	run := func(args ...string) ([]byte, error) { return command(ctx, tool.Path, work, env, args...) }
	data, err := run("version")
	if err != nil {
		return err
	}
	fields := strings.Fields(string(data))
	if len(fields) != 4 || fields[0] != "go" || fields[1] != "version" || !goversion.IsValid(fields[2]) || goversion.Compare(fields[2], "go1.27.0") < 0 || fields[3] != runtime.GOOS+"/"+runtime.GOARCH {
		return ErrPrerequisite
	}
	// The trusted Go command authenticates module graph downloads using the Go
	// checksum database. Independently compare the catalog's main zip/go.mod sums.
	data, err = run("mod", "download", "-json", module+"@"+versionPin())
	if err != nil {
		return err
	}
	var downloaded struct{ Path, Version, Sum, GoModSum string }
	if json.Unmarshal(data, &downloaded) != nil || downloaded.Path != module || downloaded.Version != versionPin() || downloaded.Sum != moduleSum || downloaded.GoModSum != modSum {
		return errors.New("module integrity mismatch")
	}
	_, err = run("install", "-trimpath", module+"@"+versionPin())
	return err
}

// Avoid shadowing go/version in the build code.
func versionPin() string { return version }

func runGo(ctx context.Context, path, dir string, env []string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, path, args...)
	isolateCommand(cmd)
	cmd.Dir = dir
	cmd.Env = env
	cmd.WaitDelay = 2 * time.Second
	var out cappedOutput
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	if out.exceeded {
		return nil, errors.New("go output exceeded limit")
	}
	return out.buf.Bytes(), err
}

type cappedOutput struct {
	buf      bytes.Buffer
	exceeded bool
}

func (w *cappedOutput) Write(p []byte) (int, error) {
	n := len(p)
	remaining := (64 << 10) - w.buf.Len()
	if len(p) > remaining {
		p = p[:remaining]
		w.exceeded = true
	}
	_, _ = w.buf.Write(p)
	return n, nil
}

func copyBounded(dst io.Writer, src io.Reader, limit int64) (int64, error) {
	n, err := io.Copy(dst, io.LimitReader(src, limit+1))
	if n > limit {
		return n, errors.New("file exceeded limit")
	}
	return n, err
}

// Read build metadata, never execute the newly built server for validation.
func validateBinary(path string) error {
	if _, err := fileDigest(path); err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm()&0o111 == 0 {
		return errors.New("nonexecutable output")
	}
	b, err := buildinfo.ReadFile(path)
	if err != nil {
		return err
	}
	if b.Path != module || b.Main.Path != module || b.Main.Version != versionPin() || b.Main.Sum != moduleSum || b.Main.Replace != nil {
		return errors.New("unexpected Go binary metadata")
	}
	settings := map[string]string{}
	for _, s := range b.Settings {
		settings[s.Key] = s.Value
	}
	if settings["GOOS"] != runtime.GOOS || settings["GOARCH"] != runtime.GOARCH || settings["CGO_ENABLED"] != "0" {
		return errors.New("unexpected platform")
	}
	return nil
}
