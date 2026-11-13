// F2 — Ledger debit race. services/ledger.js debit():
//   const balance = await getBalance(client, userId)   // plain SELECT
//   if (balance < amount) throw ...
//   INSERT ledger_entries ... ; UPDATE ledger_accounts SET balance = balance - $2
// No FOR UPDATE on ledger_accounts, so N concurrent withdrawals each read the stale
// balance, all pass the check, and the account goes NEGATIVE. /api/mpesa/withdraw
// (index.js:1738-1774) fires initiateB2c BEFORE the debit, so in production every
// accepted debit is a real KES payout; extra accepted debits under the race = free KES.
//
// PoC runs the target's own db.js + ledger.js against a scratch DB: 20 RUSD account,
// 16 parallel 10 RUSD withdrawals. Honest floor: 2 accepted, final 0.
// Race: final < 0, or Σdebits != balance delta. Loop rounds until observed.
//
// With --patched, same PoC but the debit is the guarded single-statement form
// (the proposed fix); the race can no longer drive balance negative.
import { pg } from './deps.mjs'
import { ensureScratchDb } from './dbsetup.mjs'

const PATCHED = process.argv.includes('--patched')
const REPO = '/tmp/ckb-whitehat/dular2/repo/server'
const DB = await ensureScratchDb()

process.env.DATABASE_URL = DB
process.env.DEMO_MODE = 'true'

const { pool } = await import(`${REPO}/db.js`)
const { ensureLedgerAccount } = await import(`${REPO}/services/ledger.js`)
const { Client } = pg

// Target's withTransaction (verbatim semantics from server/db.js)
async function withTransaction(cb) {
  const client = await pool.connect()
  try {
    await client.query('BEGIN')
    const result = await cb(client)
    await client.query('COMMIT')
    return result
  } catch (error) {
    await client.query('ROLLBACK')
    throw error
  } finally { client.release() }
}

const FIXED_DEBIT = async (client, { userId, amount, sourceType, sourceId, metadata = {} }) => {
  await ensureLedgerAccount(client, userId)
  const inserted = await client.query(
    `INSERT INTO ledger_entries (user_id, direction, amount_base_units, source_type, source_id, metadata)
     VALUES ($1,'debit',$2,$3,$4,$5)
     ON CONFLICT (source_type, source_id, direction, user_id) DO NOTHING
     RETURNING id`, [userId, amount.toString(), sourceType, sourceId, metadata])
  if (inserted.rowCount === 0) return false
  const guarded = await client.query(
    `UPDATE ledger_accounts
     SET balance_base_units = balance_base_units - $2, updated_at = now()
     WHERE user_id = $1 AND balance_base_units >= $2
     RETURNING balance_base_units`, [userId, amount.toString()])
  if (!guarded.rows[0]) {
    // reject the already-inserted entry atomically
    await client.query(`DELETE FROM ledger_entries WHERE id = $1`, [inserted.rows[0].id])
    throw new Error('Insufficient RUSD balance')
  }
  return true
}

const c = new Client({ connectionString: DB })
await c.connect()
const { rows: [u] } = await c.query(`INSERT INTO users (phone, verified_at) VALUES ('+254700000002', now()) ON CONFLICT (phone) DO UPDATE SET verified_at = now() RETURNING id`)
await c.query(`DELETE FROM ledger_entries WHERE user_id=$1`, [u.id])
await c.query(`DELETE FROM ledger_accounts WHERE user_id=$1`, [u.id])
await ensureLedgerAccount(c, u.id)

const B = 2_000_000_000n // 20 RUSD
const A = 1_000_000_000n // 10 RUSD

function attempt(n, salt, debit) {
  return withTransaction(async (client) => {
    // same call shape as /api/mpesa/withdraw (index.js:1747-1770): insert tx row, then debit
    const created = await client.query(
      `INSERT INTO mpesa_transactions (user_id, kind, phone, kes_amount, rusd_base_units, status, conversation_id)
       VALUES ($1,'withdrawal','+254700000002',100,100000000,'pending',$2) RETURNING id`,
      [u.id, `${PATCHED ? 'wph' : 'wpr'}-${salt}-${n}`])
    return debit(client, { userId: u.id, amount: A, sourceType: 'mpesa_withdrawal', sourceId: created.rows[0].id })
  }).then(() => 'accepted').catch((e) => e.message === 'Insufficient RUSD balance' ? 'rejected' : `error:${e.message}`)
}

const targetLedger = (await import(`${REPO}/services/ledger.js`)).debit
let anyNegative = false
let anyMismatch = false
for (let round = 1; round <= 25; round += 1) {
  await c.query(`UPDATE ledger_accounts SET balance_base_units=$2 WHERE user_id=$1`, [u.id, B.toString()])
  await c.query(`DELETE FROM ledger_entries WHERE user_id=$1`, [u.id])
  await c.query(`DELETE FROM mpesa_transactions WHERE conversation_id LIKE '${PATCHED ? 'wph' : 'wpr'}%'`)
  const results = await Promise.all(Array.from({ length: 16 }, (_, i) => attempt(i, round, PATCHED ? FIXED_DEBIT : targetLedger)))
  const accepted = results.filter(r => r === 'accepted').length
  const final = (await c.query(`SELECT balance_base_units::text AS b FROM ledger_accounts WHERE user_id=$1`, [u.id])).rows[0].b
  const entries = (await c.query(
    `SELECT coalesce(sum(amount_base_units),0)::text AS s FROM ledger_entries WHERE user_id=$1 AND direction='debit'`, [u.id])).rows[0].s
  const consistent = BigInt(entries) === B - BigInt(final)
  if (BigInt(final) < 0n) anyNegative = true
  if (!consistent) anyMismatch = true
  console.log(`round ${String(round).padStart(2)}: accepted=${accepted}/16 final=${final} debits=${entries} ${consistent ? 'consistent' : 'INCONSISTENT'}` + (BigInt(final) < 0n ? ' [NEGATIVE]' : ''))
  if (!PATCHED && (anyNegative || anyMismatch)) break
  if (PATCHED && round >= 3 && !anyMismatch && !anyNegative) break
}

await c.end()
await pool.end()
if (PATCHED) {
  console.log(anyNegative || anyMismatch
    ? 'FIXED-DEBIT REGRESSION FAILED — still inconsistent'
    : 'PASS (patched) — guarded debit held the invariant across all rounds; balance never went negative')
} else {
  console.log(anyNegative
    ? 'F2 FIRED — target debit() drove the account balance NEGATIVE under concurrent withdrawals.'
    : anyMismatch
      ? 'F2 FIRED — ledger entries no longer reconcile with the balance under concurrency.'
      : 'F2 NOT observed this run — re-run (the race is timing-dependent; 25 rounds at 16-way parallelism)')
}
