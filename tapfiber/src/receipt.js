import crypto from "node:crypto";
import { canonical } from "./payload.js";

export function generateKeys() {
  const { publicKey, privateKey } = crypto.generateKeyPairSync("ed25519");
  return {
    publicKey: publicKey.export({ type: "spki", format: "pem" }),
    privateKey: privateKey.export({ type: "pkcs8", format: "pem" }),
  };
}

export function issueReceipt(order, { privateKey, publicKey }) {
  if (order.state !== "PAID") throw new Error(`cannot issue receipt for ${order.state} order`);
  const body = {
    version: 1,
    orderId: order.orderId,
    paymentHash: order.paymentHash,
    network: order.network,
    asset: order.asset,
    amountShannons: order.amountShannons,
    paidAt: order.paidAt,
  };
  const signature = crypto.sign(null, Buffer.from(canonical(body)), privateKey).toString("base64");
  return { body, publicKey, signature };
}

export function verifyReceipt(receipt, { expectedPublicKey } = {}) {
  try {
    if (expectedPublicKey && receipt.publicKey.trim() !== expectedPublicKey.trim()) return { valid: false, reason: "unexpected_issuer" };
    const ok = crypto.verify(null, Buffer.from(canonical(receipt.body)), receipt.publicKey, Buffer.from(receipt.signature, "base64"));
    return ok ? { valid: true } : { valid: false, reason: "bad_signature" };
  } catch {
    return { valid: false, reason: "malformed" };
  }
}
