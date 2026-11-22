package agent

import (
	"context"
	"reflect"
	"sync"
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

// TestSpawnObserverReceivesLinesInOrder: the observer sees stdout lines as
// they are emitted, in order, with newlines stripped.
func TestSpawnObserverReceivesLinesInOrder(t *testing.T) {
	s := Spec{Name: "sh", Bin: "/bin/sh", Base: []string{"-c"}}
	var mu sync.Mutex
	var lines []string
	res, err := SpawnWithObserver(context.Background(), s,
		[]string{"-c", "echo a-line; sleep 0.1; echo b-line; sleep 0.1; echo c-line"}, nil, ".",
		60*time.Second, 60*time.Second,
		func(line string) {
			mu.Lock()
			lines = append(lines, line)
			mu.Unlock()
		})
	if err != nil {
		t.Fatal(err)
	}
	if res.Code != 0 {
		t.Fatalf("want exit 0, got %+v", res)
	}
	mu.Lock()
	defer mu.Unlock()
	want := []string{"a-line", "b-line", "c-line"}
	if len(lines) != len(want) {
		t.Fatalf("got %d lines, want %d: %#v", len(lines), len(want), lines)
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Fatalf("line %d = %q, want %q (all: %#v)", i, lines[i], want[i], lines)
		}
	}
}

// TestSpawnNoObserverUnchanged: Spawn (nil observer) still returns the full
// captured stdout and exit code.
func TestSpawnNoObserverUnchanged(t *testing.T) {
	s := Spec{Name: "sh", Bin: "/bin/sh", Base: []string{"-c"}}
	res, err := Spawn(context.Background(), s,
		[]string{"-c", "echo plain"}, nil, ".",
		60*time.Second, 60*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if res.Code != 0 || res.Stdout != "plain\n" || res.Stalled || res.TimedOut {
		t.Fatalf("want unchanged Spawn result {Code:0 Stdout:plain\n}, got %+v", res)
	}
}

// TestSpawnObserverFlushTrailingLine: a final line without a newline is
// still delivered to the observer after the child exits.
func TestSpawnObserverFlushTrailingLine(t *testing.T) {
	s := Spec{Name: "sh", Bin: "/bin/sh", Base: []string{"-c"}}
	var mu sync.Mutex
	var lines []string
	observe := func(line string) {
		mu.Lock()
		lines = append(lines, line)
		mu.Unlock()
	}
	res, err := SpawnWithObserver(context.Background(), s,
		[]string{"-c", "printf 'tail-no-newline'"}, nil, ".",
		60*time.Second, 60*time.Second, observe)
	if err != nil {
		t.Fatal(err)
	}
	if res.Code != 0 {
		t.Fatalf("want exit 0, got %+v", res)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(lines) != 1 || lines[0] != "tail-no-newline" {
		t.Fatalf("want %#v, got %#v", []string{"tail-no-newline"}, lines)
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

// TestSpawnKillPathChattyChildNoRace: a chatty child is killed at the
// wall-clock boundary while a stdio-copy goroutine may be mid-Write, which
// is exactly the pair that raced in audit M1 (out.String() vs feeder.Write).
// The child is bounded (25s) so it can't leak, and max=1s guarantees the
// kill path with output still flowing. Run under `go test -race` to catch
// the String()/Write() race: without the drainDone + mutex guard this fails
// the race detector.
func TestSpawnKillPathChattyChildNoRace(t *testing.T) {
	s := Spec{Name: "sh", Bin: "/bin/sh", Base: []string{"-c"}}
	res, err := Spawn(context.Background(), s,
		[]string{"-c", "i=0; while [ $i -lt 500 ]; do echo tick; i=$((i+1)); sleep 0.05; done"}, nil, ".",
		60*time.Second, 1*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if !res.TimedOut || res.Code != 1 {
		t.Fatalf("want a killed chatty child (timedOut, code 1), got %+v", res)
	}
	if res.Stdout == "" {
		t.Fatal("expected captured output from the chatty child up to the kill")
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
