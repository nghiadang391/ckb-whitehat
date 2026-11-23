# ckb-whitehat

Authorized security challenges of CKB / Fiber (FNN) projects: AI-assisted triage
→ named attack hypotheses → deterministic, replayable PoCs → per-finding patch
guidance. Findings terminate in machine-checkable state (a DB balance, a
forged input that lands a bad state, a real testnet tx hash) — reproducible
with one command against unmodified source.

Method: **"AI finds, the PoC proves."** No finding ships without a
deterministic artifact.

## Contents

| Target | Layer | PoC command | Report |
|---|---|---|---|
| [dular](dular/) — M-Pesa STK-push → RUSD on-ramp over Fiber | app/DB/config (F1–F9) + on-chain C4.1–C4.5 | `node dular/poc/<finding>.mjs` | [dular/FINDINGS.md](dular/FINDINGS.md) |
| [tapfiber](tapfiber/) — tap-to-pay order/receipt library (pure logic) | offline only (S1–S7) | `node tapfiber/poc/attack.mjs` | [tapfiber/FINDINGS.md](tapfiber/FINDINGS.md) |

## The method (installable skill)

`.claude/skills/ckb-whitehat/SKILL.md` — the generalized methodology: scope &
consent rules, the triage -> hypothesis -> PoC -> patch workflow, token-efficiency
rules learned from real runs, the PoC quality bar (assert observable state, not
string-greps of your own source), a CKB/Fiber cheatsheet, and the FINDINGS.md
template.

Install it into your own Claude Code setup:

```sh
git clone https://github.com/nghiadang391/ckb-whitehat.git
cp -r ckb-whitehat/.claude/skills/ckb-whitehat ~/.claude/skills/
```

Then `/ckb-whitehat` runs a new challenge, and the skill applies to any
project with money, state, auth, or chain interaction — CKB/Fiber is the anchor
lane, not a hard requirement.

## The CLI

A single static Go binary — no Node, no npm, no package-manager supply
chain; one hash per platform to verify. The skill text is embedded in the
binary, so a bare download works standalone.

Install (Linux / macOS, amd64 / arm64):

```sh
curl -fsSL https://raw.githubusercontent.com/nghiadang391/ckb-whitehat/main/install.sh | sh
# -> $HOME/.local/bin/ckb-whitehat
```

or build from source:

```sh
go build -o ckb-whitehat ./cmd/ckb-whitehat
```

Use it:

```sh
ckb-whitehat init ~/src/target-repo --scope app,chain
ckb-whitehat run --target ~/src/target-repo --chain
ckb-whitehat chain status   # or chain up / chain down
ckb-whitehat report --target ~/src/target-repo
ckb-whitehat compare new-report.md baseline-report.md
```

### Local CKB/Fiber Devnet Harness & On-Chain Verification

Passing `--chain` (or `--scope app,chain`) to `init` or `run` **automatically
starts** a self-contained local Docker CKB devnet (`127.0.0.1:8114`) + 2 peered
Fiber (FNN) nodes (`127.0.0.1:8227` / `8327`) with pre-funded **CKB and
`TEST_XUDT` channels** — **zero pre-funded testnet addresses or faucets
required**. Use `ckb-whitehat chain status` / `chain up` / `chain down` anytime
for manual lifecycle control.

- **Mainnet refusal guard:** `--chain` checks CKB `get_blockchain_info` and FNN
  `chain_hash` up front and hard-refuses if pointed at CKB mainnet.
- **Strict post-run RPC verification:** When `--chain` is active and findings
  fire, `ckb-whitehat` independently re-queries every reported CKB tx hash and
  Fiber payment hash against the local RPC so hallucinated hashes cannot pass
  completeness.
- **Reusable CKB/Fiber invariant packs:** Materialized into
  `<workspace>/packs/` (`chain-client.mjs`, `pack-payment-idempotency.mjs`,
  `pack-hold-invoice-htlc.mjs`, `pack-udt-decimals.mjs`, `pack-self-custody.mjs`).
- **Rust CKB-script support:** When `init` detects a Rust / `Cargo.toml` target,
  `run` automatically enables `Bash(cargo:*)` alongside Node PoCs (still keeping
  `curl`, `wget`, `psql`, and `env` blocked).

It drives your local agent CLI
(`claude -p --verbose --output-format stream-json --permission-mode dontAsk --setting-sources user`
or `codex exec --full-auto --json`) through the challenge and writes
`FINDINGS.md` to a workspace outside the target repo. Structured JSONL events
stream in real time as the agent runs tools and writes findings, and under
Claude's `--permission-mode dontAsk` any unapproved tool fails immediately
with a visible error instead of silently deadlocking the agent. Like
no-mistakes, it **never asks for an API key** — the agent binary carries its
own auth.

On a terminal, `run` renders a responsive `no-mistakes`-inspired live TUI
while the agent works: rounded-border **Pipeline** and **Log** boxes placed
side-by-side in a 2-column layout on wide terminals (`>= 100` columns) and
stacked vertically on narrow terminals, reflowing live on terminal resize
with strict zero-wrap truncation so long lines never wrap or flood the
terminal. The **Pipeline** box tracks the spinner, elapsed time, **idle
seconds since the last event** (your "is it stuck?" signal), current stage
(triage → hypotheses → poc → firing → report), and checkpoint round; the
**Log** box tails live tool and assistant activity. If no agent events arrive
for more than half of `--stall-timeout`, the panel displays a **stall
warning** before the run is killed. When `--interactive` is on and the agent
raises a checkpoint, the panel switches to a checkpoint question box with
one-key choices (`a` approve / `s` skip / `d` deny / `x` stop). Piped or
redirected output (CI, tests) stays on the plain text path — byte-identical
to before.

### Prerequisites

The binary itself is self-contained, but a run needs local tooling:

| Piece | Needed for | Without it |
|---|---|---|
| An agent CLI on PATH (`claude` or `codex`) | every `init` and `run` | `init` and `run` fail fast before starting, naming the missing binary |
| **Node** (18+) | running Node `.mjs` PoCs and embedded CKB/Fiber packs | Node PoCs cannot execute; static findings only |
| **Cargo / Rust** | Rust CKB-script targets (`Cargo.toml` / `ckb-testtool`) | Rust PoCs cannot compile/run; `init` flags `cargo` as missing |
| **Docker** | `--chain` local CKB devnet + 2-node FNN harness (pre-funded CKB & `TEST_XUDT` channels) **and** isolated scratch Postgres (`127.0.0.1:5439`, throwaway credentials) for Postgres targets — zero pre-funded testnet addresses or host DB passwords needed | `--chain` cannot auto-start devnet; DB PoCs require setting `CKB_WHITEHAT_POSTGRES_URL` manually |
| `CKB_WHITEHAT_POSTGRES_URL` *(optional)* | overriding the auto-provisioned Docker scratch Postgres with your own scratch DB | Docker auto-provisions an isolated `postgres:16-alpine` container automatically |

`init` fails fast if no agent CLI (`claude` or `codex`) is on PATH, then runs
the agent in **strictly read-only mode** (`Read,Glob,Grep,LS` — no `Bash`, no
`Write`) to inspect the target repo's stack (JS/TS, Rust, Go, Python,
Solidity, monorepos, etc.). It checks host prerequisites (Node, Postgres, FNN,
and any extra tools discovered in the target), displays each suggested target
dependency-install command (preferring safe `--ignore-scripts` for JS/TS by
default, with `--allow-scripts` opt-in for native addons) with risk warnings
for individual `[a]pprove / [d]eny / [s]kip all` confirmation on a TTY
(auto-denied in non-TTY / CI runs or with `--no-install-deps`; external local
directories require `--install-deps` before prompting), and saves the ground-
truth result to `<workspace>/setup.json` for `run`.

## Dular

- `dular/poc/f1…f8f9-*.mjs` — offline PoCs, each a one-command run against
  unmodified target source. The target is located by `DULAR_DIR`, which takes
  the **repository root** (default `/tmp/ckb-whitehat/dular2/repo`); a
  `server/` dir is also accepted (detected by the presence of `schema.sql`
  without a sibling `server/`). The PoCs' third-party deps (`pg`) resolve
  through the target's own `node_modules` — this directory has no
  package.json. DB-backed PoCs (f1, f2, f7) run against an isolated scratch
  database seeded from the target's `schema.sql` and drop it on teardown.
  Race PoC: `f2-ledger-race.mjs` (fires against unmodified source; Part C/D
  re-run `poc/fixed-ledger.js` to show the fix passes and stays atomic when
  nested inside an outer `withTransaction`).
- `dular/poc/c4-onchain.mjs`, `c4-openchannel2.mjs`, `c4-finalize2.mjs` —
  on-chain evidence on CKB testnet via two local FNN nodes
  (`127.0.0.1:8227/8327`). All three default to re-verifying the settled
  traces **by recorded hash** (no new RUSD moves; `get_transaction`
  confirms the funding tx, with an explicit error path — never a masked
  empty result). Fresh sends / the C4.5 probe are opt-in via
  `C4_FIRE=1` and move more RUSD between our own two nodes. Results are
  merged key-level into `dular/poc/c4-record.json` (historical entries
  survive re-runs); the funding tx payload is in `fund-tx2.json`.
  Impact confined to our own addresses and local services on a public
  testnet.

## tapfiber

Upstream source is vendored at `tapfiber/` (MIT, github.com/0xVida/tapfiber);
the PoC runs against it in-tree: `node tapfiber/poc/attack.mjs`.

## Security model

- Claude runs with
  `claude -p --verbose --output-format stream-json --permission-mode dontAsk --setting-sources user`
  and the default offline tool allowlist (`Bash(node:*)`,
  `Bash(pg_isready:*)`, `Bash(git:*)`, `Bash(grep:*)`, `Bash(ls:*)`,
  `Bash(mkdir:*)`, `Bash(cd:*)`, plus `Bash(cargo:*)` automatically on Rust /
  CKB-script targets, and `Read`, `Write`, `Edit`, `Grep`, `Glob`, `LS` —
  deliberately excluding `curl`, `wget`, `psql`, and command launchers like
  `env`). Unapproved tools fail
  immediately instead of silently blocking until stall timeout, and
  `--setting-sources user` prevents project-local `.claude/` settings in an
  untrusted target repo from overriding permissions or hooks. During `init`,
  inspection runs strictly read-only (`Read,Glob,Grep,LS` for Claude;
  `--sandbox read-only` for Codex), and any suggested setup commands are
  confined inside the target directory, scanned for shell/injection risks, and
  require explicit per-command approval on a TTY. Codex runs with
  `codex exec --full-auto --json` and ignores `--allowed-tools` entirely (the
  CLI says so explicitly instead of implying enforcement).
- **Mainnet refusal & strict on-chain verification**: `--chain` checks CKB
  `get_blockchain_info` and FNN `chain_hash` before running and hard-refuses
  CKB mainnet; post-run completeness independently re-verifies every reported
  tx/payment/channel hash against the local devnet CKB/FNN RPC.
- **Allowlists are still prompt/CLI level, not a sandbox**: `Bash(node:*)`
  can reach the network and filesystem.
- The target is treated as untrusted *content* — the skill instructs the
  agent to never execute code from the target tree, and PoCs run in the
  workspace — but there is **no OS-level isolation boundary** between the
  agent/PoC process and your host. Run against untrusted targets in a
  disposable VM or container (e.g. `docker --network=none`), or a
  compromised/broken PoC is one `node -e` away from touching your machine.
- Checkpoint (gate) lines and agent output are sanitized before reaching
  the terminal; non-interactive runs never respawn on gate lines.
- `git clone` of URL targets passes hardened flags
  (`-c core.hooksPath=/dev/null -c core.fsmonitor=false -c core.symlinks=false`)
  so a hostile repo can't run hooks or plant fsmonitor binaries during clone.

## Disclosure

Findings in these reports are **unpatched observations against public code**.
This repo shares them openly because it is a demonstration of the
whitehat method (audit C1): the PoCs are kept non-exploitable on the
target's live systems — they run against local instances, mocked vendors,
and our own testnet addresses only — but the exploit mechanics and the
pointers to unpatched targets (including paths to PII/OTP material inside
the target repos) are visible here. The intended flow is: report the
maintainer privately with a patch window, obtain consent, then keep these
public. Until that consent is in place, treat the target-repo findings in
`dular/` and `tapfiber/` as disclosure-pending, not public knowledge, and
do not act on them. On-chain traces used only our own testnet addresses,
faucet asset, and local services.
