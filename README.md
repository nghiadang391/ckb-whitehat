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
ckb-whitehat run --target ~/src/target-repo
ckb-whitehat report --target ~/src/target-repo
ckb-whitehat compare new-report.md baseline-report.md
```

It drives your local agent CLI (`claude -p` or `codex exec`) through the
challenge and writes `FINDINGS.md` to a workspace outside the target repo.
Like no-mistakes, it **never asks for an API key** — the agent binary
carries its own auth.

On a terminal, `run` shows a live **box** while the agent works: a header
row, a status row (spinner, elapsed time, **idle seconds since the last
output line** — your "is it stuck?" signal, the current stage
(triage → hypotheses → poc → firing → report), the round), and a lower
panel that shows the agent's last few lines. If the agent goes quiet for
more than half of `--stall-timeout`, the lower panel flips to a
**stall warning** so you see *why* it's silent ("likely blocked on a
permission prompt — widen the allowlist") before the run is killed. When
`--interactive` is on and the agent raises a checkpoint, the lower panel
becomes the question with one-key choices
(`a` approve / `s` skip / `d` deny / `x` stop). Piped or redirected
output (CI, tests) is the plain text path — byte-identical to before.

### Prerequisites

The binary itself is self-contained, but a run needs local tooling:

| Piece | Needed for | Without it |
|---|---|---|
| An agent CLI on PATH (`claude` or `codex`) | every run | the run fails before starting; `run` names the missing binary |
| **Node** (18+) | running the agent-written PoCs (targets are Node apps) | PoCs cannot execute; static findings only |
| A scratch **Postgres** | DB-backed PoCs (ledger, callback, race classes) | those findings report "DB unavailable" in FINDINGS.md instead of firing |
| Two **Fiber (FNN) nodes** on a public testnet + our own addresses | the on-chain evidence layer (`--scope chain` / `--chain`) only | the run degrades to app-scope; on-chain section is N/A |

`init` checks what it can (node version, FNN node `node_info` on the
well-known local ports, Postgres via `pg_isready`) and prints a
`prerequisites:` block so you know before the run which layers will work.
It is informational, never blocking — init succeeds either way.

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

## Disclosure

Findings in these reports are unpatched observations against public code and
remain **private** until the maintainers' consent. On-chain traces used only
our own testnet addresses, faucet asset, and local services.
