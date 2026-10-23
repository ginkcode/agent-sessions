package files

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type GitDiffBasis string

const (
	GitDiffHead   GitDiffBasis = "head"
	GitDiffIndex  GitDiffBasis = "index"
	GitDiffStaged GitDiffBasis = "staged"
)

type GitDiffContent struct {
	Content  Content      `json:"content"`
	Basis    GitDiffBasis `json:"basis"`
	Deleted  bool         `json:"deleted"`
	ReadOnly bool         `json:"readOnly"`
}

// ReadGitDiff keeps index snapshots separate from editable working content.
// Deleted entries still return removal hunks on empty read-only content.
func ReadGitDiff(ctx context.Context, policy EditPolicy, root, rel string, basis GitDiffBasis) (GitDiffContent, error) {
	result := GitDiffContent{Basis: basis, ReadOnly: basis == GitDiffStaged}
	if basis != GitDiffHead && basis != GitDiffIndex && basis != GitDiffStaged {
		return result, errors.New("invalid Git diff basis")
	}
	err := withGitWrite(ctx, root, func(w gitWriter) error {
		paths, err := gitPaths(w.root, []string{rel})
		if err != nil {
			return err
		}
		rel = paths[0]
		if rel == "." {
			return ErrEditTarget
		}
		target := filepath.Join(w.root, filepath.FromSlash(rel))
		real, err := editResolveAncestor(target)
		if err != nil {
			return err
		}
		if err := policy.refuse(false, target, real); err != nil {
			return err
		}
		// Refuse links (including in existing parents) and nonregular targets before
		// reading a working file or historical blob associated with that path.
		if err := gitDiscardPreflight(policy, w.root, rel); err != nil {
			return err
		}
		prefix, err := w.run("", "rev-parse", "--show-prefix")
		if err != nil {
			return err
		}
		repoPath := strings.TrimSuffix(string(prefix), "\n") + rel
		index, err := w.run("", "ls-files", "--stage", "-z", "--", rel)
		if err != nil {
			return err
		}
		indexed := false
		for _, record := range strings.Split(string(index), "\x00") {
			if record == "" {
				continue
			}
			meta, path, ok := strings.Cut(record, "\t")
			fields := strings.Fields(meta)
			if !ok || len(fields) != 3 || path != rel {
				return errors.New("invalid Git index entry")
			}
			if fields[2] != "0" {
				if basis == GitDiffStaged {
					return errors.New("cannot read staged content for an unmerged file")
				}
				continue
			}
			if fields[0] != "100644" && fields[0] != "100755" {
				return ErrEditTarget
			}
			indexed = true
		}
		status, err := GitRepoStatus(w.ctx, w.root)
		if err != nil {
			return err
		}
		if !status.OK {
			return errors.New(status.Reason)
		}
		if basis == GitDiffStaged {
			if indexed {
				content, err := readGitBlob(w, ":"+repoPath, rel)
				if err != nil {
					return err
				}
				result.Content = content
			} else {
				result.Deleted = true
				result.Content = Content{Path: rel, Edit: &EditInfo{Reason: EditReadOnly}}
			}
		} else {
			content, err := readGitWorking(w.root, rel)
			if errors.Is(err, os.ErrNotExist) {
				result.Deleted = true
				result.ReadOnly = true
				result.Content = Content{Path: rel, Edit: &EditInfo{Reason: EditReadOnly}}
			} else if err != nil {
				return err
			} else {
				CheckEdit(policy, w.root, rel, content.Edit)
				result.Content = content
			}
		}
		// Bound and inspect the old side too: a tiny replacement/deletion must not
		// produce an unbounded patch containing a huge or binary historical blob.
		baseSpec := ""
		if basis == GitDiffIndex && indexed {
			baseSpec = ":" + repoPath
		}
		if basis != GitDiffIndex && status.HasCommits {
			baseSpec = "HEAD:" + repoPath
		}
		baseExists := false
		baseText := ""
		if baseSpec != "" {
			if _, err := w.run("", "cat-file", "-e", baseSpec); err == nil {
				baseExists = true
				old, err := readGitBlob(w, baseSpec, rel)
				if err != nil {
					return err
				}
				baseText = old.Text
				result.Content.Binary = result.Content.Binary || old.Binary
				result.Content.TooLarge = result.Content.TooLarge || old.TooLarge
			}
		}
		if result.Content.Binary || result.Content.TooLarge {
			return nil
		}
		args := []string{"diff", "--no-color", "--no-ext-diff", "--no-textconv", "--no-renames", "--ignore-submodules=all", "-U0"}
		switch basis {
		case GitDiffHead:
			if status.HasCommits {
				args = append(args, "HEAD")
			}
		case GitDiffStaged:
			args = append(args, "--cached")
			if status.HasCommits {
				args = append(args, "HEAD")
			}
		}
		diff, err := w.run("", append(args, "--", rel)...)
		if err != nil {
			return err
		}
		result.Content.Changes = parseHunks(string(diff))
		// Git ignores a recreated working path once its deletion is staged.
		// Preserve the HEAD-to-working basis instead of showing the index deletion.
		if basis == GitDiffHead && !indexed && baseExists && !result.Deleted {
			if baseText == result.Content.Text {
				result.Content.Changes = nil
			} else {
				removed := strings.Split(strings.TrimSuffix(baseText, "\n"), "\n")
				if baseText == "" {
					removed = nil
				}
				count := strings.Count(result.Content.Text, "\n")
				if result.Content.Text != "" && !strings.HasSuffix(result.Content.Text, "\n") {
					count++
				}
				kind, start := "modified", 1
				if baseText == "" {
					kind = "added"
				}
				if count == 0 {
					kind, start = "deleted", 0
				}
				result.Content.Changes = []LineChange{{Kind: kind, Start: start, Count: count, Removed: removed}}
			}
		}
		// Git does not diff untracked working files. Before the first commit HEAD
		// also has no base; both cases are a whole-file addition (except deletions).
		addedWorking := basis != GitDiffStaged && !result.Deleted && ((basis == GitDiffIndex && !indexed) || (basis == GitDiffHead && !baseExists))
		if addedWorking && result.Content.Text != "" {
			lines := strings.Count(result.Content.Text, "\n")
			if !strings.HasSuffix(result.Content.Text, "\n") {
				lines++
			}
			result.Content.Changes = []LineChange{{Kind: "added", Start: 1, Count: lines}}
		}
		// Missing entries should not silently become a fake deleted file.
		if result.Deleted && len(result.Content.Changes) == 0 && !indexed && !baseExists {
			return os.ErrNotExist
		}
		return nil
	})
	return result, err
}

func readGitBlob(w gitWriter, spec, rel string) (Content, error) {
	out := Content{Path: rel, Edit: &EditInfo{Reason: EditReadOnly}}
	size, err := w.run("", "cat-file", "-s", spec)
	if err != nil {
		return out, err
	}
	out.Size, err = strconv.ParseInt(strings.TrimSpace(string(size)), 10, 64)
	if err != nil {
		return out, err
	}
	if out.Size < 0 {
		return out, fmt.Errorf("invalid Git blob size")
	}
	if out.Size > MaxReadBytes {
		out.TooLarge = true
		return out, nil
	}
	data, err := w.run("", "cat-file", "blob", spec)
	if err != nil {
		return out, err
	}
	if int64(len(data)) != out.Size {
		return out, errors.New("Git blob changed while reading")
	}
	if IsBinary(data) {
		out.Binary = true
		return out, nil
	}
	out.Text = string(data)
	// Deliberately no revision: index snapshots can never be used for SaveFile.
	return out, nil
}

func readGitWorking(root, rel string) (Content, error) {
	full, err := Resolve(root, rel)
	if err != nil {
		return Content{}, err
	}
	f, err := os.Open(full)
	if err != nil {
		return Content{}, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return Content{}, err
	}
	if !info.Mode().IsRegular() {
		return Content{}, ErrEditTarget
	}
	out := Content{Path: rel, Size: info.Size()}
	data, err := readText(f, &out)
	if err != nil {
		return Content{}, err
	}
	if data != nil {
		out.Edit = textEditInfo(data)
	}
	return out, nil
}
