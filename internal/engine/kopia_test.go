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
	io.WriteString(stdout, f.out[key]) // output comes even with a failing exit
	return f.err[key]
}

var ret = Retention{Latest: 10, Hourly: 48, Daily: 7, Weekly: 4, Monthly: 12, Annual: 3}

func TestKopiaPolicies(t *testing.T) {
	f := &fakeExec{out: map[string]string{
		"policy show": `{"retention":{"keepLatest":10,"keepHourly":48,"keepDaily":7,"keepWeekly":4,"keepMonthly":12,"keepAnnual":3},` +
			`"files":{"ignore":["/ledger.db"]},"scheduling":{"manual":true}}` + "\n" +
			`{"retention":{"keepLatest":10},"files":{},"scheduling":{"intervalSeconds":43200}}` + "\n",
	}}
	k := &Kopia{Exec: f, Container: "kopia"}
	got, err := k.Policies(context.Background(), []string{"/data/ledger", "/data/keep"})
	if err != nil {
		t.Fatal(err)
	}
	if len(f.cmds) != 1 || f.cmds[0] != "kopia: kopia policy show /data/ledger /data/keep --json" {
		t.Fatalf("one call for every path: %q", f.cmds)
	}
	if !got[0].Manual || got[0].Retention == nil || *got[0].Retention != ret || got[0].Ignores[0] != "/ledger.db" {
		t.Fatalf("%+v", got[0])
	}
	if got[1].Manual || got[1].Retention != nil {
		t.Fatalf("partly set retention counts as not set: %+v", got[1])
	}
	// Fewer documents than paths is an error, not a silent mismatch.
	if _, err := k.Policies(context.Background(), []string{"/a", "/b", "/c"}); err == nil {
		t.Fatal("no error for a missing policy")
	}
}

func TestKopiaConfigure(t *testing.T) {
	f := &fakeExec{}
	k := &Kopia{Exec: f, Container: "kopia"}
	cur := Current{Ignores: []string{"/old.db", "*.log"}, Retention: &Retention{Latest: 10, Hourly: 48, Daily: 7, Weekly: 4, Monthly: 24, Annual: 3}}
	if err := k.Configure(context.Background(), "/data/ledger", cur, Policy{Ignores: []string{"*.log", "/ledger.db"}, Retention: ret}); err != nil {
		t.Fatal(err)
	}
	want := "kopia: kopia policy set /data/ledger --manual --remove-ignore=/old.db --add-ignore=/ledger.db " +
		"--keep-latest=10 --keep-hourly=48 --keep-daily=7 --keep-weekly=4 --keep-monthly=12 --keep-annual=3"
	if len(f.cmds) != 1 || f.cmds[0] != want {
		t.Fatalf("got %q", f.cmds)
	}

	// Already as wanted: no policy set.
	f.cmds = nil
	cur = Current{Ignores: []string{"/ledger.db"}, Retention: &ret, Manual: true}
	if err := k.Configure(context.Background(), "/data/ledger", cur, Policy{Ignores: []string{"/ledger.db"}, Retention: ret}); err != nil {
		t.Fatal(err)
	}
	if len(f.cmds) != 0 {
		t.Fatalf("got %q", f.cmds)
	}

	// No ignores still sets a list of its own, so a parent's don't apply,
	// and retention inherited (not set on the path) is set explicitly.
	cur = Current{Ignores: []string{"/keep.db"}, Manual: true}
	if err := k.Configure(context.Background(), "/data/keep/staging/keep", cur, Policy{Retention: ret}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.cmds[0], "--add-ignore="+noInherit) || !strings.Contains(f.cmds[0], "--keep-latest=10") {
		t.Fatalf("got %q", f.cmds)
	}
}

func TestKopiaSnapshotBatch(t *testing.T) {
	f := &fakeExec{out: map[string]string{
		"snapshot create": `{"id":"abc","source":{"path":"/data/app"},"startTime":"2026-10-08T19:47:54Z","endTime":"2026-10-08T19:48:54Z",` +
			`"rootEntry":{"summ":{"size":600,"files":3,"numFailed":1}}}` + "\n" +
			`{"id":"def","source":{"path":"/data/b"},"rootEntry":{"summ":{"size":5,"files":1}}}` + "\n",
	}}
	k := &Kopia{Exec: f, Container: "kopia"}
	got, err := k.Snapshot(context.Background(), []string{"/data/app", "/data/b"}, "Keep run 1")
	if err != nil {
		t.Fatal(err)
	}
	snap := got["/data/app"]
	if snap.ID != "abc" || snap.Size != 600 || snap.Files != 3 || snap.Errors != 1 || snap.End.Sub(snap.Start).Seconds() != 60 || got["/data/b"].ID != "def" {
		t.Fatalf("%+v", got)
	}
	if len(f.cmds) != 1 || !strings.Contains(f.cmds[0], "snapshot create /data/app /data/b --json --description=Keep run 1") {
		t.Fatal(f.cmds)
	}

	// A path missing from the output failed, even with exit status 0.
	got, err = k.Snapshot(context.Background(), []string{"/data/app", "/data/b", "/data/c"}, "")
	if err == nil || len(got) != 2 {
		t.Fatalf("%v %v", got, err)
	}
	// A failing exit keeps the snapshots that worked.
	f.err = map[string]error{"snapshot create": errors.New("exit status 1: upload error: /data/c: gone")}
	got, err = k.Snapshot(context.Background(), []string{"/data/app", "/data/b", "/data/c"}, "")
	if err == nil || !strings.Contains(err.Error(), "kopia snapshot create") || len(got) != 2 {
		t.Fatalf("%v %v", got, err)
	}
}

func TestKopiaStats(t *testing.T) {
	f := &fakeExec{out: map[string]string{"content stats": "Count: 14557\nTotal Bytes: 10525447971\nTotal Packed: 10520993036 (compression 0.0%)\n"}}
	st, err := (&Kopia{Exec: f, Container: "kopia"}).Stats(context.Background())
	if err != nil || st.Size != 10520993036 {
		t.Fatalf("%+v %v", st, err)
	}
	f.out["content stats"] = "Count: 1\nTotal Bytes: 651\nAverage: 651\n"
	if st, err := (&Kopia{Exec: f, Container: "kopia"}).Stats(context.Background()); err != nil || st.Size != 651 {
		t.Fatalf("uncompressed: %+v %v", st, err)
	}
}

func TestKopiaList(t *testing.T) {
	f := &fakeExec{out: map[string]string{"snapshot list": `[{"id":"3c89","source":{"host":"h","userName":"root","path":"/data/a"},` +
		`"description":"Keep run 1","startTime":"2026-10-09T06:14:53Z","endTime":"2026-10-09T06:14:54Z",` +
		`"rootEntry":{"summ":{"size":11,"files":2}}}]`}}
	got, err := (&Kopia{Exec: f, Container: "kopia"}).List(context.Background())
	if err != nil || len(got) != 1 || got[0].ID != "3c89" || got[0].Path != "/data/a" || got[0].Description != "Keep run 1" || got[0].Files != 2 {
		t.Fatalf("%+v %v", got, err)
	}
	if f.cmds[0] != "kopia: kopia snapshot list --all --json" {
		t.Fatal(f.cmds)
	}
}

// Output as kopia 0.23 prints it, from a test repository with every pack
// damaged.
func TestKopiaVerify(t *testing.T) {
	progress := `{"processedObjectCount":0,"processedBytes":0,"readFileCount":0,"readBytes":0,"expectedTotalObjectCount":4}` + "\n"
	f := &fakeExec{out: map[string]string{"snapshot verify": progress +
		`{"stats":{"processedObjectCount":4,"processedBytes":11,"readFileCount":0,"readBytes":0},"errorCount":2,` +
		`"errorStrings":["error reading object ee3f: invalid checksum","error reading object f313: invalid checksum"]}` + "\n"},
		err: map[string]error{"snapshot verify": errors.New("exit status 1: encountered 2 errors")}}
	k := &Kopia{Exec: f, Container: "kopia"}
	v, err := k.Verify(context.Background(), 5)
	if err != nil || v.ErrorCount != 2 || len(v.Errors) != 2 || v.Objects != 4 {
		t.Fatalf("damage is a result, not a failure to run: %+v %v", v, err)
	}
	if f.cmds[0] != "kopia: kopia snapshot verify --verify-files-percent=5 --json" {
		t.Fatal(f.cmds)
	}

	f.out["snapshot verify"] = progress + `{"stats":{"processedObjectCount":6,"readFileCount":3,"readBytes":15},"errorCount":0}` + "\n"
	f.err = nil
	if v, err := k.Verify(context.Background(), 5); err != nil || v.ErrorCount != 0 || v.Files != 3 || v.Bytes != 15 {
		t.Fatalf("%+v %v", v, err)
	}

	// No summary: the check didn't run.
	f.out["snapshot verify"] = ""
	f.err = map[string]error{"snapshot verify": errors.New("exit status 1: can't connect to storage")}
	if _, err := k.Verify(context.Background(), 5); err == nil || !strings.Contains(err.Error(), "connect") {
		t.Fatal(err)
	}
}

func TestKopiaDelete(t *testing.T) {
	f := &fakeExec{}
	k := &Kopia{Exec: f, Container: "kopia"}
	if err := k.Delete(context.Background(), []string{"a", "b"}); err != nil {
		t.Fatal(err)
	}
	if len(f.cmds) != 1 || f.cmds[0] != "kopia: kopia snapshot delete a b --delete" {
		t.Fatal(f.cmds)
	}
}

func TestKopiaRestore(t *testing.T) {
	f := &fakeExec{}
	k := &Kopia{Exec: f, Container: "kopia"}
	if err := k.Restore(context.Background(), "3c89", "/sub/dir/", "/r/x/sub/dir"); err != nil {
		t.Fatal(err)
	}
	if err := k.Restore(context.Background(), "3c89", "", "/r/x"); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"kopia: kopia snapshot restore 3c89/sub/dir /r/x/sub/dir --parallel=32 --skip-owners",
		"kopia: kopia snapshot restore 3c89 /r/x --parallel=32 --skip-owners",
	}
	if strings.Join(f.cmds, "\n") != strings.Join(want, "\n") {
		t.Fatalf("%q", f.cmds)
	}
	// What kopia 0.23 says for a path the snapshot doesn't have.
	f.err = map[string]error{"snapshot restore": errors.New("exit status 1: unable to get filesystem entry: error reading directory: entry not found")}
	if err := k.Restore(context.Background(), "3c89", "nope", "/r/x/nope"); !errors.Is(err, ErrNotInSnapshot) {
		t.Fatal(err)
	}
}
