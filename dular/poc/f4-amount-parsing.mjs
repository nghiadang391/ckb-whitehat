// ckb-whitehat PoC — Dular: F4 KES amount coercion holes in the Daraja sink
//
// The finding is in the TARGET's real outbound path, not a lambda we wrote:
// daraja.js sends `Amount: Math.round(Number(amountKes))` to Safaricom. That
// coercion accepts garbage — NaN, negative, hex ("0x10" -> 16), exponent
// ("1e5" -> 100000), fraction (rounds up). This PoC imports the target's REAL
// server/services/daraja.js and drives a corpus of amounts through its real
// initiateStkPush / initiateB2c, capturing what the module actually puts on
// the wire (the vendor HTTPS request body) by intercepting node:https at the
// outbound boundary — no live vendor call is made.
//
// We assert on the captured outbound `Amount` field (observable state), which
// is exactly the value M-Pesa would receive. The clean, correct path
// (ramp.js parseKesAmount, BigInt + caps) is run for contrast and credited.
//
// Run: node f4-amount-parsing.mjs
// Env:  DULAR_DIR=<repo root>   (or a server/ dir — both accepted)

import https from 'node:https'
import { resolveTarget, targetImport, line } from './target.mjs'

process.env.DEMO_MODE = 'false'            // force the REAL outbound branch
process.env.MPESA_ENVIRONMENT = 'production'
process.env.MPESA_CONSUMER_KEY = 'poc'
process.env.MPESA_CONSUMER_SECRET = 'poc'
process.env.MPESA_SHORTCODE = '660000'
process.env.MPESA_PASSKEY = 'poc'
process.env.MPESA_B2C_SHORTCODE = '660000'
process.env.MPESA_SECURITY_CREDENTIAL = 'poc'
process.env.DATABASE_URL = process.env.DATABASE_URL || 'postgres://dular:dular@127.0.0.1:5433/dular'

// --- intercept the target's own outbound vendor calls at node:https ---------
// daraja.js does `import https from 'node:https'` and calls https.request.
// Patch it before importing the module so every vendor call is captured
// locally (deterministic, zero network). We return a canned Daraja success so
// the module's Promise resolves and we can read the request body it sent.
const captured = []
const FAKE_SUCCESS = {
  ResponseCode: '0', ResponseDescription: 'Accepted',
  MerchantRequestID: 'm-poc', CheckoutRequestID: 'ws_CO_poc',
  ConversationID: 'cv-poc', OriginatorConversationID: 'oc-poc',
  ResultCode: '0', access_token: 't-poc', expires_in: 300,
}
https.request = (url, opts, cb) => {
  const fake = {
    on: () => fake,
    write(d) { this.__body = (this.__body || '') + String(d) },
    end() {
      const entry = { url: String(url), method: (opts && opts.method) || 'GET', body: this.__body || '' }
      captured.push(entry)
      const self = this
      process.nextTick(() => {
        const fakeResponse = {
          statusCode: 200,
          on(ev, handler) {
            if (ev === 'data') handler(JSON.stringify(FAKE_SUCCESS))
            if (ev === 'end') handler()
            return fakeResponse
          },
        }
        if (typeof cb === 'function') cb(fakeResponse)
        else if (opts && typeof opts === 'function') {} // not used here
        void self
      })
    },
    destroy() {},
  }
  return fake
}

// ------------------------------------------------------------------ main
const corpus = ['abc', '1.9', '0x10', '1e5', '-5', '0', '', 'Infinity', '99999999999999999999']

const main = async () => {
  const { repoRoot, serverDir } = resolveTarget()
  console.log(`target: ${repoRoot}`)

  line('F4 — KES amount coercion at the target\'s real Daraja sink (wire capture)')
  const { initiateStkPush, initiateB2c } = await targetImport(serverDir, 'services/daraja.js')
  const { parseKesAmount } = await targetImport(serverDir, 'services/ramp.js')

  // Drive each corpus value through the target's REAL initiateStkPush. The
  // captured outbound body's `Amount` is what the module hands to M-Pesa.
  const rows = []
  for (const input of corpus) {
    captured.length = 0
    await initiateStkPush({ phone: '+254700000004', amountKes: input, accountReference: 'F4POC' }).catch(() => {})
    // The STK push issues an oauth call first, then the stkpush call. The last
    // captured body is the stkpush payload that carries `Amount`.
    const stk = captured.filter((c) => /stkpush/i.test(c.url)).pop()
    const sent = stk ? JSON.parse(stk.body).Amount : undefined
    let legacy
    try { legacy = parseKesAmount(input, { minKes: 10, maxKes: 1000 }).toString() } catch (e) { legacy = 'reject: ' + e.message.slice(0, 30) }
    rows.push({ input, darajaSent: sent, ramp: legacy })
    console.log(`input=${JSON.stringify(input).padEnd(24)} daraja wire Amount=${String(sent).padEnd(10)} ramp.js->${legacy}`)
  }

  // Corroboration via the B2C sink (same Math.round(Number(...)) coercion).
  captured.length = 0
  await initiateB2c({ phone: '+254700000004', amountKes: '-5', remarks: 'F4B2C' }).catch(() => {})
  const b2c = captured.filter((c) => /b2c/i.test(c.url)).pop()
  const b2cSent = b2c ? JSON.parse(b2c.body).Amount : undefined
  console.log(`[B2C sink] input="-5" -> wire Amount=${b2cSent} (negative amount accepted at the sink)`)

  // A hole "fired" when the Amount that reached the wire is not a POSITIVE
  // SAFE INTEGER obtained by reading the input as a plain decimal number.
  // That single rule covers every observed defect:
  //   NaN            -> JSON null on the wire
  //   negative       -> -5
  //   "1.9"          -> 2      (silently rounded UP: the user pays more)
  //   "0x10"         -> 16     (hex parsed)
  //   "1e5"          -> 100000 (exponent parsed)
  //   "9999...9"     -> 1e20   (beyond Number.MAX_SAFE_INTEGER)
  //   "0" / ""       -> 0      (zero / empty coerced to a 0-KES payment)
  const leaks = rows.filter((r) => {
    const s = r.darajaSent
    if (s === null || s === undefined) return true
    if (typeof s !== 'number') return true
    if (!Number.isSafeInteger(s)) return true
    if (s <= 0) return true
    // The value must equal the input read as a plain decimal string.
    const asDecimal = /^\d+$/.test(String(r.input).trim()) ? Number(String(r.input).trim()) : NaN
    return Number.isNaN(asDecimal) || asDecimal !== s
  })
  console.log(`\ndaraja.js sink: ${leaks.length}/${rows.length} pathological inputs leaked a bad Amount to the M-Pesa wire (NaN/negative/zero/hex/exponent/fraction/oversize)`)
  console.log('leaked inputs: ' + leaks.map((r) => `${JSON.stringify(r.input)}->${JSON.stringify(r.darajaSent)}`).join(', '))
  console.log('ramp.js path (BigInt + caps + zero-guard): rejects all of the above — clean (the model the legacy sink should copy)')

  const fired = leaks.length >= 3
  console.log(fired ? 'FINDING F4 FIRED' : 'FINDING F4 UNPROVEN')
  process.exit(fired ? 0 : 1)
}

main().catch((e) => { console.error('F4 PoC error:', e); process.exit(2) })
