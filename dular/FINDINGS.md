# ckb-whitehat — Dular (duongja/Dular)

Target: https://github.com/duongja/Dular — M-Pesa STK-push mobile-money wallet
over Fiber (FNN) + CKB testnet RUSD. PoCs run against **unmodified source** in
a local clone (`/tmp/ckb-whitehat/dular2/repo`) + local Postgres, with vendors
(M-Pesa/Daraja, Fiber RPC) mocked at their outbound boundaries. No request
ever left this machine toward dular's live Vercel/Railway deployment.

One command per finding: `node dular/poc/<finding>.mjs` (offline F1–F9) and
`node dular/poc/c4-openchannel2.mjs` + `node dular/poc/c4-finalize2.mjs`
(on-chain C4). `DULAR_DIR` points at the repo **root** (a `server/` dir is
also accepted and detected by `schema.sql` presence). Third-party deps (`pg`)
resolve through the target's own `node_modules`; DB-backed PoCs (F1, F2, F7)
run against a throwaway scratch database seeded from the target's
`schema.sql` and drop it on teardown, so no PoC row survives into a shared
database.

Severity = impact on a M-Pesa merchant/user, weighted by how a real attacker
reaches it. Status states: **PROVEN** — the PoC ran and its claimed output
was observed; only a PROVEN row claims money movement. **UNPROVEN** —
reasoned from code but the PoC did not run (none today — every finding
below has a captured run). **STRUCTURAL** — a static/code-shape observation
verified against the repo, no executable exploit.

---

## At a glance

Two layers: **offline** findings (app/DB/config — the code you can break on a
laptop) and the **on-chain** evidence (C4.x — the same findings proven
against a live RUSD channel on Dular's own testnet). Details for every row
follow in this file, in the same order.

### Offline findings (F1–F9) — app / DB / config layer

| # | Finding (one clause) | Sev | Status | Money-path? |
|---|---|---|---|---|
| **F1** | Forged M-Pesa STK callback credits the ledger / settles RUSD with zero vendor calls (legacy route needs no auth; ramp route bypasses the token in demo mode) | **CRITICAL** | PROVEN (fired) | yes |
| **F2** | Double-spend race in `ledger.js debit()` — read→guard→write with no lock; patched module + nesting-atomicity proof included | **HIGH** | PROVEN (fired) | yes |
| **F3** | Demo mode fabricates `ResultCode:0` success under a production M-Pesa config; no startup guard refuses the pair | **HIGH** | PROVEN (fired) | yes |
| **F4** | KES amount coercion at the real Daraja sink — 9/9 pathological inputs reach the M-Pesa wire (NaN, negative, zero, hex, exponent, fraction, oversize) | MEDIUM | PROVEN (fired) | yes |
| **F5** | Known `SESSION_SECRET` default is the live OTP salt — captured `(phone, hash)` brute-forced <1s offline via the target's own `hashOtp` | MEDIUM | PROVEN (fired) | yes |
| **F6** | Browser self-custody wallet: 4-digit PIN, no lockout; exfiltrated blob → owner's real Fiber/CKB keys recovered offline (the lockout/export absence is structural) | MEDIUM | PROVEN (crack ran) + STRUCTURAL (no lockout/export) | keys |
| **F7** | Wildcard CORS + global 400 handler leaking internal error strings | MEDIUM | PROVEN (fired) | no |
| **F8** | Un-redacted M-Pesa/OTP screenshots + Kenyan phone PII + `postgres://user:pass@` creds committed | MEDIUM | STRUCTURAL (inventory verified against the repo) | no |
| **F9** | `@nervosnetwork/fiber-js` — the browser key-holding module — pinned to a pre-release rc in the lockfile | LOW | STRUCTURAL (lockfile pin verified) | no |

### On-chain / state evidence — our 2-node FNN 0.9.1 stack, Dular's UDT + chain

| Trace | App finding proven | Result | payment/tx hash |
|---|---|---|---|
| **C4.1** | control | GREEN (re-verified by hash) | `0x76da2929…39f1` |
| **C4.2** | **F1** forged callback | RED (settled in the original run; re-verified by hash) | `0x9f22a21a…a4daba` |
| **C4.3** | **F2** double-settle | SAFE-AT-CHAIN (duplicate rejected) | send 1 `0x864781d3…5bd14` |
| **C4.4** | F1/F4 invoice guards | AMBER (attach-time only) | (app validator output) |
| **C4.5** | ops edge (auto-accept) | by-design guard held | (live fnn log) |

Money-moved artifacts are all in `dular/poc/c4-record.json`: user 10-RUSD
transfer `0x43f4d527…b0cb26d` → channel `0xcef4106e` funding tx
`0x72ed0d77…df27d3` (committed at block `0x15e1b32`, re-confirmed via
`get_transaction`). The 9.1 + 0.9 = 10 RUSD reconcile was exact at the
c4-finalize2 snapshot; balB has since moved (verification sends, opt-in
`C4_FIRE=1`), so the record carries both the snapshot and the live balances.

---

## F1 — Forged M-Pesa callback = free RUSD (CRITICAL, PROVEN)

**Code:** `server/index.js` legacy route `POST /api/mpesa/callback/stk`
(line 2282): gated **only** by the `LEGACY_MANAGED_WALLET_ENABLED` feature
flag — no token, no Daraja signature, no IP allowlist. `ResultCode: '0'`
→ `applyStkResult` → `settlePaidDeposit` → ledger credit of
`rusd_base_units`. The ramp version (`server/rampRoutes.js:1219`) **does**
verify a `token` query param (SHA-256 + `timingSafeEqual`), but:
- it is a **single static shared secret** in a URL query string, and
- `callbackTokenMatches` (line 84) has a demo-mode bypass:
  `if (config.demoMode && !config.ramp.callbackToken) return true`.
- Neither path verifies a Daraja **server-to-server signature** over the
  callback body; `checkout_request_id` (a time/phone-shaped id the app logs
  and stores in `provider_payload`) is the only lookup key.

**PoC (`f1-forged-callback.mjs`, captured from an actual run; exit 0):**

```
=== F1-A — forged legacy STK callback (no auth, no token, zero vendor calls) ===
forged callback POST -> HTTP 200 {"ok":true}
transaction state: pending -> completed
ledger credits: 0 -> 100000000 (1 RUSD to the user, 100 KES "paid")
vendor calls made: 0
F1-A FIRED: forged callback credited the ledger with zero M-Pesa money and zero vendor calls
control (unknown checkout id): credits still 100000000 (no new credit — the key is the id, not ResultCode)

=== F1-B — forged RAMP STK callback, demo-mode bypass (no token required) ===
forged ramp callback (no token) -> HTTP 200 {"ok":true}
order state: mpesa_pending -> completed (fiber_payment_hash=0xffffffffff… status=Success)
F1-B FIRED: order completed — operator sent RUSD to the attacker-supplied
browser_invoice with zero M-Pesa money and zero vendor calls

=== F1 verdict ===
F1-A legacy forgery: true    F1-B ramp demo-mode forgery: true
vendor calls made across the whole run: 0
FINDING F1 FIRED
```

**Impact:** an attacker who learns/guesses an in-flight `checkout_request_id`
(or an attacker who knows the deployment is demo-mode) POSTs a forged
`ResultCode: 0` and the user's deposit is "confirmed" and settled: the
operator's RUSD moves to the user's invoice while M-Pesa money never moved.
On the legacy path this is unconditional; on the ramp path it requires
`DEMO_MODE=true` without a `RAMP_CALLBACK_TOKEN` (the shipped `.env.example`
sets exactly that) or knowledge of the shared token.

**Patch:**
1. Kill the legacy callbacks: delete the `LEGACY_MANAGED_WALLET_ENABLED`
   routes (or make the flag a hard removal, not a toggle).
2. Replace the shared static token with per-deposit HMAC: Daraja signs the
   callback (it supports a custom `X-Daraja-Signature`-style header when
   configured) or verify the provider's TLS cert + a per-request nonce that
   Daraja generates; store `callbackSecret = random(32)` **per checkout**
   at STK initiation and require `HMAC(secret, body)` on the way back.
3. Delete the demo-mode bypass line; `DEMO_MODE` must never relax auth.

---

## F2 — Double-spend race in `ledger.js debit()` (HIGH, PROVEN)

**Code:** `server/services/ledger.js:37` — `SELECT balance` (no lock) →
guard → `INSERT ledger_entries` → `UPDATE balance = balance - amount`.
No transaction, no `FOR UPDATE`. The repo **already ships** the fix
primitives (`withTransaction`, `withAdvisoryLock` in `server/db.js`) and
uses them everywhere else (`rampRoutes.js`, `ussd.js`) — the money path in
`ledger.js` just doesn't. Call sites: `server/index.js:1763, 2250, 2257`,
`server/services/ussd.js:188, 195`, `server/services/settlement.js:146`.

**PoC (`f2-ledger-race.mjs`, captured from an actual run; exit 0).** Part A
is the deterministic proof; Part B the real-module race; the Part B re-run
against the patched module is the regression; **Part D proves the patch
stays atomic when `debit()` is called inside an outer `withTransaction` —
the way `rampRoutes.js`/`index.js` actually call it — and Part E is the
negative control showing the original flag-based helper
(`client.__inTransaction`, which the target's `withTransaction` never
sets) commits a second, independently-rollback transaction that survives
the outer rollback:**

```
=== F2-A — deterministic interleaving of debit() SQL (two clients, same funds) ===
both clients read balance = 1000 (each passes guard: 1000 >= 600)
entries inserted: 1 + 1 (both succeeded)
final balance = -200 (2 x 600 debits against 1000 of funds)
INVARIANT VIOLATED: two payments succeeded, balance negative

=== F2-B — UNFIXED ledger.js: 12 parallel real debit() calls, 600 each, balance 1000 ===
successful debits: 8 (max that fits in 1000 = 1), rejected: 4
final balance: -3800   sum of posted debits: 4800   funds available: 1000
INVARIANT VIOLATED: 8 debits posted, 4800 > 1000 — more money went out than existed

=== F2-B — FIXED ledger.js (withTransaction + FOR UPDATE): 12 parallel real debit() calls ===
successful debits: 1 (max that fits in 1000 = 1), rejected: 11
final balance: 400   sum of posted debits: 600
invariant held: only 1 debit fit, balance never negative

=== F2-D — FIXED ledger.js: throwing inner debit inside an outer withTransaction must roll back ===
outer transaction threw as designed: simulated failure AFTER the ledger mutation
balance before=1000 after=1000 | posted debits before=0 after=0
ATOMICITY HELD (FIXED ledger.js): the inner debit joined the outer transaction and was rolled back with it

=== F2-E — negative control: the original flag-based helper breaks nesting atomicity ===
outer threw=true | balance after outer rollback = 400 | posted debits = 600
EXPECTED — the flag-based helper committed its debit in a SECOND transaction that the outer rollback never undid
FINDING F2 FIRED — ledger.js debit() is racy; the patched module closes it and stays atomic when nested.
```

**Nesting rule (new):** the target calls `debit(client, …)` with the
*transaction's* client (e.g. `index.js` `withTransaction(async (client) =>
… debit(client, …)`). A patched helper that unconditionally opens its own
`withTransaction` would take a second pool connection and a second,
independently-committing transaction — pool starvation risk, and the inner
write survives an outer `ROLLBACK` (exactly what Part E demonstrates with
the original flag-based detection). `fixed-ledger.js` now detects
"in-transaction" via `client.getTransactionStatus()` (pg reports `'T'`)
and joins the caller's transaction instead.

**Impact:** two concurrent payments (`/api/payments/send-phone`, any
`debit` caller) both read the pre-deduction balance, both pass the guard,
both post → user's RUSD balance is negative; the ledger says "paid" for
money that was never there. `phone_payment` debits feed key-sends to other
users' Fiber wallets, so the loss propagates off-ledger.

**Patch (working, in `poc/fixed-ledger.js`):** wrap the critical section:

```js
return withTransaction(async (c) => {
  await ensureLedgerAccount(c, userId)
  const row = await c.query(
    'SELECT balance_base_units FROM ledger_accounts WHERE user_id = $1 FOR UPDATE',
    [userId])
  const balance = BigInt(row.rows[0]?.balance_base_units || '0')
  if (balance < amount) throw new Error('Insufficient RUSD balance')
  // … existing INSERT + UPDATE, unchanged …
})
```

with the nesting rule above. Re-running the identical harness against the
patched module holds the invariant (1 success, 11 rejected, balance 400)
and Part D/E verify atomicity under nesting. That is the regression pack:
fail before, pass after, in the same run.

---

## F3 — Demo mode fabricates provider success under a production config (HIGH, PROVEN)

**Code:** `server/services/daraja.js` — when `DEMO_MODE=true`,
`initiateStkPush` returns a canned `CheckoutRequestID` and
`queryStkPushStatus` returns `ResultCode: '0'` with **zero vendor calls**.
`config.js` defaults `MPESA_ENVIRONMENT` to **production** and reads
`DEMO_MODE` and `MPESA_ENVIRONMENT` independently: nothing refuses to start
or refuse deposits when they coexist. `.env.example` ships
`DEMO_MODE=true` as a copy-paste default.

**PoC (`f3-demo-mode.mjs` imports and calls the target's REAL `config.js`
+ `services/daraja.js` under the bad pair; captured from an actual run;
exit 0):**

```
=== F3 — demo-mode provider fabrication under a production configuration ===
config: demoMode = true | mpesa.environment = production
initiateStkPush -> {"MerchantRequestID":"demo-merchant-…","CheckoutRequestID":"demo-stk-…","ResponseCode":"0","ResponseDescription":"Demo STK Push accepted",…}
queryStkPushStatus -> {"ResultCode":"0","ResultDesc":"Demo STK payment processed successfully.",…}
F3 FIRED: a deployment configured for PRODUCTION M-Pesa will self-confirm every
STK deposit and B2C payout with zero provider calls when DEMO_MODE=true
startup guard in config.js refusing demoMode+production: ABSENT — config.js reads the flags independently; no cross-check exists
FINDING F3 FIRED
```

**Impact:** one stray `DEMO_MODE=true` in a production env (or a
mis-deployed demo config hitting a prod base URL) turns every "deposit
confirmed" and "payout completed" into fabricated success — the classic
self-confirmed-deposit vulnerability class.

**Patch (one guard in `config.js`):**

```js
if (demoMode && (process.env.MPESA_ENVIRONMENT || 'production') === 'production')
  throw new Error('DEMO_MODE=true is only valid with MPESA_ENVIRONMENT=sandbox')
```

…and make `DEMO_MODE` default `false` in `.env.example`.

---

## F4 — KES amount coercion holes at the real Daraja sink (MEDIUM, PROVEN)

**Code:** `daraja.js` sends `Amount: Math.round(Number(amountKes))` to
Safaricom in both `initiateStkPush` and `initiateB2c`. The PoC no longer
tests a locally-defined lambda: it imports the target's **real**
`services/daraja.js` and drives a corpus through `initiateStkPush` /
`initiateB2c`, intercepting the module's own `node:https` request at the
outbound boundary and asserting on the `Amount` field that actually goes
on the wire — the value M-Pesa would receive.

**PoC (`f4-amount-parsing.mjs`, captured from an actual run; exit 0):**

```
=== F4 — KES amount coercion at the target's real Daraja sink (wire capture) ===
input="abc"                    daraja wire Amount=null       ramp.js->reject: KES amount must be a whole int
input="1.9"                    daraja wire Amount=2          ramp.js->reject: KES amount must be a whole int
input="0x10"                   daraja wire Amount=16         ramp.js->reject: KES amount must be a whole int
input="1e5"                    daraja wire Amount=100000     ramp.js->reject: KES amount must be a whole int
input="-5"                     daraja wire Amount=-5         ramp.js->reject: KES amount must be a whole int
input="0"                      daraja wire Amount=0          ramp.js->reject: KES amount must be greater tha
input=""                       daraja wire Amount=0          ramp.js->reject: KES amount must be a whole int
input="Infinity"               daraja wire Amount=null       ramp.js->reject: KES amount must be a whole int
input="99999999999999999999"   daraja wire Amount=100000000000000000000 ramp.js->reject: KES amount must be at most 100
[B2C sink] input="-5" -> wire Amount=-5 (negative amount accepted at the sink)

daraja.js sink: 9/9 pathological inputs leaked a bad Amount to the M-Pesa wire
leaked inputs: "abc"->null, "1.9"->2, "0x10"->16, "1e5"->100000, "-5"->-5, "0"->0, ""->0, "Infinity"->null, "99…9"->1e20
ramp.js path (BigInt + caps + zero-guard): rejects all of the above — clean (the model the legacy sink should copy)
FINDING F4 FIRED
```

The legacy route is partially guarded (`toRUsdBaseUnits` rejects
non-finite/≤0) and the ramp path is **clean** — `services/ramp.js
parseKesAmount` is BigInt with 0x-rejection, caps, and a zero-result
guard. Credit where due: the ramp path is the model the legacy path
should copy.

**Patch:** replace the coercion with the ramp.js validator at the Daraja
sink (`assertWholeKES(amount, min, max)` before any outbound call).

---

## F5 — Known `SESSION_SECRET` default salts every OTP hash (MEDIUM, PROVEN)

**Code:** `config.js` line 14:
`sessionSecret: process.env.SESSION_SECRET || 'dev-session-secret-change-me'`;
`utils.js hashOtp = sha256(secret:phone:code)`. A deploy that forgets the
env var salts **every OTP** with a public literal.

**PoC (`f5-secret-default.mjs` now imports the target's REAL `config.js`
and `utils.js hashOtp` — no hardcoded secret, no inline reimplementation —
and brute-forces with the target's own hash function; captured from an
actual run; exit 0):**

```
=== F5 — known SESSION_SECRET default is the target's live OTP salt ===
target config.sessionSecret (no env var) = "dev-session-secret-change-me"
OTP hash minted by target hashOtp(phone, 334567) = 36bc9de1…834c
brute-forced code 334567 in 0.08 s (900,000-keyspace, target's own hashOtp)
.env.example ships DEMO_MODE=true: true | DB creds postgres://dular:dular@ present: true
FINDING F5 FIRED — any deploy without a real SESSION_SECRET exposes every OTP to a <2s offline brute-force
```

**Impact:** any captured `(phone, otp_hash)` pair — e.g. via the F7 error
leak or a DB exposure of `otp_requests` — is recovered in <1 s per hash.

**Patch:** require `SESSION_SECRET` at boot (refuse to start, like
`DATABASE_URL`); remove the credential-laden `.env.example` defaults.

---

## F6 — Browser self-custody wallet: 4-digit PIN, no lockout, offline key recovery (MEDIUM, PROVEN crack + STRUCTURAL policy)

**Code:** `src/lib/browserWalletStore.js` — the root self-custody secrets
(Fiber + CKB keys) are encrypted at rest (PBKDF2-SHA256-250k →
AES-256-GCM), key derived from the user's PIN with **no minimum beyond
"≥4 digits" in the UI** (`SelfCustodyApp.jsx:3577`), no charset policy,
no lockout, and **no export/backup path in the module's public API**
(exactly: `createWalletRecord, deleteWalletRecord, loadWalletRecord,
saveWalletRecord, unlockWalletRecord`).

The PoC now calls the target's **real** `createWalletRecord` /
`unlockWalletRecord` (the module targets the browser IndexedDB; an
in-memory test double stands in for that API only — it changes no crypto
or policy logic), not a standalone PBKDF2 loop:

```
=== F6 — browser wallet key custody: PIN policy, lockout, offline recovery ===
createWalletRecord(phone, "1379") -> record keys: phone, encrypted, createdAt, updatedAt, version
stored blob fields: salt, iv, ciphertext (no attempt counter, no kdf params, no policy metadata)
wrong-PIN unlock attempts: 25 | accepted: 0 | record mutated by failed attempts: false
lockout state present in module: NO — the module keeps no attempt counter

=== F6 — offline crack of the exfiltrated blob (target unlockWalletRecord as oracle) ===
recovered PIN "1379" after 1380 attempts in 26.8 s (19 ms/attempt, PBKDF2-SHA256-250k)
recovered Fiber secret key: 32 bytes | CKB secret key: 32 bytes
projected full 4-digit keyspace (10,000 PINs) ≈ 3.2 min on this CPU — no attempt counter, retries unbounded
module public API: createWalletRecord, deleteWalletRecord, loadWalletRecord, saveWalletRecord, unlockWalletRecord
export/backup/recovery entry point in the wallet module: NONE
FINDING F6 FIRED — a 4-digit PIN + an exfiltrated blob yields the owner's real Fiber/CKB private keys offline,
with no lockout and no export path
```

FINDING F6 FIRED for the executed offline-recovery demonstration (the
target's own `unlockWalletRecord` is the brute-force oracle and it
recovers both private keys). The two **absence** claims — no lockout
state, no export/backup entry point — are verified by enumerating the
module's real public API and its stored record (observable state, not a
source grep), but they remain **STRUCTURAL** in the sense of "no
behavior to fire": there is no lockout that could be probed, only one
that could be probed for. That is why the at-a-glance row carries both
labels.

**Impact:** an exfiltrated blob (XSS, device seizure) + a 4-digit PIN =
owner's on-chain keys in minutes with no server-side lockout to slow it;
a lost profile is unrecoverable, which for a money product is an
operational-exposure finding.

**Patch:** enforce ≥6-char mixed-alphanumeric PIN at creation; add a
server-agnostic lockout table (attempts + backoff) around
`unlockWalletRecord`; ship a signed key-export (mnemonic or encrypted
JSON) with a documented restore path.

---

## F7 — Wildcard CORS + global 400 handler leaking internal errors (MEDIUM, PROVEN)

**Code:** `index.js` `app.use(cors())` with **no options** →
`Access-Control-Allow-Origin: *` on every response (and the app also sets
`Cross-Origin-Embedder-Policy`, so the CSP is internally inconsistent for
embedded use). The catch-all: `res.status(400).json({ error: error.message })`
— raw internal strings to any unauthenticated client.

**PoC (`f7-cors-error-leak.mjs`, boots the real app; captured from an actual
run; exit 0):**

```
=== F7 — wildcard CORS + global 400 handler leaking error.message ===
GET /api/health with Origin: evil.example -> ACAO = "*"
POST /api/auth/request-otp (bad phone) -> HTTP 400 body =
{"error":"Use a valid Kenyan phone number, e.g. +254712345678"}
F7 FIRED: any origin may read responses, and raw internal error strings reach unauthenticated clients
FINDING F7 FIRED
```

**Impact:** cross-origin exfiltration of session-bound responses from any
site + oracle of internal error strings (route topology, validation
expectations) usable to drive the F1/F4 bugs.

**Patch:** `cors({ origin: [publicBaseUrl] })`; catch-all returns a
generic 400 and logs `error.message` server-side only.

---

## F8 — Un-redacted receipts + Kenyan PII committed (MEDIUM, STRUCTURAL)

**Evidence:** static inventory of committed files — no executable exploit
exists, but the harness verifies the inventory against the repo's actual
state (`f8f9-hygiene-supply.mjs`, captured from an actual run; exit 0):

```
=== F8 — committed verification artifacts: secret/PII inventory ===
raw (unredacted) screenshots committed: mpesa-stk-nexuspay-original.jpeg, otp-delivery-nexuspay-original.jpeg
redacted screenshots committed: mpesa-stk-nexuspay-redacted.jpg, otp-delivery-nexuspay-redacted.jpg
ussd session CSVs: 1 file(s), distinct Kenyan phone numbers present: 2
.env.example ships a postgres://user:password@ credential: true
.env.example ships DEMO_MODE=true: true
F8 OBSERVED: un-redacted M-Pesa/OTP screenshots AND live Kenyan phone PII are committed to the repo
FINDING F8 STRUCTURAL
```

`verification/milestone-1/screenshots/raw/` (un-redacted M-Pesa/OTP
screens, i.e. live phone PII) sits beside the redacted copies;
`verification/milestone-2/ussd-sessions-africas-talking-2026-06-11.csv`
carries 20 session rows naming **2 distinct** Kenyan phone numbers;
`.env.example` ships `postgres://dular:dular@…` creds and `DEMO_MODE=true`.
(Corrected from the original "20 raw phone numbers" — that counted session
rows, not distinct numbers.)

**Patch:** purge the `raw/` dir and PII from git history
(`git filter-repo`), rotate the exposed DB creds, ship `.env.example` with
placeholder-only values, add a pre-commit check for `+254\d{9}`.

---

## F9 — Client-key module pinned to a pre-release (LOW, STRUCTURAL)

**Evidence** (verified by the same harness against the repo's real
`package.json` + `package-lock.json`):

```
=== F9 — supply chain: client-key module on a pre-release ===
package.json declares @nervosnetwork/fiber-js = ^0.9.0-rc7
pre-release: true
this module generates/holds the user's Fiber + CKB private keys in the browser -> an rc pin with no audit is a supply-chain surface
package-lock.json pins a concrete rc build: true
FINDING F9 STRUCTURAL
```

`@nervosnetwork/fiber-js@^0.9.0-rc7` (lockfile: `0.9.0-rc7`) is the module
that **generates and holds the user's Fiber + CKB private keys in the
browser** — a supply-chain surface worth pinning to an audited release.

**Patch:** pin an audited/stable release; add a `npm audit` gate to CI;
note the pin in the security policy.

---

## On-chain evidence

**What this section is:** the on-chain *state* that Dular's app bugs
produce — not a search for bugs in the chain. Each trace ran the app's real
money path on our 2-node FNN 0.9.1 stack on Puce testnet (Dular's own
chain, its own RUSD UDT), local Postgres only — nothing touched duongja's
operator. A RED row means the **app** bug moved real RUSD on-chain; the
chain executed it faithfully. A SAFE row means the chain face held, so
the defect stays in Dular's DB ledger.

Funding chain (all confirmed on Puce; the funding tx is re-confirmed each
run via CKB `get_transaction` → `tx_status=committed @ 0x15e1b32`, not the
nonexistent `get_transaction_status`): user 10-RUSD transfer
`0x43f4d527…b0cb26d` (out0 = 142 CKB + RUSD under A's lock `…d860f4f9`) →
10-RUSD channel `0xcef4106e`, funding tx `0x72ed0d77…df27d3` (out0 =
channel cell, 360 CKB). At the c4-finalize2 snapshot the channel held
A=9.1 / B=0.9 RUSD.

**Balance reconciliation (corrected).** The original report's breakdown
summed 0.1 (C4.1) + 0.5 (C4.2) + 0.1 (labeled "C4.3") = **0.7** while
balB = 0.9 at the c4-finalize2 snapshot. Measured from the chain this
time (each payment's invoice amount via `get_invoice` +
`parse_invoice`; settlement order per the FNN settle-tlc log), the 0.9
itemizes as `0x76da2929…` 0.1 (C4.1) + `0x9f22a21a…` 0.5 (C4.2) +
`0x97263bb8…` 0.2 + `0x3d30f70d…` 0.1 = **0.9, an exact match**. The
recorded C4.3 first send `0x864781d3…` (0.1 RUSD) settled immediately
after that snapshot, so it is not part of the 0.9. The original
breakdown was short by 0.2 because it never itemized `0x97263bb8…` (a
0.2-RUSD C4.3-style probe from an earlier run on the same channel); its
third "0.1" was actually `0x3d30f70d…`, another earlier-run settlement.
Two of the five settled commitments on this channel came from earlier
runs, not from the C4.1/C4.2/C4.3 traces. No RUSD left the channel other
than the settled payments above. The full itemization is in
`c4-record.json` (`C4.0.settlement_breakdown`).

| Trace | App finding proven | Outcome | Payment hash |
|---|---|---|---|
| C4.1 | control | **GREEN** — 0.1 RUSD settled A→B (re-verified by hash) | `0x76da2929…39f1` |
| C4.2 | **F1** forged callback | **RED** — 0.5 RUSD left the operator channel to a third-party invoice against a fabricated `ResultCode:0` (zero Daraja calls); money-in fake, money-out real (settled in the original run; re-verified by its hash) | `0x9f22a21a…a4daba` |
| C4.3 | **F2** double-settle | **SAFE-AT-CHAIN** — second concurrent send of the same invoice rejected ("Payment session already exists"): F2's double-credit is Dular's DB-ledger bug, not a Fiber overpay | send 1 `0x864781d3…5bd14` settled; send 2 rejected |
| C4.4 | F1/F4 invoice guards | **AMBER** — amount/payee guard runs only at invoice-attach time (`validateRampInvoice`); the settle hop pays the stored invoice as-is | app validator rejects wrong-amount: "Fiber invoice amount does not match the quote" |
| C4.5 | ops edge | **by-design guard held** — a 1-base-unit `open_channel` was *accepted into pending-manual-accept* (not hard-rejected); the live log shows the guard: "funding amount 1 is less than required auto-accept minimum 1000000000 … will not be auto-accepted and is pending manual acceptance". A stuck channel is operator liquidity Dular surfaces, not a vulnerability. Force-close skipped per stop-condition | live fnn log |

**C4.5 wording (corrected against `c4-record.json`).** The record's
`"open_channel_too_low": "unexpectedly accepted"` is the ground truth —
the `open_channel` **call succeeded** and the channel entered
`NegotiatingFunding`/pending-manual-accept; it was the *recorder's*
surprise (hence "unexpectedly") that FNN parked it rather than rejecting
it. The auto-accept guard itself held (RUSD `auto_accept_amount` =
`0x3b9aca00` = 10 RUSD), so this is **not** a hole and not a "FIRED"
exploit. The old report's "FIRED (by-design guard)" conflated "the probe
was accepted into a pending state" with "a guard fired as a rejection."
The PoC now records both the outcome and the guard evidence, with the
fresh probe opt-in (`C4_FIRE=1`): re-firing it adds pending channels to
our own stack without adding information.

Reproduce: `node c4-openchannel2.mjs` (re-verifies C4.1/C4.2/C4.3 by
hash, runs C4.4/C4.5; fresh sends opt-in via `C4_FIRE=1`) +
`node c4-finalize2.mjs` (re-records by hash). Every hash above is in
`dular/poc/c4-record.json` — **including the `status` and `VERDICT`
fields for C4.1 and C4.2** that the old positional-3-arg `record()`
dropped, and the C4.4 validator output that was previously **absent**
from the record and is now recorded.

Impact confined to **our own** two FNN nodes + our own CKB testnet
accounts + local Postgres.


## Layer boundary

We test **how Dular uses the chain**, not the FNN protocol itself. FNN
lock scripts (FundingLock `0x6c67…6d7c`, CommitmentLock) are shared
protocol assets; if a C4 trace trips an upstream FNN issue we link it to
the FNN repo (nervosnetwork/fiber) and do not claim it as a Dular finding.
C4.5's pending-manual-accept behavior is FNN's by-design auto-accept
guard — recorded as such, not claimed as a Dular bug.

## Honest limitations

- PoCs run against a local clone with mocked vendor/Fiber boundaries;
  findings are about the **default code paths**, not a remote exploit of
  the live dular.vercel.app service.
- F1's "free RUSD" is demonstrated end-to-end through the real Express +
  Postgres app; the Fiber hop is mocked in F1 and proven **for real** in
  C4.2 (settled in the original run; re-verified by its recorded hash this
  run, not re-fired).
- F2's parallel run is probabilistic (8/12 fired in the captured run; 9/12
  and 4/12 in earlier sessions); the deterministic interleave (Part A) is
  the reproducible core, and Parts D/E (nesting atomicity + negative
  control) are deterministic.
- F6's crack timings are this CPU's; a GPU would be faster, but the
  conclusion (minutes, unbounded retries, no lockout) does not change.
- The C4 traces use public CKB testnet: statements about isolation are
  "our addresses, our services" — the testnet itself is shared/public.
- **UNPROVEN findings: none.** Every at-a-glance row above has a captured
  run in its detail section; no row is claimed PROVEN without one. F8/F9
  are STRUCTURAL by design (static inventory of committed files / lockfile
  pin — there is no executable exploit to run); the "no lockout / no
  export" half of F6 is the same class, folded into F6's PROVEN row and
  labeled STRUCTURAL there.
