#!/usr/bin/env bash
# Node <-> Go CLI parity harness (docs/go-port-spec.md §5).
#
# Drives a fake `claude` agent (test/parity/fake-claude) under BOTH the Node
# CLI (cli/bin/ckb-whitehat.mjs) and the Go CLI (cmd/ckb-whitehat), then
# byte-compares the observable behavior: exit codes, stdout, stderr, and — the
# strongest check — the exact argv / prompt / env each CLI handed to the agent
# (the fake records them to the workspace). Node is the source of truth for the
# shared command surface; Go-only additions (clean, compare, gates, URL targets)
# are out of byte-parity scope.
#
# Usage: bash test/parity/parity.sh   (from repo root; needs `go` and `node`)
set -u

root="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$root"

export PATH=/opt/homebrew/bin:$PATH
command -v go   >/dev/null 2>&1 || { echo "FATAL: go not on PATH";     exit 2; }
command -v node >/dev/null 2>&1 || { echo "FATAL: node not on PATH";   exit 2; }

# TMPDIR ends in '/' on macOS, so mktemp's result would contain a double
# slash ('//ckb-parity...') that norm()'s sed patterns never match (the CLIs
# print path-cleaned single-slash paths). Normalize the string with pwd.
base="${TMPDIR:-/tmp}"; base="${base%/}"
tmp="$(cd "$(mktemp -d "$base/ckb-parity.XXXXXX")" && pwd)"
trap 'rm -rf "$tmp"' EXIT
mkdir -p "$tmp/bin"
GOBIN="$tmp/bin/ckb-whitehat"
NODECLI="$root/cli/bin/ckb-whitehat.mjs"
NOCMPCLI="$root/cli/bin/compare-runs.mjs"
nodehome="$tmp/nodehome"; gohome="$tmp/gohome"; mkdir -p "$nodehome" "$gohome"

FAIL=0
note()   { printf '    %s\n' "$*"; }
section(){ printf '\n== %s\n' "$*"; }

# normalize a captured stream: map the owning impl's home to $HOME
norm() { sed "s#$nodehome#\$HOME#g; s#$gohome#\$HOME#g" "$1"; }

# compare <label> <rcNode> <rcGo> <streamNode> <streamGo> <want-match:yes|no>
# streams are already files; we normalize both then byte-diff (or ignore).
compare() {
  local label="$1" rcn="$2" rcg="$3" fn="$4" fg="$5" want="$6"
  local ok=1
  [ "$rcn" != "$rcg" ] && ok=0
  local nn="$tmp/.nn" ng="$tmp/.ng"
  norm "$fn" > "$nn"; norm "$fg" > "$ng"
  if [ "$want" = yes ] && ! cmp -s "$nn" "$ng"; then ok=0; fi
  if [ "$ok" = 1 ]; then
    note "PASS $label (rc=$rcn)"
  else
    FAIL=1
    note "FAIL $label"
    [ "$rcn" != "$rcg" ] && note "  rc: node=$rcn go=$rcg"
    if [ "$want" = yes ]; then
      note "  normalized diff (node vs go):"
      diff -u "$nn" "$ng" | head -80 | sed 's/^/  /'
    fi
  fi
}

# --- build Go ---------------------------------------------------------------
section "build go"
if ! go build -o "$GOBIN" ./cmd/ckb-whitehat 2>"$tmp/gobuild.err"; then
  echo "FATAL: go build failed:"; cat "$tmp/gobuild.err"; exit 2
fi
note "built $GOBIN"

# --- fake agent -------------------------------------------------------------
FAKEBIN="$tmp/fakebin"; mkdir -p "$FAKEBIN"
cp "$root/test/parity/fake-claude" "$FAKEBIN/claude"; chmod +x "$FAKEBIN/claude"

# run_<impl> <out> <err> <rcfile> [FAKE_MODE] <cli args...>
run_node() { local o="$1" e="$2" r="$3" m="$4"; shift 4
  HOME="$nodehome" PATH="$FAKEBIN:$PATH" FAKE_MODE="$m" \
    node "$NODECLI" "$@" > "$o" 2> "$e"; echo $? > "$r"; }
run_go()   { local o="$1" e="$2" r="$3" m="$4"; shift 4
  HOME="$gohome"   PATH="$FAKEBIN:$PATH" FAKE_MODE="$m" \
    "$GOBIN" "$@" > "$o" 2> "$e"; echo $? > "$r"; }

# slug: exactly what both CLIs compute for a target path.
# Node:  resolved.replace(/[^a-zA-Z0-9]+/g,'-').replace(/^-+|-+$/g,'').toLowerCase()
# Go:    same collapse of non-alnum runs, trim dashes, lowercase.
# So: lowercase, then collapse each run of non-alnum to a single '-', trim.
slug() {
  printf '%s' "$1" \
    | tr '[:upper:]' '[:lower:]' \
    | perl -pe 's/[^a-z0-9]+/-/g; s/^-+//; s/-$//'
}
init_target() { # init_target <targdir>
  mkdir -p "$1"; ( cd "$1" && git init -q )
}

# ================= S1: init =================================================
section "S1 init parity"
t1="$tmp/t-init"; init_target "$t1"
run_node "$tmp/i1.n.o" "$tmp/i1.n.e" "$tmp/i1.n.r" "" init "$t1" --scope app,chain
run_go   "$tmp/i1.g.o" "$tmp/i1.g.e" "$tmp/i1.g.r" "" init "$t1" --scope app,chain
compare "init stdout+rc" "$(cat "$tmp/i1.n.r")" "$(cat "$tmp/i1.g.r")" \
        "$tmp/i1.n.o" "$tmp/i1.g.o" yes

label="init config.json (shared keys semantic)"
sl="$(slug "$t1")"
if node -e '
  const fs=require("fs");
  const pick=o=>({version:o.version,target:o.target,scope:o.scope,onchain:o.onchain,services:o.services||[]});
  const a=pick(JSON.parse(fs.readFileSync(process.argv[1],"utf8")));
  const b=pick(JSON.parse(fs.readFileSync(process.argv[2],"utf8")));
  const ja=JSON.stringify(a), jb=JSON.stringify(b);
  if(ja!==jb){console.log("  differs:\n    node:",ja,"\n    go  :",jb);process.exit(1);}
  console.log("    shared keys: "+ja);
' "$nodehome/.ckb-whitehat/$sl/config.json" "$gohome/.ckb-whitehat/$sl/config.json" > "$tmp/cfg.out" 2>&1
then
  note "PASS $label"; sed 's/^/    /' "$tmp/cfg.out"
else
  note "FAIL $label"; sed 's/^/    /' "$tmp/cfg.out"; FAIL=1
fi

# ================= S2: run scenarios ========================================
for mode in fire clean silent fail; do
  section "S2 run[$mode]"
  t="$tmp/t-$mode"; init_target "$t"
  run_node "$tmp/$mode.n.i.o" /dev/null "$tmp/$mode.n.i.r" "" init "$t" --scope app,chain
  run_go   "$tmp/$mode.g.i.o" /dev/null "$tmp/$mode.g.i.r" "" init "$t" --scope app,chain
  run_node "$tmp/$mode.n.o" "$tmp/$mode.n.e" "$tmp/$mode.n.r" "$mode" run "$t" --agent claude --stall-timeout 30
  run_go   "$tmp/$mode.g.o" "$tmp/$mode.g.e" "$tmp/$mode.g.r" "$mode" run "$t" --agent claude --stall-timeout 30
  compare "run[$mode] rc+stdout" "$(cat "$tmp/$mode.n.r")" "$(cat "$tmp/$mode.g.r")" \
          "$tmp/$mode.n.o" "$tmp/$mode.g.o" yes
  # both emit stderr only in these two modes; byte-compare when present
  if [ "$mode" = silent ] || [ "$mode" = fail ]; then
    compare "run[$mode] stderr" 0 0 "$tmp/$mode.n.e" "$tmp/$mode.g.e" yes
  fi
done

# --- stall: silent-for-30s fake, killed by a 3s stall budget ---------------
section "S2 run[stall]"
t="$tmp/t-stall"; init_target "$t"
run_node "$tmp/s.n.i.o" /dev/null "$tmp/s.n.i.r" "" init "$t" --scope app,chain
run_go   "$tmp/s.g.i.o" /dev/null "$tmp/s.g.i.r" "" init "$t" --scope app,chain
run_node "$tmp/s.n.o" "$tmp/s.n.e" "$tmp/s.n.r" stall run "$t" --agent claude --stall-timeout 3
run_go   "$tmp/s.g.o" "$tmp/s.g.e" "$tmp/s.g.r" stall run "$t" --agent claude --stall-timeout 3
compare "run[stall] rc+stderr" "$(cat "$tmp/s.n.r")" "$(cat "$tmp/s.g.r")" \
        "$tmp/s.n.e" "$tmp/s.g.e" yes

# --- chatty: output keeps the stall timer re-armed (§4.5) -----------------
section "S2 run[chatty]"
t="$tmp/t-chatty"; init_target "$t"
run_node "$tmp/c.n.i.o" /dev/null "$tmp/c.n.i.r" "" init "$t" --scope app,chain
run_go   "$tmp/c.g.i.o" /dev/null "$tmp/c.g.i.r" "" init "$t" --scope app,chain
run_node "$tmp/c.n.o" "$tmp/c.n.e" "$tmp/c.n.r" chatty run "$t" --agent claude --stall-timeout 1
run_go   "$tmp/c.g.o" "$tmp/c.g.e" "$tmp/c.g.r" chatty run "$t" --agent claude --stall-timeout 1
compare "run[chatty] rc+stdout (survives 1s stall)" "$(cat "$tmp/c.n.r")" "$(cat "$tmp/c.g.r")" \
        "$tmp/c.n.o" "$tmp/c.g.o" yes

# ================= S3: status / report / usage =============================
section "S3 status/report/usage"
t="$tmp/t-status"; init_target "$t"
run_node "$tmp/x.n.i.o" /dev/null "$tmp/x.n.i.r" "" init "$t" --scope app,chain
run_go   "$tmp/x.g.i.o" /dev/null "$tmp/x.g.i.r" "" init "$t" --scope app,chain
run_node "$tmp/x.n.r.o" /dev/null "$tmp/x.n.r.r" fire run "$t" --agent claude
run_go   "$tmp/x.g.r.o" /dev/null "$tmp/x.g.r.r" fire run "$t" --agent claude

run_node "$tmp/st.n.o" /dev/null "$tmp/st.n.r" "" status --target "$t"
run_go   "$tmp/st.g.o" /dev/null "$tmp/st.g.r" "" status --target "$t"
compare "status rc+stdout" "$(cat "$tmp/st.n.r")" "$(cat "$tmp/st.g.r")" "$tmp/st.n.o" "$tmp/st.g.o" yes

run_node "$tmp/rp.n.o" /dev/null "$tmp/rp.n.r" "" report --target "$t"
run_go   "$tmp/rp.g.o" /dev/null "$tmp/rp.g.r" "" report --target "$t"
compare "report rc+stdout" "$(cat "$tmp/rp.n.r")" "$(cat "$tmp/rp.g.r")" "$tmp/rp.n.o" "$tmp/rp.g.o" yes

run_node "$tmp/u.n.o" /dev/null "$tmp/u.n.r" "" frobnicate
run_go   "$tmp/u.g.o" /dev/null "$tmp/u.g.r" "" frobnicate
compare "unknown command rc=2" "$(cat "$tmp/u.n.r")" "$(cat "$tmp/u.g.r")" /dev/null /dev/null no
[ "$(cat "$tmp/u.n.r")" = 2 ] && [ "$(cat "$tmp/u.g.r")" = 2 ] || { note "FAIL unknown-cmd rc (want both 2)"; FAIL=1; }

t6="$tmp/t-nows"; mkdir -p "$t6"
run_node "$tmp/nw.n.o" /dev/null "$tmp/nw.n.r" "" run "$t6"
run_go   "$tmp/nw.g.o" /dev/null "$tmp/nw.g.r" "" run "$t6"
compare "run w/o workspace rc+stdout" "$(cat "$tmp/nw.n.r")" "$(cat "$tmp/nw.g.r")" "$tmp/nw.n.o" "$tmp/nw.g.o" yes
[ "$(cat "$tmp/nw.n.r")" = 2 ] && [ "$(cat "$tmp/nw.g.r")" = 2 ] || { note "FAIL no-workspace rc (want both 2)"; FAIL=1; }

run_node "$tmp/v.n.o" /dev/null "$tmp/v.n.r" "" version
run_go   "$tmp/v.g.o" /dev/null "$tmp/v.g.r" "" version
compare "version rc+stdout" "$(cat "$tmp/v.n.r")" "$(cat "$tmp/v.g.r")" "$tmp/v.n.o" "$tmp/v.g.o" yes

run_node /dev/null /dev/null "$tmp/h.n.r" "" help
run_go   /dev/null /dev/null "$tmp/h.g.r" "" help
[ "$(cat "$tmp/h.n.r")" = 0 ] && [ "$(cat "$tmp/h.g.r")" = 0 ] \
  && note "PASS help rc=0 (text differs by design)" || { note "FAIL help rc"; FAIL=1; }

# ================= S4: Go compare vs Node compare-runs.mjs ==================
section "S4 compare parity"
fx="$root/test/parity/fixtures"
HOME="$nodehome" node "$NOCMPCLI" "$fx/new-report.md" "$fx/base-report.md" > "$tmp/cmp.n.o" 2>/dev/null; echo $? > "$tmp/cmp.n.r"
HOME="$gohome"   "$GOBIN"  compare "$fx/new-report.md" "$fx/base-report.md" > "$tmp/cmp.g.o" 2>/dev/null; echo $? > "$tmp/cmp.g.r"
compare "compare rc+stdout" "$(cat "$tmp/cmp.n.r")" "$(cat "$tmp/cmp.g.r")" "$tmp/cmp.n.o" "$tmp/cmp.g.o" yes

# ================= S5: golden argv / prompt / env ===========================
section "S5 what each CLI handed the agent"
t="$tmp/t-golden"; init_target "$t"
run_node "$tmp/g.n.i.o" /dev/null "$tmp/g.n.i.r" "" init "$t" --scope app,chain
run_go   "$tmp/g.g.i.o" /dev/null "$tmp/g.g.i.r" "" init "$t" --scope app,chain
run_node "$tmp/g.n.o" /dev/null "$tmp/g.n.r" fire run "$t" --agent claude
run_go   "$tmp/g.g.o" /dev/null "$tmp/g.g.r" fire run "$t" --agent claude
sl="$(slug "$t")"
nfl="$nodehome/.ckb-whitehat/$sl/agent-flags.txt"
gfl="$gohome/.ckb-whitehat/$sl/agent-flags.txt"
npr="$nodehome/.ckb-whitehat/$sl/agent-prompt.txt"
gpr="$gohome/.ckb-whitehat/$sl/agent-prompt.txt"
nen="$nodehome/.ckb-whitehat/$sl/agent-env.txt"
gen="$gohome/.ckb-whitehat/$sl/agent-env.txt"

# shape assert: the claude default argv must carry these tokens (both CLIs)
for tok in -p --allowedTools --permission-mode acceptEdits --add-dir --output-format text; do
  grep -qx -- "$tok" "$nfl" && grep -qx -- "$tok" "$gfl" \
    && note "PASS golden argv contains $tok" \
    || { note "FAIL golden argv missing '$tok' (node:$(grep -qx -- "$tok" "$nfl" && echo y || echo n) go:$(grep -qx -- "$tok" "$gfl" && echo y || echo n))"; FAIL=1; }
done

# byte parity of the flags each CLI passed (normalized)
if cmp -s <(norm "$nfl") <(norm "$gfl"); then
  note "PASS agent flags node==go"
else
  note "FAIL agent flags node!=go"; diff -u <(norm "$nfl") <(norm "$gfl") | sed 's/^/  /'; FAIL=1
fi
# byte parity of the prompt (embedded skill text must be identical)
if cmp -s <(norm "$npr") <(norm "$gpr"); then
  note "PASS agent prompt node==go ($(wc -c < "$npr") bytes)"
else
  note "FAIL agent prompt node!=go (skill drift? run: go test ./internal/skill/)"; \
    diff -u <(norm "$npr") <(norm "$gpr") | sed 's/^/  /' | head -40; FAIL=1
fi
# env key-set parity (values differ by home, so compare keys + CKB_WHITEHAT_TARGET only)
ek1=$(awk -F= '{print $1}' "$nen" | sort); ek2=$(awk -F= '{print $1}' "$gen" | sort)
[ "$ek1" = "$ek2" ] && note "PASS agent env key-set (node==go)" \
  || { note "FAIL agent env keys: node=[$ek1] go=[$ek2]"; FAIL=1; }
tv1=$(grep '^CKB_WHITEHAT_TARGET=' "$nen" | cut -d= -f2-); tv2=$(grep '^CKB_WHITEHAT_TARGET=' "$gen" | cut -d= -f2-)
[ "$tv1" = "$tv2" ] && note "PASS CKB_WHITEHAT_TARGET node==go" \
  || { note "FAIL CKB_WHITEHAT_TARGET node=[$tv1] go=[$tv2]"; FAIL=1; }

# ================= summary ==================================================
section "result"
if [ "$FAIL" = 0 ]; then
  echo "PARITY: ALL PASS"
  exit 0
fi
echo "PARITY: FAILURES (see above)"
exit 1
