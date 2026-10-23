package managedtools

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

const (
	maxDownload  = 256 << 20
	maxExtracted = 1 << 30
	maxEntries   = 50000
)

var httpClient = &http.Client{
	Timeout: 10 * time.Minute,
	// Release hosts redirect to their storage; never to plain HTTP.
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if req.URL.Scheme != "https" || len(via) >= 10 {
			return errors.New("unsafe redirect")
		}
		return nil
	},
}

// fetchHTTPS writes the body of a pinned HTTPS URL to w, bounded.
func fetchHTTPS(ctx context.Context, url string, w io.Writer) error {
	if !strings.HasPrefix(url, "https://") {
		return errors.New("unsafe source")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return errors.New("download failed")
	}
	_, err = copyBounded(w, resp.Body, maxDownload)
	return err
}

// installArchive downloads a into work, compares its SHA-256 with the pin
// BEFORE reading it as an archive, and extracts it into dest without its top
// directory.
func (i *Installer) installArchive(ctx context.Context, a artifact, work, dest string) error {
	file := filepath.Join(work, "archive.tar.gz")
	f, err := os.OpenFile(file, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	h := sha256.New()
	err = i.fetch(ctx, a.URL, io.MultiWriter(f, h))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if hex.EncodeToString(h.Sum(nil)) != a.SHA256 {
		return errors.New("archive integrity mismatch")
	}
	return extractTarGz(ctx, file, dest, 1)
}

// extractTarGz extracts regular files, directories and links. It refuses
// absolute names, "..", links that leave dest, writes through a link, special
// files, and archives over the size or entry limits. Modes keep only the
// permission bits, without setuid/setgid or group/other write.
func extractTarGz(ctx context.Context, file, dest string, strip int) error {
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	var total int64
	for n := 0; ; n++ {
		if n > maxEntries {
			return errors.New("archive has too many entries")
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		rel, ok := archiveName(hdr.Name, strip)
		if !ok {
			return errors.New("unsafe archive entry")
		}
		if rel == "" {
			continue
		}
		target := filepath.Join(dest, rel)
		if err := realParent(dest, rel); err != nil {
			return err
		}
		mode := os.FileMode(hdr.Mode).Perm() &^ 0o022
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if total += hdr.Size; hdr.Size < 0 || total > maxExtracted {
				return errors.New("archive too large")
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode|0o600)
			if err != nil {
				return err
			}
			_, err = copyBounded(out, tr, hdr.Size)
			if cerr := out.Close(); err == nil {
				err = cerr
			}
			if err != nil {
				return err
			}
		case tar.TypeSymlink:
			link := hdr.Linkname
			if link == "" || path.IsAbs(link) || !filepath.IsLocal(filepath.Join(filepath.Dir(rel), filepath.FromSlash(link))) {
				return errors.New("unsafe archive link")
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			if err := os.Symlink(filepath.FromSlash(link), target); err != nil {
				return err
			}
		case tar.TypeLink:
			old, ok := archiveName(hdr.Linkname, strip)
			if !ok || old == "" {
				return errors.New("unsafe archive link")
			}
			if err := realParent(dest, old); err != nil {
				return err
			}
			if st, err := os.Lstat(filepath.Join(dest, old)); err != nil || !st.Mode().IsRegular() {
				return errors.New("unsafe archive link")
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			if err := os.Link(filepath.Join(dest, old), target); err != nil {
				return err
			}
		case tar.TypeXGlobalHeader:
		default:
			return errors.New("unsupported archive entry")
		}
	}
}

// archiveName strips the leading components of a slash-separated tar name and
// returns a local relative path ("" for the stripped directories themselves).
func archiveName(name string, strip int) (string, bool) {
	if name == "" || path.IsAbs(name) || strings.Contains(name, "\\") {
		return "", false
	}
	clean := path.Clean(name)
	if clean == "." {
		return "", true
	}
	parts := strings.Split(clean, "/")
	for _, p := range parts {
		if p == ".." {
			return "", false
		}
	}
	if len(parts) <= strip {
		return "", true
	}
	rel := filepath.FromSlash(strings.Join(parts[strip:], "/"))
	return rel, filepath.IsLocal(rel)
}

// realParent refuses an entry whose existing parent components include a
// link, so an earlier link entry can never redirect a later write.
func realParent(dest, rel string) error {
	dir := dest
	for _, p := range strings.Split(filepath.Dir(rel), string(filepath.Separator)) {
		if p == "." || p == "" {
			continue
		}
		dir = filepath.Join(dir, p)
		st, err := os.Lstat(dir)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if !st.IsDir() {
			return errors.New("unsafe archive path")
		}
	}
	return nil
}
