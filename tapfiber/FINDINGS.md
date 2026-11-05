# ckb-whitehat — tapfiber-core v0.1.0 security challenge

Target: github.com/0xVida/tapfiber (MIT, pure JS, no deps). All PoCs run against
unmodified source: `node poc/attack.mjs`. No network, no mainnet, no keys needed.

Severity = impact on a merchant using this for paid access, weighted by how a
real attacker would feed `applyEvent`.

## S1 — Post-expiry payment accepted when `event.at` is omitted (HIGH)

`applyEvent` (src/order.js): the expiry check is `if (event.at > order.expiresAt)`.
Because `event.at` is optional, any caller (webhook bug, malicious adapter) can
confirm a payment after the order's deadline by simply not sending `at`.

PoC (run output):
```
with at=5000  -> confirmed_after_expiry     (correct)
at stripped   -> PAID => receipt issuable   (bug)
```
Impact: an expired tap-to-pay order can still be marked PAID and issue a valid
receipt — a "grace period" no one asked for.
Patch: reject when `event.at === undefined` (or require it and derive from event
time), i.e. `if (event.at === undefined || event.at > order.expiresAt)`.

## S2 — `expiresAt` is not enforced by `createOrder` (MEDIUM)

`createOrder` accepts orders without `expiresAt` (payload.js enforces it for the
payload, but `createOrder` does not — and receipts come from orders, not payloads).
An unexpired order can accept confirmations with `at` up to
`Number.MAX_SAFE_INTEGER` (PoC prints ~year 285,000).
Impact: any integration that builds orders directly skips expiry entirely.
Patch: make `expiresAt` required in `createOrder`, mirroring `validate()`.

## S3 — Unpinned `paymentHash` accepts any payment (HIGH, integration-dependent)

In `applyEvent`, the paymentHash check is `if (order.paymentHash && ...)` — a
skip-if-absent. Orders created without `paymentHash` (legal per S2-adjacent
defaults) mark PAID on *any* confirmation with matching amount/asset/network.
Impact: a merchant that omits the hash trades payment binding for
"some payment of that amount happened" — a swapped/duplicate payment can be
claimed as the order's payment.
Patch: require `paymentHash` in `createOrder` unless the integration explicitly
opts into amount-only matching (document the risk).

## S4 — `verifyReceipt` defaults to accepting any issuer key (HIGH by-default)

`verifyReceipt(receipt)` with no `expectedPublicKey` returns `{valid: true}` for
any self-signed receipt. The PoC forges a receipt with a fresh Ed25519 key and
it verifies clean. The CLI's `verify` command makes this worse: with no key file
argument, exit code 0 means "valid" even for a random key.
Impact: a consumer that wires `verifyReceipt(rcpt)` without pinning the merchant
key accepts receipts signed by anyone — "proof of payment" becomes free to forge.
Patch: make `expectedPublicKey` required (throw when absent), and make the CLI
error out when the key file is omitted.

## S5 — Payload integrity is 32 bits, not a signature (MEDIUM, acknowledged)

`check()` is the first 8 hex chars of sha256(body). A tamperer who rewrites
`amountShannons` must re-find a body whose sha256 starts with 8 hex digits:
2^32 work — hours on one CPU core, seconds–minutes on a GPU. The README states
this is "not a signature"; the risk is that NFC/magnetic-tap handoffs trust this
string end-to-end.
Patch: sign the payload with the merchant key (the keys already exist for
receipts) or lengthen the check and treat mismatches as hard failures at the
device boundary, not "corruption".

## S6 — Receipt body omits `invoice` and `expiresAt` (LOW–MEDIUM)

`issueReceipt` signs `{version, orderId, paymentHash, network, asset,
amountShannons, paidAt}`. The signature proves "amount X was paid" but not the
order's expiry window or invoice reference — a proof that's weaker than the order
it describes.
Patch: add the relevant order fields to the signed body.

## S7 — Receipts have no nonce/TTL: infinite replay (MEDIUM)

`verifyReceipt` has no notion of "already redeemed". The same receipt verifies
forever (PoC: 3/3 replays all `valid:true`). If a receipt ever gates access
or a refund, one capture = unlimited use.
Patch: add a one-time `nonce` field + a consumer-side redemption log, or an
`expiresAt` on the receipt body (also fixes part of S6).

## Notes / honest limitations

- This library is explicit about scope ("no network or storage layer; callers
  are responsible for persistence and event delivery"). S1/S4/S7 are
  foot-guns-in-the-defaults more than remote-exploitable bugs: the harm needs a
  hostile or sloppy *caller*. The value of the challenge is that the defaults are
  the safe side.
- No private keys, no funds, no network involved in any PoC — fully
  reproducible offline.
