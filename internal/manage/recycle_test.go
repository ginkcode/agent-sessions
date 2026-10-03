package manage

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

type fakeRecycleNative struct {
	events      []string
	path        string
	availableOK bool
	locationErr error
	locationAt  int
	locationN   int
	initHR      recycleHRESULT
	newOpHR     recycleHRESULT
	newItemHR   recycleHRESULT
	op          *fakeRecycleOperation
}

func newFakeRecycleNative() *fakeRecycleNative {
	n := &fakeRecycleNative{availableOK: true}
	n.op = &fakeRecycleOperation{native: n}
	n.op.run = func(p *recycleProgress) {
		p.start()
		p.preDelete(recycleIfPossible, true)
		p.postDelete(recycleIfPossible, true, recycleOK, true)
		p.finish(recycleOK)
	}
	return n
}
func (n *fakeRecycleNative) available() bool { return n.availableOK }
func (n *fakeRecycleNative) checkLocation(string) error {
	n.events = append(n.events, "check-location")
	n.locationN++
	if n.locationAt == 0 || n.locationAt == n.locationN {
		return n.locationErr
	}
	return nil
}
func (n *fakeRecycleNative) lockThread()   { n.events = append(n.events, "lock") }
func (n *fakeRecycleNative) unlockThread() { n.events = append(n.events, "unlock") }
func (n *fakeRecycleNative) initialize() recycleHRESULT {
	n.events = append(n.events, "initialize")
	return n.initHR
}
func (n *fakeRecycleNative) uninitialize() { n.events = append(n.events, "uninitialize") }
func (n *fakeRecycleNative) newOperation() (recycleOperation, recycleHRESULT) {
	n.events = append(n.events, "new-operation")
	return n.op, n.newOpHR
}

func (n *fakeRecycleNative) newItem(path string) (recycleItem, recycleHRESULT) {
	n.events = append(n.events, "new-item")
	n.path = path
	return &fakeRecycleItem{n}, n.newItemHR
}

type fakeRecycleItem struct{ native *fakeRecycleNative }

func (i *fakeRecycleItem) release() { i.native.events = append(i.native.events, "release-item") }

type fakeRecycleOperation struct {
	native     *fakeRecycleNative
	flags      uint32
	flagsHR    recycleHRESULT
	adviseHR   recycleHRESULT
	unadviseHR recycleHRESULT
	deleteHR   recycleHRESULT
	performHR  recycleHRESULT
	abortHR    recycleHRESULT
	abort      bool
	progress   *recycleProgress
	run        func(*recycleProgress)
}

func (o *fakeRecycleOperation) release() {
	o.native.events = append(o.native.events, "release-operation")
}

func (o *fakeRecycleOperation) setFlags(flags uint32) recycleHRESULT {
	o.native.events = append(o.native.events, "flags")
	o.flags = flags
	return o.flagsHR
}

func (o *fakeRecycleOperation) advise(p *recycleProgress, _ recycleItem) (uint32, recycleHRESULT) {
	o.native.events = append(o.native.events, "advise")
	o.progress = p
	return 42, o.adviseHR
}

func (o *fakeRecycleOperation) unadvise(cookie uint32) recycleHRESULT {
	if cookie != 42 {
		panic("incorrect progress cookie")
	}
	o.native.events = append(o.native.events, "unadvise")
	return o.unadviseHR
}

func (o *fakeRecycleOperation) deleteItem(recycleItem) recycleHRESULT {
	o.native.events = append(o.native.events, "delete")
	return o.deleteHR
}

func (o *fakeRecycleOperation) perform() recycleHRESULT {
	o.native.events = append(o.native.events, "perform")
	if o.run != nil {
		o.run(o.progress)
	}
	return o.performHR
}

func (o *fakeRecycleOperation) aborted() (bool, recycleHRESULT) {
	o.native.events = append(o.native.events, "aborted")
	return o.abort, o.abortHR
}

func TestRecyclePathExactNativeValidation(t *testing.T) {
	for _, path := range []string{
		`C:\Users\Test User\.claude\projects\example\session.jsonl`,
		`d:\sessions\你好 😀.jsonl`,
		`C:\some folder\'quoted' & file.jsonl`,
		`C:\` + strings.Repeat("a", 256), // 259 UTF-16 units plus NUL
	} {
		t.Run(path, func(t *testing.T) {
			n := newFakeRecycleNative()
			if err := (recycleTransport{n}).Trash(path); err != nil {
				t.Fatal(err)
			}
			if n.path != path {
				t.Fatalf("changed target: %q != %q", n.path, path)
			}
		})
	}
	for _, path := range []string{
		"", `C:`, `C:\`, `C:\\`, `C:session.jsonl`, `\session.jsonl`, `/tmp/session.jsonl`,
		`C:/sessions/file`, `C:\sessions/file`, `\\host\share\session.jsonl`,
		`\\?\C:\sessions\file`, `\\.\C:\sessions\file`, `\??\C:\sessions\file`,
		`C:\sessions\*`, `C:\sessions\file?`, "C:\\sessions\\nul\x00file",
		`C:\sessions\file:stream`, `C:\sessions\file.`, `C:\sessions\file `,
		`C:\sessions\..\file`, `C:\sessions\.\file`, `C:\sessions\\file`, `C:\sessions\`,
		`C:\sessions\NUL`, `C:\sessions\con.txt`, `C:\sessions\COM1.log`,
		`C:\sessions\LPT9`, `C:\sessions\COM¹`, `C:\sessions\CONIN$`,
		`C:\$Recycle.Bin\file`, `C:\System Volume Information\file`,
		`C:\` + strings.Repeat("a", 257),
		`C:\` + strings.Repeat("😀", 128) + "a", // UTF-16, not rune/byte length
		"C:\\sessions\\\xff",
	} {
		t.Run("reject-"+path, func(t *testing.T) {
			n := newFakeRecycleNative()
			if err := (recycleTransport{n}).Trash(path); err == nil {
				t.Fatal("accepted unsafe path")
			}
			if len(n.events) != 0 || n.path != "" {
				t.Fatalf("unsafe path reached native boundary: %#v", n.events)
			}
		})
	}
}

func TestRecycleLocationUnavailableFailsBeforeCOM(t *testing.T) {
	for _, label := range []string{"network", "removable", "mount", "reparse", "unavailable"} {
		t.Run(label, func(t *testing.T) {
			n := newFakeRecycleNative()
			n.locationErr = errors.New("private C:\\Users\\secret\\session.jsonl")
			if label == "unavailable" {
				n.availableOK = false
			}
			err := (recycleTransport{n}).Trash(`C:\sessions\file`)
			if err == nil || strings.Contains(err.Error(), "secret") {
				t.Fatalf("invalid sanitized failure: %v", err)
			}
			if len(n.events) > 1 || n.path != "" {
				t.Fatalf("unsupported location reached COM: %#v", n.events)
			}
		})
	}
}

func TestRecycleConstructorNonDestructiveProbe(t *testing.T) {
	n := newFakeRecycleNative()
	tr, err := newRecycleTransport(n)
	if err != nil {
		t.Fatal(err)
	}
	if tr.Name() != "windows-recycle-bin" || tr.DisplayName() != "Recycle Bin" {
		t.Fatal("incorrect transport labels")
	}
	want := []string{"lock", "initialize", "new-operation", "flags", "advise", "unadvise", "release-operation", "uninitialize", "unlock"}
	if !reflect.DeepEqual(n.events, want) {
		t.Fatalf("probe invoked unexpected native operation: %#v", n.events)
	}
	if n.op.flags != recycleOperationFlags || n.op.flags&0x10 != 0 ||
		n.op.flags&recycleOnDelete == 0 || n.op.flags&recycleAddUndoRecord == 0 ||
		n.op.flags&recycleEarlyFailure == 0 || n.op.flags&recycleNoErrorUI == 0 ||
		n.op.flags&recycleWantNukeWarning == 0 || n.op.flags&recycleNoConnected == 0 {
		t.Fatalf("unsafe operation flags %#x", n.op.flags)
	}
}

func TestRecycleNativeLifecycle(t *testing.T) {
	n := newFakeRecycleNative()
	n.initHR = recycleFalse // Already-initialized apartment still needs Uninitialize.
	if err := (recycleTransport{n}).Trash(`C:\sessions\file`); err != nil {
		t.Fatal(err)
	}
	want := []string{"check-location", "lock", "initialize", "new-operation", "flags", "new-item", "advise", "delete", "check-location", "perform", "aborted", "unadvise", "release-item", "release-operation", "uninitialize", "unlock"}
	if !reflect.DeepEqual(n.events, want) {
		t.Fatalf("incorrect scoped native lifecycle: %#v", n.events)
	}
}

func TestRecycleSetupErrorsNeverPerform(t *testing.T) {
	for _, stage := range []string{"initialize", "new-operation", "flags", "new-item", "advise", "delete", "recheck"} {
		t.Run(stage, func(t *testing.T) {
			n := newFakeRecycleNative()
			const failure recycleHRESULT = 0x80070005
			switch stage {
			case "initialize":
				n.initHR = failure
			case "new-operation":
				n.newOpHR = failure
			case "flags":
				n.op.flagsHR = failure
			case "new-item":
				n.newItemHR = failure
			case "advise":
				n.op.adviseHR = failure
			case "delete":
				n.op.deleteHR = failure
			case "recheck":
				n.locationAt = 2
				n.locationErr = errors.New("private path")
			}
			err := (recycleTransport{n}).Trash(`C:\sessions\secret-file`)
			if err == nil || errors.Is(err, ErrTrashOutcomeUnknown) || strings.Contains(err.Error(), "secret") {
				t.Fatalf("incorrect setup error: %v", err)
			}
			for _, event := range n.events {
				if event == "perform" || event == "aborted" || (event == "uninitialize" && stage == "initialize") {
					t.Fatalf("incorrect error lifecycle: %#v", n.events)
				}
			}
			if n.events[len(n.events)-1] != "unlock" {
				t.Fatalf("thread leaked: %#v", n.events)
			}
		})
	}
}

func TestRecycleOperationFailuresAreOutcomeUnknown(t *testing.T) {
	for _, stage := range []string{"perform", "aborted", "abort-query", "unadvise", "no-callback", "item-failed", "no-recycled-item", "pre-veto"} {
		t.Run(stage, func(t *testing.T) {
			n := newFakeRecycleNative()
			switch stage {
			case "perform":
				n.op.performHR = 0x80070005
			case "aborted":
				n.op.abort = true
			case "abort-query":
				n.op.abortHR = recycleAbort
			case "unadvise":
				n.op.unadviseHR = recycleAbort
			case "no-callback":
				n.op.run = nil
			case "item-failed", "no-recycled-item":
				n.op.run = func(p *recycleProgress) {
					p.start()
					p.preDelete(recycleIfPossible, true)
					hr := recycleOK
					if stage == "item-failed" {
						hr = recycleAbort
					}
					p.postDelete(recycleIfPossible, true, hr, stage != "no-recycled-item")
					p.finish(recycleOK)
				}
			case "pre-veto":
				n.op.run = func(p *recycleProgress) {
					p.start()
					if hr := p.preDelete(0, true); hr != recycleAbort {
						t.Fatal("permanent delete not vetoed")
					}
					p.finish(recycleAbort)
				}
			}
			err := (recycleTransport{n}).Trash(`C:\sessions\secret-file`)
			if !errors.Is(err, ErrTrashOutcomeUnknown) || strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "nothing") {
				t.Fatalf("incorrect uncertain error: %v", err)
			}
			if !strings.Contains(strings.Join(n.events, ","), "perform,aborted,unadvise") {
				t.Fatalf("did not check abort after perform failure: %#v", n.events)
			}
		})
	}
}

func TestRecycleProgressFailClosed(t *testing.T) {
	for _, scenario := range []string{"no-start", "duplicate-start", "no-recycle-flag", "other-target", "duplicate-pre", "no-pre", "failed-result", "successful-skip", "user-ignored", "no-bin-item", "other-post-target", "no-post-flag", "duplicate-post", "finish-failed", "finish-before-post", "unexpected-operation", "after-finish"} {
		t.Run(scenario, func(t *testing.T) {
			p := &recycleProgress{}
			if scenario == "no-start" {
				if p.preDelete(recycleIfPossible, true) != recycleAbort {
					t.Fatal("pre without start not vetoed")
				}
			} else {
				p.start()
			}
			if scenario == "duplicate-start" {
				p.start()
			}
			flags := recycleIfPossible
			if scenario == "no-recycle-flag" {
				flags = 0
			}
			if scenario != "no-pre" {
				p.preDelete(flags, scenario != "other-target")
			}
			if scenario == "duplicate-pre" {
				p.preDelete(recycleIfPossible, true)
			}
			if scenario == "finish-before-post" {
				p.finish(recycleOK)
			}
			if scenario == "unexpected-operation" {
				p.veto()
			}
			hr := recycleOK
			if scenario == "failed-result" {
				hr = recycleAbort
			}
			if scenario == "successful-skip" {
				hr = recycleFalse
			}
			if scenario == "user-ignored" {
				hr = 0x00270005 // COPYENGINE_S_USER_IGNORED
			}
			if scenario == "no-post-flag" {
				flags = 0
			}
			p.postDelete(flags, scenario != "other-post-target", hr, scenario != "no-bin-item")
			if scenario == "duplicate-post" {
				p.postDelete(recycleIfPossible, true, recycleOK, true)
			}
			hr = recycleOK
			if scenario == "finish-failed" {
				hr = recycleAbort
			}
			p.finish(hr)
			if scenario == "after-finish" {
				p.preDelete(recycleIfPossible, true)
			}
			if p.verified() {
				t.Fatal("unsafe progress sequence reported recycled")
			}
		})
	}
}

func TestRecycleProgressAcceptsWholeItemSuccess(t *testing.T) {
	for _, hr := range []recycleHRESULT{recycleOK, recycleDontProcessChildren} {
		p := &recycleProgress{}
		p.start()
		p.preDelete(recycleIfPossible, true)
		if p.postDelete(recycleIfPossible, true, hr, true) != recycleOK || p.finish(recycleOK) != recycleOK || !p.verified() {
			t.Fatalf("recycled item with HRESULT 0x%08X not verified", uint32(hr))
		}
	}
}

func TestRecycleNonrecyclableFakeOperationVetoPreventsDelete(t *testing.T) {
	n := newFakeRecycleNative()
	deleted := false
	n.op.run = func(p *recycleProgress) {
		p.start()
		if p.preDelete(0, true) == recycleOK {
			deleted = true
			p.postDelete(0, true, recycleOK, false)
		}
		p.finish(recycleAbort)
	}
	if err := (recycleTransport{n}).Trash(`C:\sessions\file`); !errors.Is(err, ErrTrashOutcomeUnknown) || deleted {
		t.Fatalf("nonrecyclable operation was not refused: deleted=%v err=%v", deleted, err)
	}
}
