import crypto from "node:crypto";

export function canonical(v) {
  if (Array.isArray(v)) return `[${v.map(canonical).join(",")}]`;
  if (v && typeof v === "object") {
    return `{${Object.keys(v).sort().map((k) => `${JSON.stringify(k)}:${canonical(v[k])}`).join(",")}}`;
  }
  return JSON.stringify(v);
}

const b64u = (buf) => Buffer.from(buf).toString("base64url");
const check = (s) => crypto.createHash("sha256").update(s).digest("hex").slice(0, 8);

const REQUIRED = ["orderId", "amountShannons", "asset", "network", "invoice", "expiresAt"];

function validate(p) {
  for (const k of REQUIRED) if (p?.[k] === undefined || p[k] === "") throw new Error(`missing field: ${k}`);
  if (!/^[1-9][0-9]*$/.test(String(p.amountShannons))) throw new Error("amountShannons must be a positive integer string");
  if (!Number.isInteger(p.expiresAt)) throw new Error("expiresAt must be unix seconds (integer)");
}

export function encodePayload(p) {
  validate(p);
  const body = b64u(canonical({ ...p, amountShannons: String(p.amountShannons) }));
  return `tapfiber:1:${body}.${check(body)}`;
}

export function decodePayload(str, { now } = {}) {
  const m = /^tapfiber:1:([A-Za-z0-9_-]+)\.([0-9a-f]{8})$/.exec(str.trim());
  if (!m) throw new Error("not a tapfiber v1 payload");
  if (check(m[1]) !== m[2]) throw new Error("checksum mismatch");
  const p = JSON.parse(Buffer.from(m[1], "base64url").toString("utf8"));
  validate(p);
  if (now !== undefined && now > p.expiresAt) throw new Error("payload expired");
  return p;
}
