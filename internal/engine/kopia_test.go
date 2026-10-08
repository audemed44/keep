package engine

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

// fakeExec answers kopia commands from a table keyed by the first two
// arguments, and records every command.
type fakeExec struct {
	out  map[string]string
	err  map[string]error
	cmds []string
}

func (f *fakeExec) Exec(_ context.Context, container string, cmd []string, stdout io.Writer) error {
	f.cmds = append(f.cmds, container+": "+strings.Join(cmd, " "))
	key := cmd[1] + " " + cmd[2]
	if err := f.err[key]; err != nil {
		return err
	}
	_, err := io.WriteString(stdout, f.out[key])
	return err
}

func TestKopiaConfigure(t *testing.T) {
	f := &fakeExec{out: map[string]string{
		"policy show": `{"files":{"ignore":["/old.db","*.log"]},"scheduling":{"intervalSeconds":43200}}`,
	}}
	k := &Kopia{Exec: f, Container: "kopia"}
	if err := k.Configure(context.Background(), "/data/ledger", []string{"*.log", "/ledger.db"}); err != nil {
		t.Fatal(err)
	}
	want := "kopia: kopia policy set /data/ledger --manual --remove-ignore=/old.db --add-ignore=/ledger.db"
	if len(f.cmds) != 2 || f.cmds[1] != want {
		t.Fatalf("got %q", f.cmds)
	}

	// Already as wanted: no policy set.
	f = &fakeExec{out: map[string]string{
		"policy show": `{"files":{"ignore":["/ledger.db"]},"scheduling":{"manual":true}}`,
	}}
	k.Exec = f
	if err := k.Configure(context.Background(), "/data/ledger", []string{"/ledger.db"}); err != nil {
		t.Fatal(err)
	}
	if len(f.cmds) != 1 {
		t.Fatalf("got %q", f.cmds)
	}

	// No ignores still sets a list of its own, so a parent's don't apply.
	f = &fakeExec{out: map[string]string{"policy show": `{"files":{"ignore":["/keep.db"]},"scheduling":{"manual":true}}`}}
	k.Exec = f
	if err := k.Configure(context.Background(), "/data/keep/staging/keep", nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.cmds[1], "--add-ignore="+noInherit) {
		t.Fatalf("got %q", f.cmds)
	}
}

func TestKopiaSnapshot(t *testing.T) {
	f := &fakeExec{out: map[string]string{
		"snapshot create": `{"id":"abc","source":{"path":"/data/app"},"startTime":"2026-10-08T19:47:54Z","endTime":"2026-10-08T19:48:54Z",` +
			`"rootEntry":{"summ":{"size":600,"files":3,"numFailed":1}}}`,
	}}
	k := &Kopia{Exec: f, Container: "kopia"}
	snap, err := k.Snapshot(context.Background(), "/data/app", "Keep run 1: app")
	if err != nil {
		t.Fatal(err)
	}
	if snap.ID != "abc" || snap.Size != 600 || snap.Files != 3 || snap.Errors != 1 || snap.End.Sub(snap.Start).Seconds() != 60 {
		t.Fatalf("%+v", snap)
	}
	if !strings.Contains(f.cmds[0], "snapshot create /data/app --json --description=Keep run 1: app") {
		t.Fatal(f.cmds[0])
	}

	f.out["snapshot create"] = "not json"
	if _, err := k.Snapshot(context.Background(), "/data/app", ""); err == nil {
		t.Fatal("bad output passed")
	}
	f.err = map[string]error{"snapshot create": errors.New("exit status 1")}
	if _, err := k.Snapshot(context.Background(), "/data/app", ""); err == nil || !strings.Contains(err.Error(), "kopia snapshot create") {
		t.Fatalf("got %v", err)
	}
}

func TestKopiaStats(t *testing.T) {
	f := &fakeExec{out: map[string]string{"blob stats": "Count: 13\nTotal: 25622\nAverage: 1970\nHistogram:\n"}}
	st, err := (&Kopia{Exec: f, Container: "kopia"}).Stats(context.Background())
	if err != nil || st.Size != 25622 {
		t.Fatalf("%+v %v", st, err)
	}
}
