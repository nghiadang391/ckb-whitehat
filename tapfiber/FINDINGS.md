# ckb-whitehat — tapfiber-core v0.1.0 security challenge

Target: github.com/0xVida/tapfiber (MIT, pure JS, no deps). All PoCs run
against unmodified source: `node tapfiber/poc/attack.mjs`. No network, no
mainnet, no keys needed.

Severity = impact on a merchant using this for paid access, weighted by how a
real attacker would feed `applyEvent` / `decodePayload` / `verifyReceipt`.

## At a glance

### Offline findings

| # | Finding (one clause) | Sev | Status (PROVEN/UNPROVEN/STRUCTURAL) | Money-path? |
|---|---|---|---|---|
| **F1** | post-expiry confirmation accepted when `event.at` is omitted | HIGH | PROVEN (fired) | yes |
| **F2** | `expiresAt` not enforced by `createOrder` | MEDIUM | PROVEN (fired) | yes |
| **F3** | unpinned `paymentHash` accepts any payment | HIGH | PROVEN (fired) | yes |
| **F4** | `verifyReceipt` accepts any issuer key by default | HIGH | PROVEN (fired) | no |
| **F5** | payload integrity is an unkeyed SHA-256 prefix: active tampering is O(1), not 2^32 | MEDIUM | PROVEN (fired) | yes |
| **F6** | receipt body omits `invoice` and `expiresAt` | LOW–MEDIUM | PROVEN (fired) | no |
| **F7** | receipts have no nonce/TTL: infinite replay | MEDIUM | PROVEN (fired) | no |
| **F8** | `canonical()` emits `{"paidAt":undefined}`: receipt verifies in-memory, breaks after JSON round-trip | MEDIUM | PROVEN (fired) | no |
| **F9** | `verifyReceipt` with `expectedPublicKey: ""` (empty key file) skips issuer verification | HIGH | PROVEN (fired) | no |
| **F10** | `applyEvent` accepts non-numeric/non-finite `event.at` and `event.now` | MEDIUM | PROVEN (fired) | yes |
| **F11** | `createOrder` accepts `amountShannons` 0, "-100", NaN, undefined, "1.5" (and `orderId: undefined`); all reach PAID | HIGH | PROVEN (fired) | yes |
| **F12** | `decodePayload` accepts `{ now: NaN }`, bypassing expiry; the CLI `decode` command never passes `now` at all | MEDIUM | PROVEN (fired) | no |
| **F13** | `verifyReceipt` does not validate the body schema; `issueReceipt` does not check key correspondence | MEDIUM | PROVEN (fired) | no |
| **F14** | `validate()` accepts `expiresAt <= 0`, array amounts, and `null`/`false`/`0` for required string fields | MEDIUM | PROVEN (fired) | yes |

### On-chain / state evidence

| Trace | App finding proven | Result (GREEN/RED/SAFE) | payment/tx hash |
|---|---|---|---|
| — | — | not executed (no on-chain state) | — |

## F1 — Post-expiry payment accepted when `event.at` is omitted (HIGH) — PROVEN

`applyEvent` (src/order.js): the expiry check is `if (event.at > order.expiresAt)`.
Because `event.at` is optional, any caller (webhook bug, malicious adapter) can
confirm a payment after the order's deadline by simply not sending `at`.

PoC (run output, captured from an actual run of `node tapfiber/poc/attack.mjs`):
```
=== F1 — post-expiry confirmation accepted when event.at is stripped ===
with at=5000  -> confirmed_after_expiry
at stripped   -> PAID => receipt issuable
FINDING F1 FIRED — expired order accepted because event.at is optional
```
Impact: an expired tap-to-pay order can still be marked PAID and issue a valid
receipt — a "grace period" no one asked for.
Patch: reject when `event.at === undefined` (or require it and derive it from
event time), i.e. `if (event.at === undefined || event.at > order.expiresAt)`.

## F2 — `expiresAt` is not enforced by `createOrder` (MEDIUM) — PROVEN

`createOrder` (src/order.js) accepts orders without `expiresAt` (`payload.js`
enforces it for the payload, but `createOrder` does not — and receipts come
from orders, not payloads). An unexpired order can accept confirmations with
`at` up to `Number.MAX_SAFE_INTEGER`.

PoC (run output, captured from an actual run):
```
=== F2 — missing expiresAt disables expiry entirely ===
at = 9007199254740991 (~year 285,000) -> PAID
FINDING F2 FIRED — no expiry bound on orders built directly
```
Impact: any integration that builds orders directly skips expiry entirely.
Patch: make `expiresAt` required in `createOrder`, mirroring `validate()`.

## F3 — Unpinned `paymentHash` accepts any payment (HIGH, integration-dependent) — PROVEN

In `applyEvent`, the paymentHash check is `if (order.paymentHash && ...)` — a
skip-if-absent. Orders created without `paymentHash` mark PAID on *any*
confirmation with matching amount/asset/network.

PoC (run output, captured from an actual run):
```
=== F3 — order without paymentHash accepts attacker-chosen hash ===
state: PAID  paymentHash: attacker-chosen
FINDING F3 FIRED — payment binding skipped when paymentHash is absent
```
Impact: a merchant that omits the hash trades payment binding for
"some payment of that amount happened" — a swapped/duplicate payment can be
claimed as the order's payment.
Patch: require `paymentHash` in `createOrder` unless the integration explicitly
opts into amount-only matching (document the risk).

## F4 — `verifyReceipt` defaults to accepting any issuer key (HIGH by-default) — PROVEN

`verifyReceipt(receipt)` with no `expectedPublicKey` returns `{valid: true}`
for any self-signed receipt. The PoC forges a receipt with a fresh Ed25519
key and it verifies clean. The CLI's `verify` command makes this worse: with
no key file argument, exit code 0 means "valid" even for a random key.

PoC (run output, captured from an actual run):
```
=== F4 — rogue-key receipt verifies 'valid' by default ===
verify without expected key -> {"valid":true}
FINDING F4 FIRED — issuer pinning is opt-in
```
Impact: a consumer that wires `verifyReceipt(rcpt)` without pinning the
merchant key accepts receipts signed by anyone — "proof of payment" becomes
free to forge.
Patch: make `expectedPublicKey` required (throw when absent), and make the CLI
error out when the key file is omitted.

## F5 — Payload integrity is an unkeyed SHA-256 prefix, so active tampering is O(1) (MEDIUM) — PROVEN

`check()` in src/payload.js is
`crypto.createHash("sha256").update(s).digest("hex").slice(0, 8)` — an
**unkeyed** hash prefix, not a keyed MAC. The earlier draft of this report
claimed tampering required ~2^32 work ("hours on one CPU core"). That is
wrong: `decodePayload` checks `check(m[1]) !== m[2]` with no secret involved,
so an attacker who edits the body (e.g. `amountShannons`) just recomputes the
8-hex prefix over the new body — one hash, O(1), no search at all.
Lengthening the checksum does not fix active tampering either; only a keyed
MAC or a signature over the payload would.

PoC (run output, captured from an actual run — the tampered payload decodes):
```
=== F5 — payload checksum is unkeyed: tampering is O(1), not 2^32 ===
payload: tapfiber:1:eyJhbW91bnRTaGFubm9ucyI6IjEwM... check: eee2b426
tampered: tapfiber:1:eyJhbW91bnRTaGFubm9ucyI6Ijk5O... check: 141f7aef (recomputed, O(1))
amountShannons: 100000000 -> 999999999
FINDING F5 FIRED — unkeyed sha256 prefix -> active tampering is cheap, not 2^32
```
(The `payload:`/`tampered:` lines are truncated by the script itself via
`.slice(0, 40)`; the 8-hex `check:` values are complete — the PoC recomputes
the prefix from the tampered body, which is why `decodePayload` accepts it.)
Impact: NFC/magnetic-tap handoffs that trust this string end-to-end accept
attacker-rewritten amounts; the checksum only defends against accidental
corruption. The README already states "The payload checksum detects
corruption. It is not a signature."
Patch: sign the payload with the merchant key (the keys already exist for
receipts), or use a keyed MAC (HMAC) with a shared secret; keep the short
checksum only as a corruption hint.

## F6 — Receipt body omits `invoice` and `expiresAt` (LOW–MEDIUM) — PROVEN

`issueReceipt` (src/receipt.js) signs `{version, orderId, paymentHash,
network, asset, amountShannons, paidAt}`. The signature proves "amount X was
paid" but not the order's expiry window or invoice reference — a proof that's
weaker than the order it describes.

PoC (run output, captured from an actual run):
```
=== F6 — receipt body drops expiresAt and invoice (forgery by omission) ===
signed body fields: version, orderId, paymentHash, network, asset, amountShannons, paidAt | NOT signed: invoice, expiresAt -> the signature proves 'amount X paid', not 'order O paid by time T'
FINDING F6 FIRED — signature is not order-bound
```
Impact: a receipt does not prove which order or which expiry window it
belongs to; a consumer must trust an unsigned, out-of-band copy of the order.
Patch: add the relevant order fields to the signed body.

## F7 — Receipts have no nonce/TTL: infinite replay (MEDIUM) — PROVEN

`verifyReceipt` has no notion of "already redeemed". The same receipt verifies
forever (PoC: 3/3 replays all `valid: true`). If a receipt ever gates access
or a refund, one capture = unlimited use.

PoC (run output, captured from an actual run):
```
=== F7 — receipt replay: same receipt proves 'paid' forever, for any number of claims ===
replay 1 -> {"valid":true}
replay 2 -> {"valid":true}
replay 3 -> {"valid":true}
FINDING F7 FIRED — no nonce, no TTL, no redemption log
```
Impact: replay of a captured receipt is undetectable to the verifier.
Patch: add a one-time `nonce` field + a consumer-side redemption log, or an
`expiresAt` on the receipt body (also fixes part of F6).

## F8 — `canonical()` emits `{"paidAt":undefined}`: broken receipt round-trip (MEDIUM) — PROVEN

When an order is confirmed without `event.at` (the F1 case), `order.paidAt`
is `undefined`, and `issueReceipt` puts it into the signed body. `canonical()`
stringifies it as the literal token `undefined` (`JSON.stringify` does the
same on object values), so the *in-memory* receipt verifies. But any JSON
serialization of the receipt (HTTP transport, `JSON.stringify`, file storage)
drops the `undefined` key, and the re-parsed receipt now fails the
signature: `bad_signature`. A receipt that verifies only before it leaves the
process.

PoC (run output, captured from an actual run):
```
=== F8 — receipt with undefined paidAt verifies locally but breaks after JSON round-trip ===
canonical(body): {"amountShannons":"1","asset":"CKB","network":"testnet","orderId":"o8","paidAt":undefined,"paymentHash":"h8","version":1}
verify (in-memory): {"valid":true}
verify (after JSON.stringify): {"valid":false,"reason":"bad_signature"}
FINDING F8 FIRED — undefined in canonical body = broken round-trip
```
Impact: legitimate PAID orders confirmed without a timestamp produce
receipts that break as soon as they are sent or stored; silently-invalid
"proof of payment".
Patch: never sign `undefined` — require `paidAt` (fix F1) or normalize it
(e.g. `paidAt: order.paidAt ?? 0` with the type documented).

## F9 — `verifyReceipt` with an empty key file disables issuer verification (HIGH) — PROVEN

`verifyReceipt` (src/receipt.js) gates the issuer check on truthiness:
`if (expectedPublicKey && ...)`. The CLI passes the key through
`fs.readFileSync` — so an *empty* merchant key file yields
`expectedPublicKey: ""`, and the issuer check is skipped: any issuer's
receipt verifies. One typo (a blank or truncated key file) turns a
pinned-issuer flow into F4's any-issuer flow, silently, with no error.

PoC (run output, captured from an actual run — includes the CLI's empty-key-file path):
```
=== F9 — expectedPublicKey: "" (empty key file) disables issuer verification ===
rogue receipt, expectedPublicKey:"" -> {"valid":true}
rogue receipt, expectedPublicKey:"" (as CLI reads an empty key file) -> {"valid":true}
FINDING F9 FIRED — empty expectedPublicKey bypasses issuer pinning
```
Impact: a merchant who ships a blank key file accepts forgeries of "proof of
payment" with no diagnostic.
Patch: reject a missing/empty key explicitly: treat `expectedPublicKey`
absent or whitespace-only as an error, not as "any issuer".

## F10 — `applyEvent` accepts non-numeric/non-finite `event.at` and `event.now` (MEDIUM) — PROVEN

The comparisons `event.at > order.expiresAt` and `event.now >
order.expiresAt` (src/order.js) are JavaScript number comparisons. With
non-numeric or `NaN` inputs they evaluate `false`, so:

- `payment_confirmed` with `at` = `"not-a-number"` / `null` / `NaN` /
  `undefined` all mark the order PAID (the F1 case is `undefined`);
- `tick` with `now` = `NaN` or `"not-a-number"` never expires the order.

PoC (run output, captured from an actual run; the trailing `""` in the
script's output is the JSON-stringified "no rejection reason"):
```
=== F10 — applyEvent accepts non-numeric event.at / event.now ===
payment_confirmed at=not-a-number -> PAID ""
payment_confirmed at=null -> PAID ""
payment_confirmed at=NaN -> PAID ""
payment_confirmed at=undefined -> PAID ""
tick now=NaN -> AWAITING_PAYMENT | tick now="not-a-number" -> AWAITING_PAYMENT
FINDING F10 FIRED — no type check on event.at / event.now
```
Impact: a misbehaving adapter that emits `at: "2024-01-01"` (an ISO string)
or drops the field still drives orders to PAID; a `tick` with a broken clock
never expires anything.
Patch: `if (!Number.isFinite(event.at)) return keep(order, "invalid_event_time")`
(same for `event.now`), or coerce and validate before comparing.

## F11 — `createOrder` accepts invalid `amountShannons` (0, "-100", NaN, undefined, "1.5") and `orderId: undefined` (HIGH) — PROVEN

`createOrder` (src/order.js) does `amountShannons: String(amountShannons)`
with no validation, and `applyEvent` compares with
`String(event.amountShannons) !== order.amountShannons` — so every value
below is accepted and transitions the order to PAID on a matching
confirmation:

| Input | `order.amountShannons` | State after confirmation |
|---|---|---|
| `0` | `"0"` | PAID |
| `"-100"` | `"-100"` | PAID |
| `NaN` | `"NaN"` | PAID |
| `undefined` | `"undefined"` | PAID |
| `"1.5"` | `"1.5"` | PAID |

PoC (run output, captured from an actual run; `amountShannons=-100` in the
log is the string `"-100"` printed via `String(amount)`):
```
=== F11 — createOrder accepts invalid amountShannons (0, "-100", NaN, undefined, "1.5") ===
amountShannons=0 -> order.amountShannons="0", state=PAID
amountShannons=-100 -> order.amountShannons="-100", state=PAID
amountShannons=NaN -> order.amountShannons="NaN", state=PAID
amountShannons=undefined -> order.amountShannons="undefined", state=PAID
amountShannons=1.5 -> order.amountShannons="1.5", state=PAID
orderId undefined -> orderId=undefined
FINDING F11 FIRED — zero / negative / NaN / undefined / fractional amounts all reach PAID
```
Impact: a library whose only money invariant is "amounts are positive
integers" (enforced by `validate()` for payloads, not for orders) accepts
zero/negative/NaN/fractional shannon amounts and marks orders PAID. A sloppy
or hostile caller can record a PAID state for a payment that cannot exist.
Patch: validate in `createOrder` with the same rule as `validate()`:
`/^[1-9][0-9]*$/.test(String(amountShannons))`, plus a required `orderId`.

## F12 — `decodePayload` accepts `{ now: NaN }`, bypassing expiry; the CLI never passes `now` (MEDIUM) — PROVEN

`decodePayload(str, { now })` checks `if (now !== undefined && now >
p.expiresAt)`. A caller passing `{ now: NaN }` passes the check vacuously
(`NaN > x` is `false`). And the CLI `decode` command (bin/tapfiber.js) calls
`decodePayload(args[0])` with **no** `now` at all — so the shipped CLI never
enforces payload expiry anywhere: an expired payload decodes fine.

PoC (run output, captured from an actual run — includes a real CLI invocation via `bin/tapfiber.js`):
```
=== F12 — expiry bypass: { now: NaN } passes the check, CLI never supplies now ===
decodePayload(payload, { now: NaN }) -> {"amountShannons":"100","asset":"CKB","expiresAt":1000,"invoice":"i","network":"testnet","orderId":"v"}
decodePayload(payload, { now: 99999 }) -> payload expired
CLI `tapfiber decode` on the same expired payload -> {"amountShannons":"100","asset":"CKB","expiresAt":1000,"invoice":"i","network":"testnet","orderId":"v"}
FINDING F12 FIRED — NaN now bypasses expiry; the CLI has no expiry check at all
```
Impact: a device that trusts `decodePayload` with a broken/NaN clock accepts
stale tap payloads; the CLI's `decode` is even weaker — no expiry check at
all.
Patch: `if (now !== undefined && !Number.isFinite(now)) throw new Error("now
must be a finite number")`; give the CLI a `--now <secs>` flag (or derive
`now` from the system clock with an explicit warning that expiry is not
checked without one).

## F13 — `verifyReceipt` does not validate the body schema; `issueReceipt` does not check key correspondence (MEDIUM) — PROVEN

`verifyReceipt` verifies the signature over whatever `receipt.body` is — a
signed `{garbage: 1}` or `{version: 1, orderId: "x"}` (no amount, no asset,
no network) verifies `valid: true` under the issuer's own key. Separately,
`issueReceipt(order, { privateKey, publicKey })` signs with `privateKey` but
emits `publicKey` without checking the two correspond — the PoC shows the
result is a receipt that verifies `bad_signature` against its own embedded
key: silently broken, not rejected at issue time.

PoC (run output, captured from an actual run):
```
=== F13 — receipt body is not schema-validated; issueReceipt does not check key correspondence ===
signed body {garbage:1} -> {"valid":true}
signed body {version:1,orderId:'x'} (no amount/asset/network) -> {"valid":true}
issueReceipt(privateKey=A, publicKey=B) -> {"valid":false,"reason":"bad_signature"}
FINDING F13 FIRED — no body schema check, no keypair correspondence check
```
Impact: a verifier that trusts `valid: true` can be handed a receipt whose
body carries none of the money fields; and a mis-wired issuer keypair fails
only at verification time, far from the bug.
Patch: in `verifyReceipt`, require the known body fields (`version`,
`orderId`, `amountShannons`, `asset`, `network`, ...) with expected types; in
`issueReceipt`, check the keypair corresponds (re-derive the public key
from the private key and compare, or sign/verify a known buffer and require
success) and throw otherwise.

## F14 — `validate()` accepts `expiresAt <= 0`, array amounts, and falsy required fields (MEDIUM) — PROVEN

`validate()` (src/payload.js) rejects only `undefined` and `""` on required
fields, then tests `String(amountShannons)` against `/^[1-9][0-9]*$/` and
`Number.isInteger(expiresAt)`. The gaps, all accepted by `encodePayload`:

| Input | Result |
|---|---|
| `expiresAt: 0` | ACCEPTED (integer, no range check) |
| `expiresAt: -1` | ACCEPTED |
| `amountShannons: ["100"]` | ACCEPTED — regex runs on `String(["100"])` = `"100"` |
| `orderId: null` | ACCEPTED (`null` is not `undefined`/`""`) |
| `invoice: false` | ACCEPTED |
| `asset: 0` | ACCEPTED |

PoC (run output, captured from an actual run):
```
=== F14 — payload validate() accepts expiresAt<=0, array amounts, falsy required fields ===
expiresAt: 0 -> ACCEPTED
expiresAt: -1 -> ACCEPTED
amountShannons: ['100'] -> ACCEPTED
orderId: null -> ACCEPTED
invoice: false -> ACCEPTED
asset: 0 -> ACCEPTED
FINDING F14 FIRED — validate() only checks undefined/empty-string, not type or range
```
Impact: payloads can be minted with negative/zero expiry (a meaningless
deadline) or array-typed amounts that stringify to a clean-looking number —
`decodePayload` then hands the array back to the caller, who may treat it as
a number.
Patch: type-check each required field (`typeof === "string"`, non-empty, no
arrays); require `expiresAt > 0`; validate the amount on the value itself
(assert `typeof string`, then `/^[1-9][0-9]*$/`), not on `String(value)`.

## On-chain evidence (mandatory section; N/A with one line if no such state)

N/A — target has no on-chain/persistent-asset interaction; findings terminate in code state, not txs.

## Layer boundary

This target is a pure in-memory JS library (no chain RPC, no vendor API, no
persistence layer of its own), so there is no chain-usage surface to test:
the findings terminate in the library's own code state (order objects,
receipt objects, decoded payload objects). No upstream chain/FNN issue was
tripped or claimed.

## Honest limitations

- This library is explicit about scope ("no network or storage layer; callers
  are responsible for persistence and event delivery"). F1/F3/F4/F5/F7/F9
  are foot-guns-in-the-defaults more than remote-exploitable bugs: the harm
  needs a hostile or sloppy *caller*. The value of the challenge is that the
  defaults are the safe side.
- No private keys, no funds, no network involved in any PoC — fully
  reproducible offline.
- All 14 findings are PROVEN: every one was executed and its output observed
  by `poc/attack.mjs` (exit 0, 14 FIRED / 0 UNPROVEN). No finding is left
  UNPROVEN or STRUCTURAL in this run.

## Reproduction

```
node tapfiber/poc/attack.mjs    # exit 0; 14 lines of "FINDING F<n> FIRED"
node tapfiber/test/core.test.js # 6/6 pass — the target's own tests still hold
```
The per-finding blocks above quote the lines they correspond to from one
captured run of `poc/attack.mjs`.
