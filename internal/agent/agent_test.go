package agent

import (
	"context"
	"reflect"
	"testing"
	"time"
)

func TestArgs(t *testing.T) {
	s := Candidates[0]
	got := Args(s, []string{"Read", "Write"}, false, "/target", "prompt")
	want := []string{"-p", "--allowedTools", "Read,Write", "--permission-mode", "acceptEdits", "--add-dir", "/target", "--output-format", "text", "prompt"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%#v != %#v", got, want)
	}
	c := Candidates[1]
	got = Args(c, nil, true, "/target", "p")
	want = []string{"--dangerously-bypass-approvals-and-sandbox", "exec", "p"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%#v != %#v", got, want)
	}
}

func TestEnv(t *testing.T) {
	got := Env([]string{"A=B"}, "/w", "/t")
	if len(got) != 3 || got[1] != "CKB_WHITEHAT_WORKSPACE=/w" {
		t.Fatal(got)
	}
}

// TestSpawnStallKillsSilentChild: a child that never prints is killed at the
// stall timeout (spec §4.5).
func TestSpawnStallKillsSilentChild(t *testing.T) {
	s := Spec{Name: "sh", Bin: "/bin/sh", Base: []string{"-c"}}
	res, err := Spawn(context.Background(), s,
		[]string{"-c", "sleep 300"}, nil, ".",
		2*time.Second, 60*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Stalled || res.Code != 1 {
		t.Fatalf("want stalled, got %+v", res)
	}
}

// TestSpawnMaxRuntimeKills: the wall-clock cap fires regardless of output.
func TestSpawnMaxRuntimeKills(t *testing.T) {
	s := Spec{Name: "sh", Bin: "/bin/sh", Base: []string{"-c"}}
	res, err := Spawn(context.Background(), s,
		[]string{"-c", "sleep 300"}, nil, ".",
		60*time.Second, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if !res.TimedOut || res.Code != 1 {
		t.Fatalf("want timedOut, got %+v", res)
	}
}

// TestSpawnStallResetsOnOutput: continuous output must keep re-arming the
// stall timer (the parity fix from the Node CLI — a chatty agent is not
// silent). The child emits a tick every 200ms for 4s; a 1s stall budget
// would kill it almost immediately unless the timer resets on each byte.
func TestSpawnStallResetsOnOutput(t *testing.T) {
	s := Spec{Name: "sh", Bin: "/bin/sh", Base: []string{"-c"}}
	res, err := Spawn(context.Background(), s,
		[]string{"-c", "i=0; while [ $i -lt 20 ]; do echo tick; i=$((i+1)); sleep 0.2; done; echo done"}, nil, ".",
		1*time.Second, 60*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if res.Stalled || res.TimedOut || res.Code != 0 {
		t.Fatalf("chatty child must survive a shorter stall budget; got %+v", res)
	}
	if res.Stdout == "" {
		t.Fatal("expected captured output from the chatty child")
	}
}
