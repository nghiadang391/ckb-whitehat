# ckb-whitehat — sample-target v1.0
Target: https://example.com/sample-target (MIT, library). No network, no live service.

## At a glance
| # | Finding | Sev | Status |
|---|---|---|---|
| **F1** | forged provider callback credits ledger | HIGH | PROVEN (fired) |
| **S1** | demo-mode and production flags coexist | MEDIUM | STRUCTURAL |

## F1 — forged provider callback (HIGH) — PROVEN
PoC output:
FINDING F1 FIRED
Impact: ledger credit with zero vendor calls.
Patch: verify the callback signature.

## S1 — demo/production config matrix (MEDIUM) — STRUCTURAL
Static: the app accepts DEMO_MODE=true with NODE_ENV=production.
Impact: a misconfigured deployment runs demo bookkeeping in production.
Patch: reject the dangerous flag pair at boot.

## On-chain evidence
N/A — no on-chain interaction.

## Layer boundary
we tested the app, not the chain.

## Honest limitations
- S1 is STRUCTURAL, not an executed exploit
- the fuzz corpus was small
