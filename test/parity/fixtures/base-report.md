# ckb-whitehat — sample-target v1.0
Target: https://example.com/sample-target (MIT, library). No network, no live service.

## At a glance
| # | Finding | Sev | Status |
|---|---|---|---|
| **F1** | forged provider callback credits ledger | HIGH | PROVEN (fired) |
| **F2** | float amount parsing in payment path | MEDIUM | UNPROVEN |

## F1 — forged provider callback (HIGH) — PROVEN
PoC output:
FINDING F1 FIRED
Impact: ledger credit with zero vendor calls.
Patch: verify the callback signature.

## F2 — float amount parsing (MEDIUM) — UNPROVEN
PoC not executed — error: needs a funded address.
Impact: rounding drift on amounts.

## On-chain evidence
N/A — no on-chain interaction.

## Layer boundary
we tested the app, not the chain.

## Honest limitations
- single PoC class exercised
- F2 UNPROVEN: blocked on testnet funding
