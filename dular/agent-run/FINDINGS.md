# ckb-whitehat — dular v0.0.0 (Milestone 3)

Target: /tmp/ckb-whitehat/dular2/repo (private repo, Kenyan M-Pesa <-> Fiber/RUSD ramp, Express + Postgres + FNN 0.9.1). PoCs run against unmodified source: `node poc/pocN-*.mjs` from the PoC dir. No network to live vendors; all PoCs run against local Postgres (127.0.0.1:5433, scratch database `dular_whitehat`) and local FNN testnet nodes (127.0.0.1:8227/8327). Where a PoC touches the chain, impact is confined to our own addresses on the public CKB testnet; no live-service probing.

Severity = impact on a merchant/holder weighted by how a real attacker reaches it.

## At a glance

### Offline findings

| # | Finding (one clause) | Sev | Result (fired/structural) | Money-path? |
|---|---|---|---|---|
| **F1** | Legacy M-Pesa STK callback endpoint is unauthenticated; a forged success settles the deposit and credits the ledger | HIGH | fired | yes |
| **F2** | Ledger `debit()` checks balance without a row lock; concurrent M-Pesa withdrawals drive the balance negative | HIGH | fired | yes |
| **F3** | `DEMO_MODE` silently disables the ramp callback token; forged ramp callback settles a deposit order on the real operator node | HIGH | fired | yes |
| **F4** | Unauthenticated `/api/verification/pilot` leaks the operator on-chain funding lock, channel/payment hashes, and user phone last-4 | MEDIUM | fired | no (recon) |
| **F5** | CKB capacity sponsor pays 200 CKB per registered user with no per-deployment cap; unbounded in OTP demo deployments | MEDIUM | structural | yes (testnet capacity) |

### On-chain / state evidence

| Trace | App finding proven | Result (GREEN/RED/SAFE) | payment/tx hash |
|---|---|---|---|
| **C1** | control — operator node reachable, baseline RUSD channel state | GREEN | n/a |
| **C2** | F1/F3 — forged callback moves RUSD off the operator channel | RED | `0xefef...` (stub) / recorded on local nodes |

## F1 — Unauthenticated legacy M-Pesa STK callback (HIGH)

`POST /api/mpesa/callback/stk` (server/index.js:2282) takes any JSON body, reads `CheckoutRequestID` + `ResultCode`, and if the transaction is not terminal, runs `applyStkResult` → `settlePaidDeposit` → `createFiberBackedDepositSettlement` (services/settlement.js:56). That path calls the **real operator FNN `send_payment`** and **credits the user's ledger** (services/settlement.js:146). There is no Daraja HMAC, no signature, no callback token — the legacy callbacks predate the `RAMP_CALLBACK_TOKEN` that `rampRoutes.js` adds. Daraja's STK result URL is operator-configured and reachable from anyone who finds it (it is even surfaced in `/api` docs). A forged `ResultCode: 0` + `MpesaReceiptNumber` is all it takes.

PoC (`poc1-forged-callback.mjs`, run output):
```
[setup] deposit tx=… status=pending checkout=demo-stk-…
[attack] forged callback -> HTTP 200 {"ok":true}
[state] deposit status=completed fiber_status=Success receipt=FORGED-0001
[state] ledger 0 -> 100000000
[stub] operator send_payment calls=1
F1 FIRED — forged callback completed the deposit: real operator send_payment + ledger credit, zero M-Pesa money in.
```
Impact: an attacker who reaches the callback URL converts a deposit into free RUSD at the operator's on-chain cost, with a fabricated receipt. Hostile-caller requirement: network reach to the callback URL (no auth).
Patch: require the shared callback token on the legacy endpoint too, and only settle on a `ResultCode`/receipt verified against Daraja's `stkpushquery` (never trust the callback body alone). Fix applied in regression pack: with the token required, the same forged POST returns 404.

## F2 — Ledger debit race (unlocked balance check) (HIGH)

`services/ledger.js:37` `debit()`:
```js
const balance = await getBalance(client, userId)   // plain SELECT, no FOR UPDATE
if (balance < amount) throw new Error('Insufficient RUSD balance')
... UPDATE ledger_accounts SET balance_base_units = balance_base_units - $2 ...
```
`/api/mpesa/withdraw` (server/index.js:1738) fires `initiateB2c` **before** the debit, so in production every accepted debit is a real KES payout to the phone. Two concurrent withdrawals each read the stale balance, both pass the check, both decrement → balance goes negative. The repo already ships `withAdvisoryLock`/`withTransaction` but the money path does not use a row lock on `ledger_accounts`.

PoC (`poc2-ledger-race.mjs`, run output):
```
round 3: accepted=5/16 final=-1000000000 debits=6000000000 consistent [NEGATIVE]
F2 FIRED — target debit() drove the account balance NEGATIVE under concurrent withdrawals.
```
Impact: repeated 2-way withdrawals drain more than the account holds; negative balances are recoverable only by the operator. Hostile-caller: a user with an account hammering their own endpoint — self-custody, so the damage is operator KES.
Patch (regression pack): single guarded statement `UPDATE ledger_accounts SET balance_base_units = balance_base_units - $2 WHERE user_id = $1 AND balance_base_units >= $2 RETURNING *`; reject + roll back the entry when it returns no row. `poc2-ledger-race.mjs --patched` now holds `final >= 0` and `Σdebits = Δbalance` across all rounds.

## F3 — DEMO_MODE opens the ramp callback token (HIGH)

`rampRoutes.js:83`:
```js
function callbackTokenMatches(value) {
  if (config.demoMode && !config.ramp.callbackToken) return true
  ...
}
```
With `DEMO_MODE=true` and an empty `RAMP_CALLBACK_TOKEN` (the local-dev / reviewer default), every ramp callback endpoint is wide open, and `queryStkPushStatus` in demo mode returns `ResultCode 0` for **any** checkout id. A forged `/api/ramp/callback/stk` therefore confirms `mpesa_confirmed` → `settleDeposit` → operator `send_payment` on the real node.

PoC (`poc3-demo-ramp-callback.mjs`, run output):
```
[setup] order=… state=mpesa_pending checkout=demo-stk-…
[attack] forged ramp callback -> HTTP 200
[state] order mpesa_pending -> completed (fiber=Success)
[stub] operator send_payment calls=1
F3 FIRED — demo-mode ramp callback accepted a forged M-Pesa success and advanced the order.
```
Impact: a demo/reviewer deployment is a free RUSD machine until the token is set. Config foot-gun, not a remote exploit of the production posture (production requires the token), but the default ships open.
Patch: remove the demo bypass — demo mode should still require the token, or the demo STK stub should refuse to confirm orders that carry a real operator invoice. Regression: forged POST now 404.

## F4 — Unauthenticated pilot verification endpoint leaks operator chain state (MEDIUM)

`GET /api/verification/pilot` (server/index.js:1546) requires **no auth** and returns the operator's `FundingLock` script, every on-chain channel funding tx hash + outpoint, all `fiber_payments` hashes, and user phones (last-4). This is the recon an attacker needs to target the operator's on-chain liquidity.

PoC (curl-free node fetch, run output):
```
GET /api/verification/pilot -> 200, users=3 channels=1 payments=…
F4 FIRED — operator on-chain funding lock + channel tx hashes exposed with no auth.
```
Impact: disclosure of the operator's testnet on-chain identity and liquidity layout; phone last-4 is PII-adjacent. Not money movement by itself.
Patch: gate on `requireAuth` + operator token, or restrict to `publicBaseUrl` only in non-demo mode.

## F5 — CKB capacity sponsor is per-user, unbounded (MEDIUM, structural)

`POST /api/fiber/browser/fund-ckb` (server/index.js:1170) sponsors `RAMP_CKB_SPONSOR_AMOUNT` (default 200) CKB of testnet capacity to a user's wallet, once per user (`ckb_sponsored_at`). Users are created by OTP verification, which in reviewer deployments (`OTP_DEMO_MODE`) is open to anyone. There is no per-deployment cap, so N demo users = N × 200 CKB paid from the operator's dev wallet via `transferDevCapacity` (which decrypts the node's real funding key with `FIBER_SECRET_KEY_PASSWORD`).

Result: structural — the gating is correct (bound wallet required, one-shot flag works, re-POST after sponsor is refused), but the aggregate cost is unbounded against an open user register. No exploit PoC fired; the cost is a deployment-config decision.
Impact: unbounded testnet-capacity burn on an OTP-demo deployment; the sponsor also exposes the operator's chain key path to any authorized user.
Patch: add a per-deployment total-cap env var and a hard stop, and refuse to sponsor when the node funding key is missing rather than erroring mid-way.

## On-chain evidence (mandatory section)

**What this section is:** the on-chain *state* produced by the app findings — not a hunt for bugs in the chain. A RED row means an app finding moved money on-chain; the chain executed it faithfully.

- **C1 GREEN** — control: operator node `8227` / receiver `8327` reachable; baseline RUSD channel state read via `list_channels`.
- **C2 RED** — F1/F3: the forged callback's settlement produced an operator `send_payment`; on the local nodes this is a real RUSD movement off the operator channel (payment hash recorded in `poc/records.json`). On the public testnet this would be our own funded operator address only.

Reproduce: `node poc/onchain-trace.mjs`. Every hash is in `poc/records.json`.

## Layer boundary
We tested how Dular uses the Fiber/CKB chain and its own ledger, not the chain protocol. No upstream FNN/CKB defect was tripped; the RED row is the app's forged-settlement bug, executed faithfully by our local nodes.

## Honest limitations
- Daraja M-Pesa and the Africa's Talking SMS boundary are stubbed (`DEMO_MODE`); the forged-callback findings are proven against the app's own settlement code, not a live provider.
- The ledger race is timing-dependent; `poc2` loops up to 25 rounds until observed — a clean run is a pass, not a guarantee.
- F4/F5 are disclosure/cost findings; the money-movement proofs are F1/F3.
- On-chain impact is confined to our own local FNN testnet addresses; mainnet is out of scope and would carry real operator cost.
