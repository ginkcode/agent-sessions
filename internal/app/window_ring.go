package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ginkcode/agent-sessions/internal/configfile"
	"github.com/ginkcode/agent-sessions/internal/filelock"
)

// Every open window joins a ring in the claims dir so a shortcut can move
// to the next one: each window is its own process, so the system's own
// "next window of this app" (Cmd+` on macOS) never leaves the current one.
//
// A member holds win-<id>.lock while it is open and writes win-<id>.json
// next to it: where to reach it, as for a host claim, and when it joined,
// which orders the ring. A member whose lock is free has exited without
// leaving and is cleared by whoever finds it.

// ringInfo is a member's win-<id>.json.
type ringInfo struct {
	claimInfo
	Joined int64 `json:"joined"`
}

type ringMember struct {
	id   string
	info ringInfo
}

const ringPrefix = "win-"

// joinRing makes this window one that the window shortcut cycles through.
func (h *hostClaims) joinRing() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.ringLock != nil {
		return nil
	}
	if err := os.MkdirAll(h.dir, 0o700); err != nil {
		return fmt.Errorf("create window ring dir: %w", err)
	}
	if err := h.listenLocked(); err != nil {
		return err
	}
	// The listener's token is random and private to this window, so a
	// prefix of it names the window without reusing a dead one's files.
	id := h.token[:16]
	base := filepath.Join(h.dir, ringPrefix+id)
	lock, err := filelock.TryLock(base + ".lock")
	if err != nil {
		return err
	}
	info := ringInfo{
		claimInfo: claimInfo{Port: h.ln.Addr().(*net.TCPAddr).Port, Token: h.token},
		Joined:    time.Now().UnixNano(),
	}
	data, err := json.Marshal(info)
	if err == nil {
		err = configfile.WriteAtomic(base+".json", data)
	}
	if err != nil {
		_ = lock.Unlock()
		return fmt.Errorf("write window ring entry: %w", err)
	}
	h.ringLock, h.ringID = lock, id
	return nil
}

// leaveRingLocked takes this window out of the ring.
func (h *hostClaims) leaveRingLocked() {
	if h.ringLock == nil {
		return
	}
	base := filepath.Join(h.dir, ringPrefix+h.ringID)
	// The entry goes first: nobody looks for a window without one.
	_ = os.Remove(base + ".json")
	_ = h.ringLock.Unlock()
	// No other window takes this name, so its lock file can go too.
	_ = os.Remove(base + ".lock")
	h.ringLock, h.ringID = nil, ""
}

// ringMembers lists the open windows, this one included, in the order they
// joined. Entries of windows that exited without leaving are removed.
func (h *hostClaims) ringMembers() ([]ringMember, error) {
	h.mu.Lock()
	self := h.ringID
	h.mu.Unlock()
	if self == "" {
		return nil, errors.New("this window is not in the window ring")
	}
	matches, err := filepath.Glob(filepath.Join(h.dir, ringPrefix+"*.json"))
	if err != nil {
		return nil, err
	}
	members := make([]ringMember, 0, len(matches))
	for _, path := range matches {
		id := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(path), ringPrefix), ".json")
		if id != self && !ringAlive(path) {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var info ringInfo
		if json.Unmarshal(data, &info) != nil || info.Port <= 0 || info.Token == "" {
			continue
		}
		members = append(members, ringMember{id: id, info: info})
	}
	sort.Slice(members, func(i, j int) bool {
		if members[i].info.Joined != members[j].info.Joined {
			return members[i].info.Joined < members[j].info.Joined
		}
		return members[i].id < members[j].id
	})
	return members, nil
}

// ringAlive reports whether the window behind entry still holds its lock,
// removing the entry of one that does not.
func ringAlive(entry string) bool {
	base := strings.TrimSuffix(entry, ".json")
	lock, err := filelock.TryLock(base + ".lock")
	if errors.Is(err, filelock.ErrLocked) {
		return true
	}
	if err != nil {
		// Unreadable: leave it, and skip it.
		return false
	}
	_ = os.Remove(entry)
	_ = lock.Unlock()
	_ = os.Remove(base + ".lock")
	return false
}

// focusNextWindow brings the window after this one to the front, or the
// one before it when forward is false. With no other window open it does
// nothing.
func (h *hostClaims) focusNextWindow(forward bool) error {
	members, err := h.ringMembers()
	if err != nil {
		return err
	}
	h.mu.Lock()
	self := h.ringID
	h.mu.Unlock()
	at := -1
	for i, m := range members {
		if m.id == self {
			at = i
			break
		}
	}
	if at < 0 || len(members) < 2 {
		return nil
	}
	step := 1
	if !forward {
		step = -1
	}
	// A window that does not answer (stalled, or just closing) is passed
	// over for the one after it.
	var lastErr error
	for n := 1; n < len(members); n++ {
		target := members[((at+step*n)%len(members)+len(members))%len(members)]
		if lastErr = h.focusVia(target.info.claimInfo); lastErr == nil {
			return nil
		}
	}
	return lastErr
}
