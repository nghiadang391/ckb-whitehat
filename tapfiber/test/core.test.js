import { test } from "node:test";
import assert from "node:assert/strict";
import { encodePayload, decodePayload, createOrder, applyEvent, generateKeys, issueReceipt, verifyReceipt } from "../src/index.js";

const base = { orderId: "o1", amountShannons: "1000000000", asset: "CKB", network: "testnet", invoice: "fibt1abc", expiresAt: 2000, paymentHash: "0xaa" };
const confirm = (o = {}) => ({ type: "payment_confirmed", paymentHash: "0xaa", amountShannons: "1000000000", asset: "CKB", network: "testnet", at: 1500, ...o });

test("payload round-trips and fits an NFC tag", () => {
  const s = encodePayload({ ...base, amountShannons: 1000000000 });
  assert.ok(s.length < 400);
  assert.deepEqual(decodePayload(s), base);
});

test("payload rejects corruption, bad amounts and expiry", () => {
  const s = encodePayload(base);
  assert.throws(() => decodePayload(s.replace(/.$/, s.endsWith("0") ? "1" : "0")), /checksum/);
  assert.throws(() => decodePayload("hello"), /not a tapfiber/);
  assert.throws(() => encodePayload({ ...base, amountShannons: "-5" }), /positive integer/);
  assert.throws(() => encodePayload({ ...base, invoice: "" }), /missing field/);
  assert.throws(() => decodePayload(s, { now: 2001 }), /expired/);
});

test("matching confirmation pays the order exactly once", () => {
  const r1 = applyEvent(createOrder(base), confirm());
  assert.equal(r1.order.state, "PAID");
  assert.ok(r1.changed);
  const r2 = applyEvent(r1.order, confirm({ at: 1600 })); // replay
  assert.equal(r2.changed, false);
  assert.deepEqual(r2.order, r1.order);
});

test("altered amount, asset, network, hash or late confirmation is rejected", () => {
  const o = createOrder(base);
  for (const [patch, reason] of [
    [{ amountShannons: "1" }, "amount_mismatch"],
    [{ asset: "RUSD" }, "asset_mismatch"],
    [{ network: "mainnet" }, "network_mismatch"],
    [{ paymentHash: "0xbb" }, "payment_hash_mismatch"],
    [{ at: 2001 }, "confirmed_after_expiry"],
  ]) {
    const r = applyEvent(o, confirm(patch));
    assert.equal(r.order.state, "AWAITING_PAYMENT");
    assert.equal(r.rejected, reason);
  }
});

test("expiry, failure and late payments", () => {
  const o = createOrder(base);
  assert.equal(applyEvent(o, { type: "tick", now: 2000 }).order.state, "AWAITING_PAYMENT");
  const expired = applyEvent(o, { type: "tick", now: 2001 }).order;
  assert.equal(expired.state, "EXPIRED");
  const late = applyEvent(expired, confirm());
  assert.equal(late.order.state, "EXPIRED"); // never fulfilled
  assert.equal(late.rejected, "late_confirmation_on_expired");
  assert.equal(applyEvent(o, { type: "payment_failed" }).order.state, "FAILED");
});

test("receipts: only for PAID, verify offline, detect tampering and wrong issuer", () => {
  const keys = generateKeys();
  assert.throws(() => issueReceipt(createOrder(base), keys), /AWAITING_PAYMENT/);
  const paid = applyEvent(createOrder(base), confirm()).order;
  const rc = issueReceipt(paid, keys);
  assert.deepEqual(verifyReceipt(rc), { valid: true });
  assert.deepEqual(verifyReceipt(rc, { expectedPublicKey: keys.publicKey }), { valid: true });

  const tampered = structuredClone(rc);
  tampered.body.amountShannons = "1";
  assert.deepEqual(verifyReceipt(tampered), { valid: false, reason: "bad_signature" });

  assert.equal(verifyReceipt(rc, { expectedPublicKey: generateKeys().publicKey }).reason, "unexpected_issuer");
  assert.equal(verifyReceipt({}).reason, "malformed");
});
