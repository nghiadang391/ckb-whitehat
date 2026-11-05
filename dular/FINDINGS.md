# ckb-whitehat — Dular (duongja/Dular)

Target: https://github.com/duongja/Dular — M-Pesa STK-push mobile-money wallet
over Fiber (FNN) + CKB testnet RUSD. PoCs run against **unmodified source** in
a local clone + local Postgres, vendors (M-Pesa/Daraja, Fiber RPC) mocked at
their outbound boundaries. No request ever left this machine toward dular's
live Vercel/Railway deployment. One command:
`cd /tmp/ckb-whitehat/dular-poc && node <finding>.mjs`.

Severity = impact on a M-Pesa merchant/user, weighted by how a real attacker
reaches it. "Fired" = the PoC's deterministic assertion passed against the
unmodified repo.

---

## At a glance

Two layers: **offline** findings (app/DB/config — the code you can break on a
laptop) and the **on-chain** evidence (C4.1–C4.5 — the same findings proven
against a live RUSD channel on Dular's own testnet). Details for every row
follow in this file, in order.

### Offline findings (F1–F9) — app / DB / config layer

| # | Finding | Sev | Result | Money-path? |
|---|---|---|---|---|
| **F1** | Forged M-Pesa STK callback = free RUSD (static shared token; demo-mode bypass; legacy routes need no auth) | **CRITICAL** | fired | yes |
| **F2** | Double-spend race in `ledger.js debit()` (read→guard→write, no `FOR UPDATE`); 3-line fix included, harness passes | **HIGH** | fired | yes |
| **F3** | Demo mode fabricates provider success under a production config (app boots + credits without refusing the pair) | **HIGH** | fired | yes |
| **F4** | KES amount coercion in the Daraja sink (`Math.round(Number())` passes NaN/Infinity/-1) | MEDIUM | fired | yes |
| **F5** | Known `SESSION_SECRET` default salts every OTP hash — captured `(phone, otp_hash)` brute-forced <1s offline | MEDIUM | fired | yes |
| **F6** | Browser self-custody wallet: 4-digit PIN, no strength policy, no lockout, no export | MEDIUM | structural | keys |
| **F7** | Wildcard CORS + global 400 handler leaking internal error strings | MEDIUM | fired | no |
| **F8** | Un-redacted M-Pesa/OTP screenshots + Kenyan phone PII committed; DB creds in `.env.example` | MEDIUM | structural | no |
| **F9** | Client-key `fiber-js` pinned to a pre-release rc (lockfile only) | LOW | structural | no |

### On-chain evidence (C4.1–C4.5) — our 2-node FNN 0.9.1 stack, Dular's UDT + chain

| Trace | App finding proven | Outcome | Payment/tx hash |
|---|---|---|---|
| **C4.1** | control | **GREEN** — 0.1 RUSD settled A→B, honest path works | `0x76da2929…39f1` |
| **C4.2** | **F1** forged callback | **RED** — 0.5 RUSD left the operator channel for a fabricated `ResultCode:0` money-in (bug is the app's; chain executed it) | `0x9f22a21a…a4daba` |
| **C4.3** | **F2** double-settle | **SAFE-AT-CHAIN** — FNN rejects the duplicate invoice send; F2 is a Dular DB-ledger bug, not a Fiber overpay | send 1 `0x864781d3…5bd14` |
| **C4.4** | F1/F4 invoice guards | **AMBER** — guard runs only at attach time, not re-checked at settle | (app validator output) |
| **C4.5** | ops edge | **FIRED** — auto-accept-min guard observed live; force-close skipped per stop-condition | (live fnn log) |

Money-moved artifacts (all in `c4-record.json`): user 10-RUSD transfer
`0x43f4d527…b0cb26d` → channel `0xcef4106e` funding tx `0x72ed0d77…df27d3`
(confirmed at block `0x15e1b32`). Balances reconcile: 9.1 + 0.9 = 10 RUSD.

---

## F1 — Forged M-Pesa callback = free RUSD (CRITICAL, fired)

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

**PoC (`f1-forged-callback.mjs`, fired):**

```
=== F1-A — forged legacy STK callback (no auth, no token, zero vendor calls) ===
forged callback POST -> HTTP 200 {"ok":true}
transaction state: pending -> completed
ledger credits: 0 -> 100000000 (1 RUSD to the user, 100 KES "paid")
vendor calls made: 0
F1-A FIRED: forged callback credited the ledger with zero M-Pesa money and zero vendor calls

=== F1-B — forged RAMP STK callback, demo-mode bypass (no token required) ===
order state: mpesa_pending -> completed (fiber_payment_hash=0xffffffffff… status=Success)
F1-B FIRED: order completed — operator sent RUSD to the attacker-supplied
browser_invoice with zero M-Pesa money and zero vendor calls
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

## F2 — Double-spend race in `ledger.js debit()` (HIGH, fired)

**Code:** `server/services/ledger.js:37` — `SELECT balance` (no lock) →
guard → `INSERT ledger_entries` → `UPDATE balance = balance - amount`.
No transaction, no `FOR UPDATE`. The repo **already ships** the fix
primitives (`withTransaction`, `withAdvisoryLock` in `server/db.js`) and
uses them everywhere else (`rampRoutes.js`, `ussd.js`) — the money path in
`ledger.js` just doesn't.

**PoC (`f2-ledger-race.mjs`, fired) — 12 parallel 600-unit debits on 1000:**

```
=== UNFIXED ledger.js ===
successful debits: 9 (max that fits in 1000 = 1), rejected: 3
final balance: -4400   sum of posted debits: 5400   funds available: 1000
INVARIANT VIOLATED: 9 debits posted, 5400 > 1000 — more money went out than existed

=== FIXED ledger.js (withTransaction + FOR UPDATE) ===
successful debits: 1, rejected: 11
final balance: 400   sum of posted debits: 600
invariant held: only 1 debit fit, balance never negative
```

(The deterministic two-client interleave — Part A of the same PoC — lands
balance **−200** with zero randomness; the parallel run is the real-module
corroboration.)

**Impact:** two concurrent payments (`/api/payments/send-phone`, any
`debit` caller) both read the pre-deduction balance, both pass the guard,
both post → user's RUSD balance is negative; the ledger says "paid" for
money that was never there. `phone_payment` debits feed key-sends to other
users' Fiber wallets, so the loss propagates off-ledger.

**Patch (working, in `fixed-ledger.js`):** wrap the critical section:

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

Re-running the identical 12-parallel harness against the patched module
holds the invariant (1 success, 11 rejected, balance 400). That is the
regression pack: the fix and the test that locks it in.

---

## F3 — Demo mode fabricates provider success under a production config (HIGH, fired)

**Code:** `server/services/daraja.js` — when `DEMO_MODE=true`,
`initiateStkPush` returns a canned `CheckoutRequestID` and
`queryStkPushStatus` returns `ResultCode: '0'` with **zero vendor calls**.
`config.js` defaults `MPESA_ENVIRONMENT` to **production** and reads
`DEMO_MODE` and `MPESA_ENVIRONMENT` independently: nothing refuses to start
or refuse deposits when they coexist. `.env.example` ships
`DEMO_MODE=true` as a copy-paste default.

**PoC (`f3-demo-mode.mjs`, fired):**

```
config: demoMode = true | mpesa.environment = production
initiateStkPush -> {"ResponseCode":"0","CustomerMessage":"Demo mode: use callback fixture…"}
queryStkPushStatus -> {"ResultCode":"0","ResultDesc":"Demo STK payment processed successfully."}
F3 FIRED: a deployment configured for PRODUCTION M-Pesa will self-confirm every
STK deposit and B2C payout with zero provider calls when DEMO_MODE=true
startup guard in config.js refusing demoMode+production: ABSENT
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

## F4 — KES amount coercion holes in the Daraja sink (MEDIUM, fired)

**Code:** `daraja.js` sends `Amount: Math.round(Number(amountKes))`.
Corpus result (`f4-amount-parsing.mjs`):

```
input="abc"  -> NaN sent to M-Pesa      input="1.9" -> 2 (silent round-up)
input="0x10" -> 16 (hex parsed!)        input="1e5" -> 100000 (exponent)
input="-5"   -> -5 (negative amount)    input="9999…9" -> 1e20
```

The legacy route is partially guarded (`toRUsdBaseUnits` rejects
non-finite/≤0) and the ramp path is **clean** — `services/ramp.js`
`parseDecimalInteger` is BigInt with 0x-rejection, caps, and a
zero-result guard. Credit where due: the ramp path is the model the legacy
path should copy.

**Patch:** replace the coercion with the ramp.js validator at the Daraja
sink (`assertWholeKES(amount, min, max)` before any outbound call).

---

## F5 — Known `SESSION_SECRET` default salts every OTP hash (MEDIUM, fired)

**Code:** `config.js` line 14:
`sessionSecret: process.env.SESSION_SECRET || 'dev-session-secret-change-me'`;
`utils.js hashOtp = sha256(secret:phone:code)`. A deploy that forgets the
env var salts **every OTP** with a public literal.

**PoC (`f5-secret-default.mjs`, fired):**

```
brute-forced code 334567 in 0.08 s (900,000-keyspace, pure Node crypto)
.env.example ships DEMO_MODE=true: true | DB creds postgres://dular:dular@ present: true
```

**Impact:** any captured `(phone, otp_hash)` pair — e.g. via the F7 error
leak or a DB exposure of `otp_requests` — is recovered in <1 s per hash.

**Patch:** require `SESSION_SECRET` at boot (refuse to start, like
`DATABASE_URL`); remove the credential-laden `.env.example` defaults.

---

## F6 — Browser self-custody wallet: no PIN policy, no lockout, no export (MEDIUM, confirmed-structural)

**Code:** `src/lib/browserWalletStore.js` — good at-rest crypto
(PBKDF2-SHA256-250k → AES-256-GCM), but the PIN goes to UTF-8 with no
minimum length, no rate limit, no lockout, and there is **no export/backup
path anywhere in the codebase**: losing the browser profile = permanently
losing the Fiber + CKB private keys.

**PoC (`f6-pin-wallet.mjs`):**

```
min-length / PIN policy    present=no
rate-limit / lockout      present=no
export/backup/recovery    present=no
timed 1000 PBKDF2-SHA256-250k derivations in 20.4 s (this CPU)
projected full 4-digit keyspace (10,000 PINs) ≈ 3.4 min on a laptop CPU
```

**Impact:** an exfiltrated blob (XSS, device seizure) + 4-digit PIN =
owner's on-chain keys in minutes with no server-side lockout to slow it;
a lost profile is unrecoverable, which for a money product is an
operational-exposure finding.

**Patch:** enforce ≥6-char mixed-alphanumeric PIN at creation; add a
server-agnostic lockout table (attempts + backoff) around
`unlockWalletRecord`; ship a signed key-export (mnemonic or encrypted
JSON) with a documented restore path.

---

## F7 — Wildcard CORS + global 400 handler leaking internal errors (MEDIUM, fired)

**Code:** `index.js:60` `app.use(cors())` with **no options** →
`Access-Control-Allow-Origin: *` on every response (and the app also sets
`Cross-Origin-Embedder-Policy`, so the CSP is internally inconsistent for
embedded use). Line 2358 catch-all: `res.status(400).json({ error: error.message })`
— raw internal strings to any unauthenticated client.

**PoC (`f7-cors-error-leak.mjs`, fired):**

```
GET /api/health with Origin: evil.example -> ACAO = "*"
POST /api/auth/request-otp (bad phone) -> HTTP 400 body =
{"error":"Use a valid Kenyan phone number, e.g. +254712345678"}
```

**Impact:** cross-origin exfiltration of session-bound responses from any
site + oracle of internal error strings (route topology, validation
expectations) usable to drive the F1/F4 bugs.

**Patch:** `cors({ origin: [publicBaseUrl] })`; catch-all returns a
generic 400 and logs `error.message` server-side only.

---

## F8 — Un-redacted receipts + Kenyan PII committed (MEDIUM, confirmed-structural)

**Evidence:** `verification/milestone-1/screenshots/raw/` contains
`mpesa-stk-nexuspay-original.jpeg` and `otp-delivery-nexuspay-original.jpeg`
(un-redacted M-Pesa OTP screens, i.e. live phone PII) beside the redacted
copies; `verification/milestone-2` session CSVs carry **20 raw phone
numbers**; `.env.example` ships `postgres://dular:dular@…` creds and
`DEMO_MODE=true`.

**Patch:** purge the `raw/` dir and PII from git history
(`git filter-repo`), rotate the exposed DB creds, ship `.env.example` with
placeholder-only values, add a pre-commit check for `+254\d{9}`.

---

## F9 — Client-key module pinned to a pre-release (LOW, confirmed-structural)

**Evidence:** `@nervosnetwork/fiber-js@^0.9.0-rc7` — the module that
**generates and holds the user's Fiber + CKB private keys in the browser**
is a release candidate, pinned only in the lockfile.

**Patch:** pin an audited/stable release; add a `npm audit` gate to CI;
note the pin in the security policy.

---

## On-chain evidence

**What this section is:** the on-chain *state* that Dular's app bugs produce —
not a search for bugs in the chain. Each trace ran the app's real money path
on our 2-node FNN 0.9.1 stack on Lina testnet (Dular's own chain, its own
RUSD UDT), local Postgres only — nothing touched duongja's operator. A RED
row means the **app** bug moved real RUSD on-chain; the chain executed it
faithfully. A SAFE row means the chain face held, so the defect stays in
Dular's DB ledger.

Funding chain (all confirmed on Lina): user 10-RUSD transfer
`0x43f4d527…b0cb26d` (out0 = 142 CKB + RUSD under A's lock `…d860f4f9`) →
10-RUSD channel `0xcef4106e`, funding tx `0x72ed0d77…df27d3` at block
`0x15e1b32` (out0 = channel cell, 360 CKB). Channel ended A=9.1 / B=0.9 RUSD
— reconciles exactly, no RUSD escaped the channel except the settled
payments below. Explorer:
`https://pudge.explorer.nervos.org/transaction/0x72ed0d77…`.

| Trace | App finding proven | Outcome | Payment hash |
|---|---|---|---|
| C4.1 | control | **GREEN** — 0.1 RUSD settled A→B: the honest path works | `0x76da2929…39f1` |
| C4.2 | **F1** forged callback | **RED** — 0.5 RUSD left the operator channel to a third-party invoice against a fabricated `ResultCode:0` (zero Daraja calls); money-in fake, money-out real | `0x9f22a21a…a4daba` |
| C4.3 | **F2** double-settle | **SAFE-AT-CHAIN** — second concurrent send of the same invoice rejected ("Payment session already exists"): F2's double-credit is Dular's DB-ledger bug, not a Fiber overpay | send 1 `0x864781d3…5bd14` settled; send 2 rejected |
| C4.4 | F1/F4 invoice guards | **AMBER** — amount/payee guard runs only at invoice-attach time (`validateRampInvoice`); the settle hop pays the stored invoice as-is | app validator rejects wrong-amount: "Fiber invoice amount does not match the quote" |
| C4.5 | ops edge | **FIRED (by-design guard)** — B's RUSD auto-accept min = 10 RUSD held a 1-RUSD open pending manual accept; a channel stuck here is operator liquidity Dular surfaces, not a vulnerability. Force-close skipped per our stop-condition | live fnn log: "funding amount … less than required auto-accept minimum" |

Reproduce: `node c4-openchannel2.mjs` (waits for `ChannelReady`, fires
C4.1/4.2/4.3) + `node c4-finalize2.mjs` (re-records by hash + fresh unique
C4.3). Every hash above is in `c4-record.json`.

Impact confined to **our own** two FNN nodes + our own CKB testnet accounts +
local Postgres.


## Layer boundary

We test **how Dular uses the chain**, not the FNN protocol itself. FNN lock
scripts (FundingLock `0x6c67…6d7c`, CommitmentLock) are shared protocol
assets; if a C4 trace trips an upstream FNN bug we will link it to the FNN
repo (the Sail-validation precedent is the committee's own marker of that
lane) and not claim it as a Dular finding.

## Honest limitations

- PoCs run against a local clone with mocked vendor/Fiber boundaries;
  findings are about the **default code paths**, not a remote exploit of the
  live dular.vercel.app service.
- F1's "free RUSD" is demonstrated end-to-end through the real Express +
  Postgres app; the Fiber hop is mocked in F1 and proven **for real** in C4.2.
- F2's parallel run is probabilistic (it fired 9/12 this session); the
  deterministic interleave (Part A) is the reproducible core, and the fixed
  version is deterministic both ways.
- F6 numbers are this CPU's; any GPU/fpga would be faster, the conclusion
  (minutes, unbounded retries) does not change.
- C4 uses public CKB testnet: statements about isolation are
  "our addresses, our services" — testnet itself is shared/public.
