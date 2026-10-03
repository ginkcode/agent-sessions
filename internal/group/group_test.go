package group

import (
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/ginkcode/agent-sessions/internal/model"
)

func ref(agent model.AgentID, id string) model.SessionRef {
	return model.SessionRef{Agent: agent, ID: id}
}

func meta(agent model.AgentID, id, parent, cwd, repo, title string, updated int, user, assistant int, missing bool) model.SessionMeta {
	return model.SessionMeta{
		Ref: ref(agent, id), ParentID: parent, CWD: cwd, RepoRoot: repo,
		Title: title, UpdatedAt: time.Unix(int64(updated), 0).UTC(),
		Counts: model.MessageCounts{User: user, Assistant: assistant}, CWDMissing: missing,
	}
}

func session(m model.SessionMeta, count, total int, children ...Node) Node {
	var ch []Node
	if len(children) > 0 {
		ch = children
	}
	return Node{
		Key: "session:" + m.Ref.Key(), Label: m.Title, Kind: Session,
		Path: m.CWD, Agent: m.Ref.Agent, Count: count, MessageTotal: total,
		SessionRefs: []model.SessionRef{m.Ref}, Children: ch, Missing: m.CWDMissing,
	}
}

func group(key, label string, kind Kind, path string, agent model.AgentID, count, total int, missing bool, children ...Node) Node {
	var refs []model.SessionRef
	seen := make(map[string]bool)
	var collect func(nodes []Node)
	collect = func(nodes []Node) {
		for _, n := range nodes {
			if n.Kind == Session {
				if len(n.SessionRefs) > 0 {
					r := n.SessionRefs[0]
					if !seen[r.Key()] {
						seen[r.Key()] = true
						refs = append(refs, r)
					}
				}
			}
			collect(n.Children)
		}
	}
	collect(children)

	var ch []Node
	if len(children) > 0 {
		ch = children
	}
	return Node{
		Key: key, Label: label, Kind: kind, Path: path, Agent: agent,
		Count: count, MessageTotal: total, Missing: missing,
		Children: ch, SessionRefs: refs,
	}
}

func TestBuildModes(t *testing.T) {
	const home = "/home/al"
	parent := meta(model.AgentClaude, "p", "", "/home/al/work", "", "Parent", 4, 2, 3, false)
	child := meta(model.AgentClaude, "c", "p", "/tmp/child", "", "Child", 3, 1, 1, true)
	grandchild := meta(model.AgentClaude, "g", "c", "/var/g", "", "Grandchild", 1, 0, 0, false)
	secondChild := meta(model.AgentClaude, "d", "p", "/elsewhere", "", "Second child", 2, 1, 2, false)
	codex := meta(model.AgentCodex, "x", "", "/home/al/work", "", "Codex", 5, 2, 2, false)
	away := meta(model.AgentClaude, "a", "", "/home/al/away", "", "Empty", 6, 0, 0, true)
	input := []model.SessionMeta{child, codex, grandchild, away, parent, secondChild}
	before := slices.Clone(input)

	leafParent := session(parent, 1, 10,
		session(child, 0, 2, session(grandchild, 0, 0)),
		session(secondChild, 0, 3),
	)
	leafCodex := session(codex, 1, 4)
	leafAway := session(away, 1, 0)
	workDir := directoryKey("/home/al/work")
	awayDir := directoryKey("/home/al/away")

	tests := []struct {
		name string
		mode GroupMode
		want []Node
	}{
		{
			name: "directory then agent", mode: DirAgent,
			want: []Node{
				group(awayDir, "~/away", Directory, "/home/al/away", "", 1, 0, true,
					group(dirAgentKey("/home/al/away", model.AgentClaude), "Claude Code", Agent, "", model.AgentClaude, 1, 0, true, leafAway)),
				group(workDir, "~/work", Directory, "/home/al/work", "", 2, 14, false,
					group(dirAgentKey("/home/al/work", model.AgentClaude), "Claude Code", Agent, "", model.AgentClaude, 1, 10, false, leafParent),
					group(dirAgentKey("/home/al/work", model.AgentCodex), "Codex", Agent, "", model.AgentCodex, 1, 4, false, leafCodex)),
			},
		},
		{
			name: "agent then directory", mode: AgentDir,
			want: []Node{
				group(agentKey(model.AgentClaude), "Claude Code", Agent, "", model.AgentClaude, 2, 10, false,
					group(agentDirKey(model.AgentClaude, "/home/al/away"), "~/away", Directory, "/home/al/away", "", 1, 0, true, leafAway),
					group(agentDirKey(model.AgentClaude, "/home/al/work"), "~/work", Directory, "/home/al/work", "", 1, 10, false, leafParent)),
				group(agentKey(model.AgentCodex), "Codex", Agent, "", model.AgentCodex, 1, 4, false,
					group(agentDirKey(model.AgentCodex, "/home/al/work"), "~/work", Directory, "/home/al/work", "", 1, 4, false, leafCodex)),
			},
		},
		{
			name: "flat", mode: Flat,
			want: []Node{leafAway, leafCodex, leafParent},
		},
		{
			name: "invalid mode defaults to directory then agent", mode: "unexpected",
			want: []Node{
				group(awayDir, "~/away", Directory, "/home/al/away", "", 1, 0, true,
					group(dirAgentKey("/home/al/away", model.AgentClaude), "Claude Code", Agent, "", model.AgentClaude, 1, 0, true, leafAway)),
				group(workDir, "~/work", Directory, "/home/al/work", "", 2, 14, false,
					group(dirAgentKey("/home/al/work", model.AgentClaude), "Claude Code", Agent, "", model.AgentClaude, 1, 10, false, leafParent),
					group(dirAgentKey("/home/al/work", model.AgentCodex), "Codex", Agent, "", model.AgentCodex, 1, 4, false, leafCodex)),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Build(input, Options{Mode: tt.mode, Home: home})
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Build() =\n%+v\nwant\n%+v", got, tt.want)
			}
			if !reflect.DeepEqual(input, before) {
				t.Fatal("Build mutated input")
			}
		})
	}
}

func TestBuildOrphansAndCycles(t *testing.T) {
	orphan := meta(model.AgentClaude, "orphan", "absent", "/a", "", "Orphan", 4, 1, 1, false)
	crossAgent := meta(model.AgentCodex, "cross", "orphan", "/b", "", "Cross-agent", 3, 1, 0, false)
	self := meta(model.AgentClaude, "self", "self", "/a", "", "Self", 2, 0, 1, false)
	cycleA := meta(model.AgentClaude, "cycle-a", "cycle-b", "/a", "", "A", 6, 1, 0, false)
	cycleB := meta(model.AgentClaude, "cycle-b", "cycle-a", "/b", "", "B", 5, 0, 1, false)
	attached := meta(model.AgentClaude, "attached", "cycle-a", "/c", "", "Attached", 1, 1, 2, false)
	input := []model.SessionMeta{cycleB, crossAgent, attached, self, orphan, cycleA}
	want := []Node{
		session(cycleA, 1, 4, session(attached, 0, 3)),
		session(cycleB, 1, 1),
		session(orphan, 1, 2),
		session(crossAgent, 1, 1),
		session(self, 1, 1),
	}
	got := Build(input, Options{Mode: Flat})
	if !reflect.DeepEqual(got, want) {
		t.Errorf("orphan/cycle tree =\n%+v\nwant\n%+v", got, want)
	}
}

func TestBuildPathsAndMissing(t *testing.T) {
	const home = "/home/user"
	tests := []struct {
		name  string
		input []model.SessionMeta
		opts  Options
		want  []Node
	}{
		{
			name: "home and home-prefix boundary",
			input: []model.SessionMeta{
				meta(model.AgentClaude, "home", "", home, "", "Home", 1, 0, 0, false),
				meta(model.AgentClaude, "prefix", "", "/home/user2/work", "", "Prefix", 2, 1, 0, false),
			},
			opts: Options{Home: home},
			want: []Node{
				group(directoryKey("/home/user2/work"), "/home/user2/work", Directory, "/home/user2/work", "", 1, 1, false,
					group(dirAgentKey("/home/user2/work", model.AgentClaude), "Claude Code", Agent, "", model.AgentClaude, 1, 1, false,
						session(meta(model.AgentClaude, "prefix", "", "/home/user2/work", "", "Prefix", 2, 1, 0, false), 1, 1))),
				group(directoryKey(home), "~", Directory, home, "", 1, 0, false,
					group(dirAgentKey(home, model.AgentClaude), "Claude Code", Agent, "", model.AgentClaude, 1, 0, false,
						session(meta(model.AgentClaude, "home", "", home, "", "Home", 1, 0, 0, false), 1, 0))),
			},
		},
		{
			name: "unknown directory groups all empty paths",
			input: []model.SessionMeta{
				meta(model.AgentID("new-agent"), "u", "", "", "", "Unknown", 2, 1, 1, true),
				meta(model.AgentID("new-agent"), "v", "", "", "", "Other", 1, 0, 1, false),
			},
			opts: Options{Home: home},
			want: []Node{
				group(directoryKey(""), "(unknown directory)", Directory, "", "", 2, 3, false,
					group(dirAgentKey("", "new-agent"), "new-agent", Agent, "", "new-agent", 2, 3, false,
						session(meta("new-agent", "u", "", "", "", "Unknown", 2, 1, 1, true), 1, 2),
						session(meta("new-agent", "v", "", "", "", "Other", 1, 0, 1, false), 1, 1))),
			},
		},
		{
			name: "fold repo and do not change leaf missing",
			input: []model.SessionMeta{
				meta(model.AgentOpenCode, "r", "", "/tmp/missing", "/home/user/repo/../repo", "Repo", 1, 1, 2, true),
			},
			opts: Options{Home: home, FoldRepo: true},
			want: []Node{
				group(directoryKey("/home/user/repo"), "~/repo", Directory, "/home/user/repo", "", 1, 3, true,
					group(dirAgentKey("/home/user/repo", model.AgentOpenCode), "OpenCode", Agent, "", model.AgentOpenCode, 1, 3, true,
						session(meta(model.AgentOpenCode, "r", "", "/tmp/missing", "/home/user/repo/../repo", "Repo", 1, 1, 2, true), 1, 3))),
			},
		},
		{
			name: "Windows paths merge prefix and drive case under a Windows home",
			input: []model.SessionMeta{
				meta(model.AgentCodex, "w1", "", `\\?\c:\Users\Al\proj`, "", "Codex", 2, 1, 0, false),
				meta(model.AgentClaude, "w2", "", `C:\users\al\proj\`, "", "Claude", 1, 0, 1, false),
				meta(model.AgentClaude, "posix", "", "/Users/Al/proj", "", "POSIX", 3, 1, 0, false),
			},
			opts: Options{Home: `C:\Users\Al`},
			want: []Node{
				group(directoryKey("/Users/Al/proj"), "/Users/Al/proj", Directory, "/Users/Al/proj", "", 1, 1, false,
					group(dirAgentKey("/Users/Al/proj", model.AgentClaude), "Claude Code", Agent, "", model.AgentClaude, 1, 1, false,
						session(meta(model.AgentClaude, "posix", "", "/Users/Al/proj", "", "POSIX", 3, 1, 0, false), 1, 1))),
				group(directoryKey(`C:\Users\Al\proj`), `~\proj`, Directory, `C:\Users\Al\proj`, "", 2, 2, false,
					group(dirAgentKey(`C:\Users\Al\proj`, model.AgentClaude), "Claude Code", Agent, "", model.AgentClaude, 1, 1, false,
						session(meta(model.AgentClaude, "w2", "", `C:\users\al\proj\`, "", "Claude", 1, 0, 1, false), 1, 1)),
					group(dirAgentKey(`C:\Users\Al\proj`, model.AgentCodex), "Codex", Agent, "", model.AgentCodex, 1, 1, false,
						session(meta(model.AgentCodex, "w1", "", `\\?\c:\Users\Al\proj`, "", "Codex", 2, 1, 0, false), 1, 1))),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Build(tt.input, tt.opts)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Build() =\n%+v\nwant\n%+v", got, tt.want)
			}
		})
	}
}

func TestBuildKeysAndTies(t *testing.T) {
	// The naive path:agent concatenation collides for these two pairs.
	a := meta(model.AgentID("z"), "a", "", "/x:y", "", "A", 1, 1, 0, false)
	b := meta(model.AgentID("y:z"), "b", "", "/x", "", "B", 1, 0, 1, false)
	c := meta(model.AgentID("z"), "c", "", "/x:y", "", "C", 1, 0, 1, false)
	input := []model.SessionMeta{c, b, a}
	first := Build(input, Options{})
	if first[0].Children[0].Key == first[1].Children[0].Key {
		t.Fatalf("different directory/agent pairs have identical keys: %s", first[0].Children[0].Key)
	}
	if got, want := first[1].Children[0].SessionRefs, []model.SessionRef{a.Ref, c.Ref}; !reflect.DeepEqual(got, want) {
		t.Errorf("equal-time siblings or refs order = %v, want %v", got, want)
	}
	slices.Reverse(input)
	if got := Build(input, Options{}); !reflect.DeepEqual(got, first) {
		t.Errorf("tree depends on input ordering: %v vs %v", got, first)
	}

	// Home affects labels, not path-based keys.
	changed := Build(input, Options{Home: "/x"})
	firstKeys := make(map[string]string)
	for _, n := range first {
		firstKeys[n.Path] = n.Key
	}
	for _, n := range changed {
		if firstKeys[n.Path] != n.Key {
			t.Errorf("key for path %s changed: got %s, want %s", n.Path, n.Key, firstKeys[n.Path])
		}
	}
}

func TestBuildEmpty(t *testing.T) {
	if got := Build(nil, Options{}); len(got) != 0 {
		t.Fatalf("Build(nil) = %v, want empty", got)
	}
}
