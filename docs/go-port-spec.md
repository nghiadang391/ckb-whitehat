# ckb-whitehat — Go CLI port spec

Status: **spec, not yet implemented.** End state: the CLI is a single static
Go binary; the Node CLI (`cli/bin/*.mjs`, `cli/package.json`) is **deleted**.
The PoC suites (`dular/poc/`, `dular/agent-run/`, `tapfiber/poc/`) are
untouched — they're agent-produced artifacts that run under Node because the
*targets* are Node apps; the tool itself no longer requires Node.

Reference: the current Node source is the source of truth for behavior:
`cli/bin/ckb-whitehat.mjs` (481 lines) and `cli/bin/compare-runs.mjs` (74
lines), as shipped by the merged audit-fix PR. The port must preserve the
fixes that PR landed: cwd isolation, per-agent flag sets, exit-code
semantics, and the `FINDING <id>` verdict-line checks.

---

## 1. Goals and non-goals

**Goals**
- One static binary, stdlib-only (zero external Go modules): `ckb-whitehat`.
- URL targets clone into managed disposable storage; dependency installation
  is explicit opt-in via `--install-deps`, never an implicit lifecycle-script
  execution.
- Byte-for-byte compatible behavior with the Node CLI for every documented
  flag, env var, exit code, and console output contract.
- One hash per platform to verify; no package-manager supply chain.
- Install paths: prebuilt binaries (GitHub Releases) + `install.sh`;
  `go build ./cmd/ckb-whitehat` for development.
- `compare-runs.mjs` folded in as a `compare` subcommand.

**Non-goals**
- No change to `.claude/skills/ckb-whitehat/SKILL.md` or the PoC suites.
- No change to how the agent is invoked conceptually (we still shell out to
  `claude -p` / `codex exec`; the binary carries its own auth — we never
  ask for an API key).
- No cross-target-language generalization: PoCs are still written by the
  agent in whatever language the target runs.

## 2. Repo layout

```
go.mod                    module github.com/nghiadang391/ckb-whitehat, go 1.22
cmd/ckb-whitehat/main.go  flag parsing + subcommand dispatch
internal/workspace/       slug, legacy fallback, config.json, init/status/report
internal/agent/          agent detection, per-agent arg sets, spawn + stall/timeout
internal/detect/         pg_isready / FNN node_info probing
internal/verify/         marker + FINDING verdict-line checks (verifyRun)
internal/compare/        report diff (coverage / novelty / sections)
install.sh                curl | sh installer (downloads latest release for the platform)
.github/workflows/release.yml   multi-platform builds, sha256sums, tag-driven releases
README.md                 install section rewritten (Go-only)
cli/                      deleted
```

Go module path: `github.com/nghiadang391/ckb-whitehat` (matches the remote).
`go.mod` has **no require block** — stdlib only (`os/exec`, `net/http`,
`os/signal`, `regexp`, `encoding/json`).

## 3. Commands and exit codes (unchanged contract)

```
ckb-whitehat init [dir] [--target DIR] [--scope app,chain] [--chain]
ckb-whitehat run  [dir] [--target DIR] [--scope …] [--agent claude|codex]
                  [--allowed-tools LIST] [--dangerously-skip-permissions | --yolo]
                  [--stall-timeout SECS] [--max-runtime SECS]
ckb-whitehat status [dir] [--target DIR]
ckb-whitehat report [dir] [--target DIR]
ckb-whitehat compare <new-report.md> <baseline-report.md>
ckb-whitehat version | help
```

Exit codes: **0** ok (including a clean run with `findings=0 fired=0`),
**1** findings fired / run error / missing workspace or marker / incomplete
run, **2** bad usage / no agent found / unknown agent. `compare` exits 0
always (reporting tool, not a gate) and 2 on usage error.

## 4. Behavior port, item by item

### 4.1 Workspace layout (`internal/workspace`)

- Slug: `path.resolve(target)` → replace every run of non-`[a-zA-Z0-9]`
  with `-`, trim leading/trailing `-`, lowercase. Empty → `root`.
  Directory: `$HOME/.ckb-whitehat/<slug>`.
- **Legacy fallback** (must keep — existing workspaces on user machines
  predate the slug change): if the slug dir does not exist and
  `$HOME/.ckb-whitehat/<basename(resolved)>` does, use the legacy dir.
- `init`: verifies target is a directory (else exit 2), warns to stderr if
  it lacks `.git` ("findings cannot be diffed against a baseline"), creates
  `poc/` subdirectory, writes `config.json` (`version`, `target`,
  `created_at` RFC3339, `scope[]`, `onchain`, `services[]`) and a
  placeholder `FINDINGS.md`. `onchain = hasChainFlag || "chain" in scope`.
  Scope parsing: comma-split, trim, drop empties. Prints the three
  `initialized workspace / target / scope / next` lines.
- `status`: prints workspace, target, scope, onchain, services
  (joined by ` | `, or `(none detected)`), poc file count, report path;
  exit 1 + "no workspace for <target>" if absent.
- `report`: cats `FINDINGS.md` to stdout; exit 1 + "no report yet" if absent.

### 4.2 Agent detection and arg sets (`internal/agent`)

Detection replaces the Node `execFileSync('which', …)` with
`exec.LookPath` (portable; no dependency on `which`):

| agent | base args | `--allowedTools` | claude-only flags |
|---|---|---|---|
| `claude` | `-p` | yes, appended as `--allowedTools <csv>` | `--permission-mode acceptEdits --add-dir <target> --output-format text` |
| `codex` | `exec` | **no** | **none** |

Order of assembly (Node parity): start with base args; if
`--dangerously-skip-permissions`/`--yolo`, unshift the blanket flag; append
`--allowedTools`; append claude-only flags; append the prompt as the last
arg. `--agent` selects by name among *detected* agents; unknown → exit 2.
None detected → exit 2 with the "no agent CLI found … we never ask for an
API key" message.

> **Open item O1:** the Node code passes `--dangerously-skip-permissions`
> verbatim to `codex exec`, which does not accept that flag — codex's
> equivalent is `--dangerously-bypass-approvals-and-sandbox` (verify against
> the user's codex version; the fix is a per-agent mapping table).

### 4.3 Skill resolution in the prompt

Node resolved `SKILL.md` relative to the *script's* location
(`bin/../../.claude/skills/ckb-whitehat/SKILL.md`) — an artifact of running
the CLI from inside the repo. A installed binary has no repo root, so:

```
skill = first existing of:
  $CKB_WHITEHAT_SKILL                          (explicit override)
  $HOME/.claude/skills/ckb-whitehat/SKILL.md   (installed skill — the
  $HOME/.codex/skills/ckb-whitehat/SKILL.md       documented install path)
  builtin embedded text                       (go:embed the SKILL.md so a
                                                bare download still works)
```

Embedding the skill via `go:embed` (shipped from the repo copy at build
time) means the binary works standalone; the home-dir copies win when
present so users can override. Prompt text, hard rules 1–6, and the
`CKB-WHITEHAT-RESULT: findings=<n> fired=<n> report=<path>` final-line
contract: identical to today, generated from the same template.

### 4.4 Permissions, timeouts, env

- `allowedTools`: `--allowed-tools` flag > `CKB_WHITEHAT_ALLOWED_TOOLS`
  env > default list `Bash(node:*),Bash(pg_isready:*),Read,Write,Edit,Grep,Glob,LS`
  (curl/psql deliberately excluded — documented in help). Empty string =
  allow nothing.
- `stall-timeout` default 300, `max-runtime` default 3600; flag > env
  (`CKB_WHITEHAT_STALL_TIMEOUT` / `CKB_WHITEHAT_MAX_RUNTIME`) > default;
  non-positive or unparsable → default.
- Child env: full inheritance + `CKB_WHITEHAT_WORKSPACE` +
  `CKB_WHITEHAT_TARGET`.
- **cwd = the workspace, never the target** — the core audit fix. Keep the
  in-code comment explaining why (an interpreter started in an untrusted
  clone loads its settings/hooks/node_modules).

### 4.5 Spawn, stall, and wall-clock cap

Node's `spawnAgent` semantics to reproduce:
- child stdio: stdin ignored, stdout/stderr piped and captured.
- process group (`detached: true` / `Setpgid`) so the kill reaches the
  agent's children: `syscall.Kill(-pid, SIGKILL)` — Go equivalent is
  `cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}` and
  `syscall.Kill(-proc.Pid, syscall.SIGKILL)` on both paths.
- any output byte resets the stall timer; no output for `stallTimeout` →
  kill, `stalled=true`. Elapsed > `maxRuntime` → kill, `timedOut=true`.
  (Timer at 1s granularity, or `time.After` loop — behavior: first
  violation wins; `timedOut` takes precedence in reporting.)
- after a natural exit, allow stdio to drain (Node: 2s fallback);
  Go's `cmd.Wait()` closes the pipes, so `WaitDelay` (Go ≥ 1.20) is not
  needed — spec the same *observable* behavior: no output is lost.
- `run` writes `<ws>/run.log` = stdout + `"\n--- stderr ---\n" + stderr`
  (stderr section only when non-empty) and echoes the last 40 stdout lines.

### 4.6 Result marker + completeness (`internal/verify`)

- Last match of
  `CKB-WHITEHAT-RESULT: findings=(\d+) fired=(\d+) report=(.+)` in
  stdout (the agent may print intermediate markers). No marker → exit 1,
  "(no CKB-WHITEHAT-RESULT marker found …)".
- `findings > 0 || fired > 0` → exit 1 ("1 finding/error" is by design —
  a run that found anything is non-zero; document this in help).
- Completeness checks (printed as `ok     <what>` / `MISSING <what>`):
  always: report exists and > 200 chars; marker printed.
  **only when the marker reports findings > 0**: ≥ 1 non-hidden regular
  file in `<ws>/poc/`; ≥ 1 stdout line matching
  `^FINDING\s+(\S+)\s+(FIRED|UNPROVEN|STRUCTURAL)\b` (multiline);
  ≥ 1 same-pattern line in the report. Any MISSING → exit 1.
  Go note: same regex, but the `findings=0` clean-run path must skip the
  PoC/verdict requirements (a clean run proves nothing needs proving).

### 4.7 Service auto-detection (`internal/detect`)

Run-phase only; results persist into `config.json` and the prompt's
"Local services already running" block:
- `pg_isready -h 127.0.0.1 -p 5432|5433|5434` (exec, first "accepting
  connections" wins and stops).
- `POST http://127.0.0.1:8227|8327` JSON-RPC `node_info`, 1.5s timeout;
  report `v<result.version>` when present.
- Both probe failures are silent (service simply not listed).

### 4.8 `compare` subcommand (`internal/compare`)

Port of `compare-runs.mjs`, input `<new.md> <baseline.md>`, exit 0 (2 on
usage). All JS semantics are RE2-compatible, two care items:
- **Title slicing** `slice(0, 58)`/`slice(0, 70)` must be rune-based —
  titles contain em-dashes; byte-slicing would corrupt UTF-8.
- Finding-id regex `^#{2,3}\s+((?:F|S|C4)\d+(?:\.\d+)?)\s*[—-]\s*(.+)$`
  (multiline); trailing `(…)` stripped from titles; matching is by
  **content, not id** — keyword overlap = shared words (len > 3,
  lowercase alphanumeric runs) / min(|A|,|B|); a finding matches at
  overlap ≥ 0.45, or at ≥ 0.25 when ids are equal. Prints REDISCOVERED /
  MISSED / NEW tables, the four required sections check
  (`## At a glance`, `On-chain evidence`, `Layer boundary`,
  `Honest limitations`), and the coverage %.

## 5. Test plan (parity is the acceptance bar)

Unit (table-driven, no network):
1. slug + legacy fallback (incl. `~/a/app` vs `~/b/app` collision case and
   empty-slug → `root`).
2. flag parsing: `--name value` vs `--name=value`, VALUE_FLAGS
   consumption, bare `--allowed-tools` → default.
3. agent arg assembly: exact argv for claude and codex, with and without
   `--allowedTools`, with blanket flag — golden tests against the Node
   CLI's emitted argv (recorded by running the Node build under a
   `PATH`-shadowed fake agent that prints its args).
4. marker: last-of-many wins; `findings=0` path skips PoC/verdict checks;
   each MISSING check individually forces exit 1.
5. compare: threshold cases (0.45 / 0.25 boundaries), em-dash titles
   (rune slicing), section presence.
6. stall/timeout: fake agent = `bash -c 'sleep 300'` with `--stall-timeout
   2` → stalled, exit 1; `--max-runtime 3` on a long sleep → timedOut.
   Integration test with the real binary, 5–10s runtime.
7. exit-code matrix: every (condition → code) row of §3.

Parity sweep (one-off, before deleting the Node CLI): run both binaries
against the same three targets (dular, tapfiber, a trivial fixture repo)
with fake/real agents and `diff` the stdout, exit codes, `run.log`,
`config.json`, and `FINDINGS.md` verdict sections. Record in the PR.

## 6. Release

- `.github/workflows/release.yml`: on tag `v*` — matrix
  linux/darwin/windows × amd64/arm64,
  `go build -ldflags "-s -w"` → `ckb-whitehat-<os>-<arch>` (+ `.exe`),
  `sha256sums.txt` artifact + pinned release assets.
- `install.sh`: detects os/arch, downloads, verifies sha256, installs to
  `$HOME/.local/bin` (or asks for `sudo` on system paths).
- `go build ./cmd/ckb-whitehat` remains the dev path; no npm anywhere.

## 7. Migration and deletion

**PR 3 — "add the Go CLI (parity)"**: `go.mod`, `cmd/`, `internal/`,
tests, release workflow, `install.sh`; `cli/` (Node) still present.
README documents both, Go marked the recommended path.

**PR 4 — "Go-only"**: delete `cli/` (both `.mjs` files, `package.json`,
the `npm link` instructions), README install section becomes Go-only,
root README's "The CLI" section updated. `compare-runs.mjs`'s only
non-subcommand reference (docs) is removed. Nothing else in the repo
imports the Node CLI.

## 8. Decisions (resolved with the user)

- **O1 (codex blanket flag)** — per-agent mapping table: `claude` →
  `--dangerously-skip-permissions`; `codex` →
  `--dangerously-bypass-approvals-and-sandbox`. Verify the codex spelling
  against the user's installed binary at build time (spec item, not a
  blocker — the checkpoint design below does not depend on it).
- **O2 (skill location)** — user decision: like no-mistakes, **all logic
  lives in the binary**. SKILL.md is embedded via `go:embed`; a bare
  download works standalone. `$CKB_WHITEHAT_SKILL` remains as an
  override. The `.md` in the repo is documentation/install artifact
  only — the agent (guided by the embedded skill text) calls the
  binary; the binary never parses .md.
- **O3 (install.sh dest)** — `~/.local/bin`.

## 9. URL targets: auto-clone into a managed directory

`--target` accepts a URL in addition to a local path:

- Detection: `http(s)://`, `git@`, `ssh://` → URL; otherwise a local
  path (behavior identical to today).
- Clone dir: `~/.ckb-whitehat/repos/<repo-slug>` (slug from the URL's
  basename). Stable path ⇒ the existing workspace slug (keyed on the
  resolved target path) stays stable; no workspace-keying change.
- History: full clone by default (the skill diffs findings against a
  baseline); `--depth N` opt-in. `--fresh` removes and re-clones.
- Dependencies are **not installed by default**: package-manager lifecycle
  scripts execute arbitrary target code. `--install-deps` explicitly opts in
  (use `npm ci` when a lockfile exists, otherwise `npm install`); print a
  loud warning. `--no-install` is accepted as an explicit no-op. The clone
  is disposable tool-owned state, never the user's working tree.
- `config.json` gains `target_origin` (`local`|`clone`), `clone_url`,
  `commit` (HEAD sha at clone time); `status` prints them.
- New subcommand `clean`: remove `~/.ckb-whitehat/repos/*`
  (`--workspaces` extends to the slug workspaces).
- Agent hard rules unchanged: target content is untrusted; `--add-dir`
  points at the clone dir; the agent's cwd remains the workspace.

## 10. Interactive gates (opt-in; unattended is the default)

Today the tool never pauses: default allowlist + 300s stall-kill means
a blocked prompt ends the run. Add no-mistakes-style **defined gates**
via `run --interactive` (off by default — unattended output is
byte-identical to today):

- Hard constraint: the CLI only presents **its own** skill-defined
  decision points, never free-form agent text — the target repo is
  hostile content, and "the agent asked me something" is a
  prompt-injection channel.
- Gates: (1) **triage** — approve / add / drop / stop the hypothesis
  list, rendered by the CLI; (2) **permission** — agent requests an
  escalated tool (e.g. live `curl` probe): allow-once / deny;
  (3) **publish** — before any outward-facing step, when added.
- Mechanics: agent persists state to the workspace and ends the turn
  with `CKB-WHITEHAT-GATE: <gate-id> …`; the CLI suspends the stall
  timer, prompts on the TTY, records the answer in
  `<ws>/decisions.json`, and resumes. Primary resume = **checkpoint
  re-spawn** (fresh prompt + decisions + prior state; no dependency on
  agent session semantics); optimization = `claude --resume <sid>`
  where available.
- CI / non-TTY: `--auto-skip-gates` (default policy **skip = deny** —
  the safe answer).
- The wall-clock kill still bounds everything outside an active gate.

## 11. Remaining open items

- Verify `claude -p --resume` / codex session support (checkpoint
  re-spawn is the fallback, so nothing is blocked on it).
- Codex blanket-flag spelling confirmed against the user's codex.
- Clone depth default (spec says full; `--depth` overrides) and
  `npm ci` vs `npm install` fallback order.

## 12. What deliberately does NOT move

- `.claude/skills/ckb-whitehat/SKILL.md` (the methodology; language-agnostic).
- `dular/poc/`, `dular/agent-run/`, `tapfiber/poc/` and their FINDINGS.md
  reports — agent artifacts that run on the target's own runtime.
- `dular/poc/fixed-ledger.js` (CommonJS, required by the target's module
  system).
- All recorded on-chain evidence (`c4-record.json`, FNN logs).
