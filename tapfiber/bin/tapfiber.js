#!/usr/bin/env node
import fs from "node:fs";
import { decodePayload, verifyReceipt } from "../src/index.js";

const [cmd, ...args] = process.argv.slice(2);
const out = (x) => console.log(typeof x === "string" ? x : JSON.stringify(x, null, 2));

if (cmd === "decode") {
  out(decodePayload(args[0]));
} else if (cmd === "verify") {
  const rc = JSON.parse(fs.readFileSync(args[0], "utf8"));
  const key = args[1] ? fs.readFileSync(args[1], "utf8") : undefined;
  const r = verifyReceipt(rc, { expectedPublicKey: key });
  out(r);
  process.exitCode = r.valid ? 0 : 1;
} else {
  out("usage:\n  tapfiber decode <payload>\n  tapfiber verify <receipt.json> [merchant-public-key.pem]");
  process.exitCode = cmd ? 1 : 0;
}
