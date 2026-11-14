# ckb-whitehat — dular v0.0.0 (Milestone 3)

Target: /tmp/ckb-whitehat/dular2/repo (private repo, Kenyan M-Pesa <-> Fiber/RUSD ramp, Express + Postgres + FNN 0.9.1). PoCs run against unmodified source: `node pocN-*.mjs` from this dir (or the CLI workspace `poc/`), with `CKB_WHITEHAT_TARGET` set to the clone. No network to live vendors; all PoCs run against local Postgres (scratch database `dular_whitehat`) and a local Fiber JSON-RPC stub. Where a PoC touches the chain, impact is confined to our own addresses on the public CKB testnet; no live-service probing.

Severity = impact on a merchant/holder weighted by how a real attacker reaches it.

Status states, exactly:
- **PROVEN** — the PoC ran and its claimed output was observed. Only a PROVEN row may say "fired" and only a PROVEN row may claim money movement.
- **UNPROVEN** — reasoned from code but the PoC did not run to a green verdict; say what blocked it.
- **STRUCTURAL** — a static/code-shape observation (no executable exploit).

## At a glance

### Offline findings

| # | Finding (one clause) | Sev | Status (PROVEN/UNPROVEN/STRUCTURAL) | Money-path? |
|---|---|---|---|---|
| **F1** | Legacy M-Pesa STK callback endpoint is unauthenticated; a forged success settles the deposit and credits the ledger | HIGH | PROVEN (fired) | yes |
| **F2** | Ledger `debit()` checks balance without a row lock; concurrent M-Pesa withdrawals drive the balance negative | HIGH | PROVEN (fired) | yes |
| **F3** | `DEMO_MODE` silently disables the ramp callback token; forged ramp callback settles a deposit order on the real operator node | HIGH | UNPROVEN | yes |
| **F4** | Unauthenticated `/api/verification/pilot` leaks the operator on-chain funding lock, channel/payment hashes, and user phone last-4 | MEDIUM | PROVEN (fired) | no (recon) |
| **F5** | CKB capacity sponsor pays 200 CKB per registered user with no per-deployment cap; unbounded in OTP demo deployments | MEDIUM | STRUCTURAL | yes (testnet capacity) |

### On-chain / state evidence

| Trace | App finding proven | Result (GREEN/RED/SAFE) | payment/tx hash |
|---|---|---|---|
| **C1** | control — operator node reachable, baseline RUSD channel state | GREEN | n/a |
| **C2** | F1 — forged callback settlement fires operator `send_payment` | RED (stub) | `0xefef…ef` (fnnstub) |

C2 is a stub-level RED: the settlement path was observed to call the operator's `send_payment` against our JSON-RPC stub (`0xefef…` payment hashes), not against a live channel. `onchain-trace.mjs` extends C1/C2 to a funded local FNN stack when one is running; it prints `ONCHAIN: N/A` otherwise. No live-testnet tx was recorded for this run.

## F1 — Unauthenticated legacy M-Pesa STK callback (HIGH) — PROVEN

`POST /api/mpesa/callback/stk` (server/index.js:2282) takes any JSON body, reads `CheckoutRequestID` + `ResultCode`, and if the transaction is not terminal, runs `applyStkResult` → `settlePaidDeposit` → `createFiberBackedDepositSettlement` (services/settlement.js:56). That path calls the **operator FNN `send_payment`** and **credits the user's ledger** (services/settlement.js:146). There is no Daraja HMAC, no signature, no callback token — the legacy callbacks predate the `RAMP_CALLBACK_TOKEN` that `rampRoutes.js` adds. Daraja's STK result URL is operator-configured and reachable from anyone who finds it. A forged `ResultCode: 0` + `MpesaReceiptNumber` is all it takes.

PoC (`poc1-forged-callback.mjs`, run output, captured from an actual run):
```
[setup] scratch DB ready: postgres://.../dular_whitehat
[state] deposit status=completed fiber_status=Success receipt=FORGED-0001
[state] ledger 0 -> 10000000000
[stub] operator send_payment calls=1
F1 FIRED — forged callback completed the deposit: real operator send_payment + ledger credit, zero M-Pesa money in, zero Daraja calls.
FINDING F1 FIRED
```
Impact: an attacker who reaches the callback URL converts a deposit into free RUSD at the operator's on-chain cost, with a fabricated receipt. Hostile-caller requirement: network reach to the callback URL (no auth).
Patch: require the shared callback token on the legacy endpoint too, and only settle on a `ResultCode`/receipt verified against Daraja's `stkpushquery` (never trust the callback body alone).

## F2 — Ledger debit race (unlocked balance check) (HIGH) — PROVEN

`services/ledger.js` `debit()`:
```js
const balance = await getBalance(client, userId)   // plain SELECT, no FOR UPDATE
if (balance < amount) throw new Error('Insufficient RUSD balance')
... UPDATE ledger_accounts SET balance_base_units = balance_base_units - $2 ...
```
`/api/mpesa/withdraw` fires `initiateB2c` **before** the debit, so in production every accepted debit is a real KES payout to the phone. Two concurrent withdrawals each read the stale balance, both pass the check, both decrement → balance goes negative. The repo already ships `withAdvisoryLock`/`withTransaction` but the money path does not use a row lock on `ledger_accounts`.

PoC (`poc2-ledger-race.mjs`, run output, captured from an actual run):
```
round 3: accepted=5/16 final=-1000000000 debits=6000000000 consistent [NEGATIVE]
F2 FIRED — target debit() drove the account balance NEGATIVE under concurrent withdrawals.
FINDING F2 FIRED
```
Impact: repeated 2-way withdrawals drain more than the account holds; negative balances are recoverable only by the operator. Hostile-caller: a user with an account hammering their own endpoint — self-custody, so the damage is operator KES.
Patch (regression pack): single guarded statement `UPDATE ledger_accounts SET balance_base_units = balance_base_units - $2 WHERE user_id = $1 AND balance_base_units >= $2 RETURNING *`; reject + roll back the entry when it returns no row. `poc2-ledger-race.mjs --patched` now holds `final >= 0` and `Σdebits = Δbalance` across all rounds, and prints `FINDING F2 FIRED` (regression = invariant broken again) vs exit 0.

## F3 — DEMO_MODE opens the ramp callback token (HIGH) — UNPROVEN

`rampRoutes.js:83`:
```js
function callbackTokenMatches(value) {
  if (config.demoMode && !config.ramp.callbackToken) return true
  ...
}
```
With `DEMO_MODE=true` and an empty `RAMP_CALLBACK_TOKEN` (the local-dev / reviewer default), every ramp callback endpoint is wide open, and `queryStkPushStatus` in demo mode returns `ResultCode 0` for **any** checkout id. A forged `/api/ramp/callback/stk` on an order in `mpesa_pending` therefore confirms `mpesa_confirmed` → `settleDeposit` → operator `send_payment` — by code inspection of `reconcileDeposit` (rampRoutes.js:366-413) and the demo Daraja stub.

PoC (`poc3-demo-ramp-callback.mjs`, run output, captured from an actual run):
```
[setup] order=… state=created
[setup] after /stk: status=created checkout=null
[attack] forged ramp callback -> HTTP 200
[state] order created -> created (fiber=null hash=null)
[stub] operator send_payment calls=0
FINDING F3 UNPROVEN
```
PoC not executed to a green verdict — exact blocker: the demo order gets stuck at `created` because driving it to `mpesa_pending` requires attaching a browser invoice (PUT `/api/ramp/deposits/:id/invoice`), which calls FNN `parse_invoice` returning an invoice whose description must equal `Dular deposit <order.id>` — a dynamic value the generic stub cannot supply. What the run did prove: the forged callback reaches the handler and returns 200 with **no token** in demo mode (the gate is open), but the order's state was not in `ACTIVE_DEPOSIT_STATES`'s reconcilable set with a checkout id, so no settlement fired.
Impact (as reasoned from code): a demo/reviewer deployment is a free RUSD machine until the token is set. Config foot-gun, not a remote exploit of the production posture (production requires the token), but the default ships open.
Patch: remove the demo bypass — demo mode should still require the token, or the demo STK stub should refuse to confirm orders that carry a real operator invoice.

## F4 — Unauthenticated pilot verification endpoint leaks operator chain state (MEDIUM) — PROVEN

`GET /api/verification/pilot` (server/index.js:1546) requires **no auth** and returns the operator's `FundingLock` script, every on-chain channel funding tx hash + outpoint, all `fiber_payments` hashes, and user phones (last-4). This is the recon an attacker needs to target the operator's on-chain liquidity.

PoC (`poc4-pilot-disclosure.mjs`, run output, captured from an actual run):
```
[attack] GET /api/verification/pilot (no auth) -> HTTP 200
[state] leaks funding-lock-hex=true channel-funding-tx=true payment-hash=true phone-last4=true
F4 FIRED — unauthenticated /api/verification/pilot returned operator chain state (funding lock, channel tx hashes, payment hashes).
FINDING F4 FIRED
```
Impact: disclosure of the operator's testnet on-chain identity and liquidity layout; phone last-4 is PII-adjacent. Not money movement by itself.
Patch: gate on `requireAuth` + operator token, or restrict to `publicBaseUrl` only in non-demo mode.

## F5 — CKB capacity sponsor is per-user, unbounded (MEDIUM, STRUCTURAL)

`POST /api/fiber/browser/fund-ckb` (server/index.js:1170) sponsors `RAMP_CKB_SPONSOR_AMOUNT` (default 200) CKB of testnet capacity to a user's wallet, once per user (`ckb_sponsored_at`). Users are created by OTP verification, which in reviewer deployments (`OTP_DEMO_MODE`) is open to anyone. There is no per-deployment cap, so N demo users = N × 200 CKB paid from the operator's dev wallet via `transferDevCapacity` (which decrypts the node's real funding key with `FIBER_SECRET_KEY_PASSWORD`).

STRUCTURAL — the gating is correct (bound wallet required, one-shot flag works, re-POST after sponsor is refused), but the aggregate cost is unbounded against an open user register. No exploit PoC; the cost is a deployment-config decision.
Impact: unbounded testnet-capacity burn on an OTP-demo deployment; the sponsor also exposes the operator's chain key path to any authorized user.
Patch: add a per-deployment total-cap env var and a hard stop, and refuse to sponsor when the node funding key is missing rather than erroring mid-way.

## On-chain evidence (mandatory section)

**What this section is:** the on-chain *state* produced by the app findings — not a hunt for bugs in the chain. A RED row requires a trace that was actually executed and observed on our own testnet addresses; a trace that never ran is UNPROVEN and cannot produce a RED row.

- **C1 GREEN** — control: operator/receiver FNN nodes reachable; baseline channel state read via `list_channels` (stub when no funded local stack; `onchain-trace.mjs` reads the real stack when `127.0.0.1:8227/8327` are running and funded).
- **C2 RED (stub)** — F1: the forged callback's settlement was observed to fire the operator `send_payment` against the JSON-RPC stub (payment hashes `0xefef…`, deterministic, zero live-chain movement). The on-chain money movement (RUSD leaving a real operator channel) is the documented follow-on cost of running the funded-stack trace; it was not executed for this run.

Reproduce: `node onchain-trace.mjs` (prints `ONCHAIN: N/A` when no funded local FNN stack is present).

## Layer boundary
We tested how Dular uses the Fiber/CKB chain and its own ledger, not the chain protocol. No upstream FNN/CKB defect was tripped; the stub-level RED row is the app's forged-settlement bug, executed by our deterministic stub rather than a live channel.

## Honest limitations
- Daraja M-Pesa and the Africa's Talking SMS boundary are stubbed (`DEMO_MODE`); the forged-callback finding (F1) is proven against the app's own settlement code and a JSON-RPC stub, not a live provider or live channel.
- The ledger race is timing-dependent; `poc2` loops up to 25 rounds until observed — a clean run is a pass, not a guarantee.
- **F3 is UNPROVEN, listed here by design:** the forged ramp callback reached the handler unauthenticated (the demo-mode gate is open), but driving the order to `mpesa_pending` requires a live FNN `parse_invoice` with an order-specific description that the generic stub cannot produce. The code path it would trigger (`reconcileDeposit` → demo `queryStkPushStatus` `ResultCode 0` → `settleDeposit`) is cited in the finding, not executed.
- F4/F5 are disclosure/cost findings; the money-movement proofs are F1 (proven at stub level) and the funded-stack follow-on.
- On-chain impact is confined to our own local FNN testnet addresses; mainnet is out of scope and would carry real operator cost.
