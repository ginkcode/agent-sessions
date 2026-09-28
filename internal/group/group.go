// Package group builds presentation trees from session metadata snapshots.
package group

import (
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/ginkcode/agent-sessions/internal/model"
)

// GroupMode selects the order of the directory and agent group levels.
type GroupMode string

const (
	DirAgent GroupMode = "dir-agent"
	AgentDir GroupMode = "agent-dir"
	Flat     GroupMode = "flat"
)

// Kind identifies the type of a tree node.
type Kind string

const (
	Directory Kind = "directory"
	Agent     Kind = "agent"
	Session   Kind = "session"
)

// Node is a group or session in the visible navigation tree.
type Node struct {
	Key          string             `json:"key"`
	Label        string             `json:"label"`
	Kind         Kind               `json:"kind"`
	Path         string             `json:"path,omitempty"`
	Agent        model.AgentID      `json:"agent,omitempty"`
	Count        int                `json:"count"`
	MessageTotal int                `json:"messageTotal"`
	Children     []Node             `json:"children,omitempty"`
	SessionRefs  []model.SessionRef `json:"sessionRefs,omitempty"`
	Missing      bool               `json:"missing,omitempty"`
}

// Options controls grouping and display without changing the metadata snapshot.
type Options struct {
	Mode     GroupMode
	Home     string
	FoldRepo bool
}

type treeNode struct {
	Node
	meta       *model.SessionMeta
	children   []*treeNode
	allMissing bool
}

// Build constructs a new navigation tree without changing sessions or reading
// the filesystem. An invalid mode uses DirAgent.
func Build(sessions []model.SessionMeta, opts Options) []Node {
	mode := opts.Mode
	if mode != Flat && mode != AgentDir {
		mode = DirAgent
	}

	// Keep one leaf per catalog key. Copies make the result independent of the
	// caller's slice even if the caller reuses it after Build.
	byKey := make(map[string]*treeNode, len(sessions))
	for _, session := range sessions {
		m := session
		byKey[m.Ref.Key()] = &treeNode{
			Node: Node{
				Key:         "session:" + m.Ref.Key(),
				Label:       m.Title,
				Kind:        Session,
				Agent:       m.Ref.Agent,
				Path:        m.CWD,
				Missing:     m.CWDMissing,
				SessionRefs: []model.SessionRef{m.Ref},
			},
			meta: &m,
		}
	}

	parents := make(map[string]string, len(byKey))
	for key, leaf := range byKey {
		m := leaf.meta
		if m.ParentID == "" {
			continue
		}
		parentKey := (model.SessionRef{Agent: m.Ref.Agent, ID: m.ParentID}).Key()
		if parentKey != key && byKey[parentKey] != nil {
			parents[key] = parentKey
		}
	}

	// Break every edge within a cycle, not just one arbitrary edge. Nodes
	// pointing into a cycle stay attached to their respective cycle root.
	state := make(map[string]uint8, len(byKey))
	for key := range byKey {
		if state[key] != 0 {
			continue
		}
		chain := make([]string, 0)
		positions := make(map[string]int)
		for at := key; at != "" && state[at] == 0; at = parents[at] {
			if start, seen := positions[at]; seen {
				for _, member := range chain[start:] {
					delete(parents, member)
				}
				break
			}
			positions[at] = len(chain)
			chain = append(chain, at)
		}
		for _, member := range chain {
			state[member] = 1
		}
	}

	var roots []*treeNode
	for key, leaf := range byKey {
		if parent := byKey[parents[key]]; parent != nil {
			parent.children = append(parent.children, leaf)
		} else {
			roots = append(roots, leaf)
		}
	}

	var sortLeaves func([]*treeNode)
	sortLeaves = func(leaves []*treeNode) {
		slices.SortFunc(leaves, func(a, b *treeNode) int {
			if c := b.meta.UpdatedAt.Compare(a.meta.UpdatedAt); c != 0 {
				return c
			}
			return strings.Compare(a.meta.Ref.Key(), b.meta.Ref.Key())
		})
		for _, leaf := range leaves {
			sortLeaves(leaf.children)
		}
	}
	sortLeaves(roots)

	if mode == Flat {
		return finish(roots, false)
	}

	first := make(map[string]*treeNode)
	second := make(map[string]map[string]*treeNode)
	for _, leaf := range roots {
		path := groupPath(*leaf.meta, opts.FoldRepo)
		agent := leaf.meta.Ref.Agent
		var firstKey, secondKey string
		if mode == AgentDir {
			firstKey = agentKey(agent)
			secondKey = agentDirKey(agent, path)
		} else {
			firstKey = directoryKey(path)
			secondKey = dirAgentKey(path, agent)
		}
		parent := first[firstKey]
		if parent == nil {
			if mode == AgentDir {
				parent = agentNode(firstKey, agent)
			} else {
				parent = directoryNode(firstKey, path, opts.Home)
			}
			parent.allMissing = true
			first[firstKey] = parent
			second[firstKey] = make(map[string]*treeNode)
		}
		child := second[firstKey][secondKey]
		if child == nil {
			if mode == AgentDir {
				child = directoryNode(secondKey, path, opts.Home)
			} else {
				child = agentNode(secondKey, agent)
			}
			child.allMissing = true
			second[firstKey][secondKey] = child
			parent.children = append(parent.children, child)
		}
		child.children = append(child.children, leaf)
		child.allMissing = child.allMissing && leaf.meta.CWDMissing
		parent.allMissing = parent.allMissing && leaf.meta.CWDMissing
	}

	groups := make([]*treeNode, 0, len(first))
	for _, group := range first {
		groups = append(groups, group)
		sortGroups(group.children)
	}
	sortGroups(groups)
	return finish(groups, false)
}

func groupPath(m model.SessionMeta, foldRepo bool) string {
	path := m.CWD
	if foldRepo && m.RepoRoot != "" {
		path = m.RepoRoot
	}
	if path == "" {
		return ""
	}
	return filepath.Clean(path)
}

func directoryKey(path string) string {
	if path == "" {
		return "dir:(unknown directory)"
	}
	return "dir:" + path
}

func agentKey(agent model.AgentID) string { return "agent:" + string(agent) }

func dirAgentKey(path string, agent model.AgentID) string {
	p := path
	if p == "" {
		p = "(unknown directory)"
	}
	return "dir-agent:" + strconv.Itoa(len(p)) + ":" + p + ":" + string(agent)
}

func agentDirKey(agent model.AgentID, path string) string {
	p := path
	if p == "" {
		p = "(unknown directory)"
	}
	return "agent-dir:" + strconv.Itoa(len(agent)) + ":" + string(agent) + ":" + p
}

func directoryNode(key, path, home string) *treeNode {
	label := "(unknown directory)"
	if path != "" {
		label = pathLabel(path, home)
	}
	return &treeNode{Node: Node{Key: key, Label: label, Kind: Directory, Path: path}}
}

func agentNode(key string, agent model.AgentID) *treeNode {
	label := string(agent)
	switch agent {
	case model.AgentClaude:
		label = "Claude Code"
	case model.AgentCodex:
		label = "Codex"
	case model.AgentOpenCode:
		label = "OpenCode"
	}
	return &treeNode{Node: Node{Key: key, Label: label, Kind: Agent, Agent: agent}}
}

func pathLabel(path, home string) string {
	if home == "" {
		return path
	}
	cleanHome := filepath.Clean(home)
	cleanPath := filepath.Clean(path)
	rel, err := filepath.Rel(cleanHome, cleanPath)
	if err != nil || filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return path
	}
	if rel == "." {
		return "~"
	}
	return "~" + string(filepath.Separator) + rel
}

func sortGroups(nodes []*treeNode) {
	slices.SortFunc(nodes, func(a, b *treeNode) int {
		if c := strings.Compare(strings.ToLower(a.Label), strings.ToLower(b.Label)); c != 0 {
			return c
		}
		return strings.Compare(a.Key, b.Key)
	})
}

func collectSessionRefs(node *Node, refs *[]model.SessionRef, seen map[string]bool) {
	if node.Kind == Session {
		if len(node.SessionRefs) > 0 {
			ref := node.SessionRefs[0]
			key := ref.Key()
			if !seen[key] {
				seen[key] = true
				*refs = append(*refs, ref)
			}
		}
	}
	for i := range node.Children {
		collectSessionRefs(&node.Children[i], refs, seen)
	}
}

func finish(nodes []*treeNode, nested bool) []Node {
	result := make([]Node, 0, len(nodes))
	for _, node := range nodes {
		out := node.Node
		if node.meta != nil {
			out.MessageTotal = node.meta.Counts.Total()
			if len(node.children) > 0 {
				out.Children = finish(node.children, true)
				for _, child := range out.Children {
					out.MessageTotal += child.MessageTotal
				}
			} else {
				out.Children = nil
			}
			if !nested {
				out.Count = 1
			}
		} else {
			if len(node.children) > 0 {
				out.Children = finish(node.children, false)
				for _, child := range out.Children {
					out.Count += child.Count
					out.MessageTotal += child.MessageTotal
				}
			} else {
				out.Children = nil
			}
			out.Missing = node.allMissing

			var refs []model.SessionRef
			seen := make(map[string]bool)
			collectSessionRefs(&out, &refs, seen)
			out.SessionRefs = refs
		}
		result = append(result, out)
	}
	return result
}
