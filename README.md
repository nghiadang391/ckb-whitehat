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
| [skill-methodology.md](skill-methodology.md) | the method, generalized | — | — |

## Dular

- `dular/poc/f1…f8f9-*.mjs` — offline PoCs. The app must be available at
  `DULAR_DIR` (default `/tmp/ckb-whitehat/dular/server`); set
  `DULAR_DIR=/path/to/dular/server` to point at a local clone. Race PoC:
  `f2-ledger-race.mjs` (fires against unmodified source; re-run with
  `poc/fixed-ledger.js` to show the fix passes).
- `dular/poc/c4-onchain.mjs`, `c4-openchannel2.mjs`, `c4-finalize2.mjs` —
  on-chain evidence on CKB testnet via two local FNN nodes
  (`127.0.0.1:8227/8327`). All results are recorded in
  `dular/poc/c4-record.json`; the funding tx payload is in `fund-tx2.json`.
  Impact confined to our own addresses and local services on a public testnet.

## tapfiber

Upstream source is vendored at `tapfiber/` (MIT, github.com/0xVida/tapfiber);
the PoC runs against it in-tree: `node tapfiber/poc/attack.mjs`.

## Disclosure

Findings in these reports are unpatched observations against public code and
remain **private** until the maintainers' consent. On-chain traces used only
our own testnet addresses, faucet asset, and local services.
