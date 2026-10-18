package files

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// Download describes an artifact, never its server-side filesystem path.
type Download struct {
	Name      string `json:"name"`
	Token     string `json:"token"`
	Directory bool   `json:"directory"`
}

// WriteDownload streams original file bytes or a ZIP containing the selected
// directory. Rooted filesystem operations prevent symlink-swap escapes while
// reading. Archives reject symlinks and special files rather than following them.
func WriteDownload(ctx context.Context, root, rel string, dst io.Writer) (Download, error) {
	full, err := Resolve(root, rel)
	if err != nil {
		return Download{}, err
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return Download{}, err
	}
	name, err := filepath.Rel(realRoot, full)
	if err != nil {
		return Download{}, err
	}
	r, err := os.OpenRoot(realRoot)
	if err != nil {
		return Download{}, err
	}
	defer r.Close()
	info, err := r.Stat(name)
	if err != nil {
		return Download{}, err
	}
	out := Download{Name: filepath.Base(full), Directory: info.IsDir()}
	if !info.IsDir() {
		if !info.Mode().IsRegular() {
			return Download{}, fmt.Errorf("not a regular file: %s", rel)
		}
		f, err := r.Open(name)
		if err != nil {
			return Download{}, err
		}
		defer f.Close()
		opened, err := f.Stat()
		if err != nil {
			return Download{}, err
		}
		if !opened.Mode().IsRegular() {
			return Download{}, fmt.Errorf("not a regular file: %s", rel)
		}
		_, err = io.Copy(dst, contextReader{ctx, f})
		return out, err
	}
	out.Name += ".zip"
	zw := zip.NewWriter(dst)
	err = fs.WalkDir(r.FS(), filepath.ToSlash(name), func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("archive refuses symlink: %s", p)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			return fmt.Errorf("archive refuses special file: %s", p)
		}
		h, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		suffix, err := filepath.Rel(name, filepath.FromSlash(p))
		if err != nil {
			return err
		}
		h.Name = filepath.Base(full)
		if suffix != "." {
			h.Name += "/" + filepath.ToSlash(suffix)
		}
		if info.IsDir() {
			h.Name += "/"
		} else {
			h.Method = zip.Deflate
		}
		w, err := zw.CreateHeader(h)
		if err != nil || info.IsDir() {
			return err
		}
		f, err := r.Open(filepath.FromSlash(p))
		if err != nil {
			return err
		}
		defer f.Close()
		stat, err := f.Stat()
		if err != nil {
			return err
		}
		if !stat.Mode().IsRegular() {
			return fmt.Errorf("not a regular file: %s", p)
		}
		_, err = io.Copy(w, contextReader{ctx, f})
		return err
	})
	if err != nil {
		_ = zw.Close()
		return Download{}, err
	}
	return out, zw.Close()
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}
