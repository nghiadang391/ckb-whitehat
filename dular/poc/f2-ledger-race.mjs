// ckb-whitehat PoC — Dular: F2 double-spend race in server/services/ledger.js
//
// ledger.js debit() does: SELECT balance (no lock) -> guard -> INSERT entry
// (idempotency on source id only) -> UPDATE balance = balance - amount.
// No transaction, no FOR UPDATE. Two concurrent debits both read the same
// balance, both pass the guard, both insert (different source ids), both
// subtract. The primitives to fix this (withTransaction / withAdvisoryLock)
// already exist in server/db.js but are not used by ledger.js.
//
// Four parts:
//   A. Deterministic interleaving of the EXACT SQL debit() issues, run by two
//      clients (this is the race the app's own concurrent debits hit).
//   B. Real-module corroboration: N parallel calls to the actual debit()
//      exported by the repo, real Postgres, pool clients, no transaction.
//   C. Fix proof: the same Part B run against a patched copy of ledger.js
//      (withTransaction + SELECT ... FOR UPDATE) — invariant now holds.
//   D. Nesting/atomicity proof for the fix: a THROWING inner ledger mutation
//      inside an OUTER withTransaction must roll the outer transaction back —
//      i.e. the fix must not open a second, independently-committing
//      transaction (which would silently commit the inner write).
//
// All DB work targets an ISOLATED scratch database on our own local Postgres,
// created from the target's own schema.sql and dropped on teardown. No
// network, no live service, and no row can survive into a shared database.
//
// Run: node f2-ledger-race.mjs
// Env:  DULAR_DIR=<repo root>   (or a server/ dir — both accepted)

import { fileURLToPath } from 'node:url'
import { resolveTarget, targetImport, targetPg, ensureScratchDb, dropScratchDb, line } from './target.mjs'

const { repoRoot, serverDir } = resolveTarget()
const db = await ensureScratchDb(repoRoot, { name: 'dular_poc_f2' })
process.env.DATABASE_URL = db.url

// ---------------------------------------------------------------- part A
async function partA() {
  line('F2-A — deterministic interleaving of debit() SQL (two clients, same funds)')
  const { pool } = await targetImport(serverDir, 'db.js')

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
  const { pool } = await targetImport(serverDir, 'db.js')
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

// ---------------------------------------------------------------- part D
// Atomicity of the fix under nesting: the target calls ledger.debit() from
// INSIDE an outer withTransaction(). The fix must JOIN that transaction, not
// open its own — otherwise the inner write commits independently and the
// outer ROLLBACK does not undo it.
//
// Check: outer withTransaction -> debit() -> throw. After the outer rollback
// the ledger balance and entries must be UNCHANGED. (With a naive
// "always open my own transaction" helper, the debit would have committed and
// the balance would show the deduction.)
async function partD(ledgerPath, label) {
  line(`F2-D — ${label}: throwing inner debit inside an outer withTransaction must roll back`)
  const { pool, withTransaction } = await targetImport(serverDir, 'db.js')
  const ledger = await import(ledgerPath)

  const uid = '33333333-3333-3333-3333-333333333333'
  await pool.query('DELETE FROM ledger_entries WHERE user_id = $1', [uid])
  await pool.query('DELETE FROM ledger_accounts WHERE user_id = $1', [uid])
  await pool.query('INSERT INTO users (id, phone) VALUES ($1, $2) ON CONFLICT DO NOTHING', [uid, '+254700000006'])
  await pool.query('INSERT INTO ledger_accounts (user_id, balance_base_units) VALUES ($1, 1000)', [uid])

  const readState = async () => {
    const bal = (await pool.query('SELECT balance_base_units FROM ledger_accounts WHERE user_id = $1', [uid])).rows[0].balance_base_units
    const sum = (await pool.query('SELECT COALESCE(SUM(amount_base_units),0) AS s FROM ledger_entries WHERE user_id = $1 AND direction = $2', [uid, 'debit'])).rows[0].s
    return { bal: String(bal), sum: String(sum) }
  }

  const before = await readState()
  let threw = false
  try {
    await withTransaction(async (client) => {
      await ledger.debit(client, {
        userId: uid, amount: 600n,
        sourceType: 'phone_payment', sourceId: 'nested-throw-1',
      })
      throw new Error('simulated failure AFTER the ledger mutation')
    })
  } catch (e) {
    threw = true
    console.log(`outer transaction threw as designed: ${e.message}`)
  }
  const after = await readState()
  const rolledBack = before.bal === after.bal && before.sum === after.sum
  console.log(`balance before=${before.bal} after=${after.bal} | posted debits before=${before.sum} after=${after.sum}`)
  console.log(rolledBack
    ? `ATOMICITY HELD (${label}): the inner debit joined the outer transaction and was rolled back with it`
    : `ATOMICITY BROKEN (${label}): the inner debit committed independently of the outer rollback`)

  // Pool starvation check: how many connections does the nested path hold?
  const { Client } = targetPg(repoRoot)
  const probe = new Client({ connectionString: db.url })
  await probe.connect()
  const inTx = await probe.query('SELECT count(*)::int AS c FROM pg_stat_activity WHERE datname = $1', [db.name])
  console.log(`backend connections open on the scratch DB during this run: ${inTx.rows[0].c}`)
  await probe.end()

  return { threw, rolledBack }
}

// ---------------------------------------------------------------- part E
// Negative control for Part D: the ORIGINAL helper shape (a client.__inTransaction
// flag that the target's withTransaction never sets) opens a SECOND
// independently-committing transaction on the pool. Run the same throw-after-debit
// and show the balance survives the outer rollback — i.e. the naive version
// breaks atomicity, so Part D is a genuine discriminator, not a tautology.
async function partE() {
  line('F2-E — negative control: the original flag-based helper breaks nesting atomicity')
  const { pool, withTransaction } = await targetImport(serverDir, 'db.js')

  const ensureLedgerAccount = async (client, userId) => {
    await client.query(`INSERT INTO ledger_accounts (user_id, balance_base_units) VALUES ($1, 0) ON CONFLICT (user_id) DO NOTHING`, [userId])
  }
  // The original fixed-ledger.js helper, verbatim (the flag is never set by
  // the target's withTransaction, so nested calls always open a new
  // connection + transaction).
  const naiveMutation = (client, op) => {
    if (client.__inTransaction) return op(client)
    return withTransaction((tx) => { tx.__inTransaction = true; return op(tx) })
  }
  const naiveDebit = async (client, { userId, amount, sourceType, sourceId }) =>
    naiveMutation(client, async (c) => {
      await ensureLedgerAccount(c, userId)
      const row = (await c.query(`SELECT balance_base_units FROM ledger_accounts WHERE user_id = $1 FOR UPDATE`, [userId])).rows[0]
      if (BigInt(row?.balance_base_units || '0') < amount) throw new Error('Insufficient RUSD balance')
      const ins = await c.query(`INSERT INTO ledger_entries (user_id, direction, amount_base_units, source_type, source_id) VALUES ($1, 'debit', $2, $3, $4) ON CONFLICT (source_type, source_id, direction, user_id) DO NOTHING RETURNING id`, [userId, amount.toString(), sourceType, sourceId])
      if (ins.rowCount === 0) return false
      await c.query(`UPDATE ledger_accounts SET balance_base_units = balance_base_units - $2, updated_at = now() WHERE user_id = $1`, [userId, amount.toString()])
      return true
    })

  const uid = '44444444-4444-4444-4444-444444444444'
  await pool.query('DELETE FROM ledger_entries WHERE user_id = $1', [uid])
  await pool.query('DELETE FROM ledger_accounts WHERE user_id = $1', [uid])
  await pool.query('INSERT INTO users (id, phone) VALUES ($1, $2) ON CONFLICT DO NOTHING', [uid, '+254700000008'])
  await pool.query('INSERT INTO ledger_accounts (user_id, balance_base_units) VALUES ($1, 1000)', [uid])

  let threw = false
  try {
    await withTransaction(async (client) => {
      await naiveDebit(client, { userId: uid, amount: 600n, sourceType: 'phone_payment', sourceId: 'naive-throw-1' })
      throw new Error('simulated failure AFTER the ledger mutation')
    })
  } catch { threw = true }
  const bal = (await pool.query('SELECT balance_base_units FROM ledger_accounts WHERE user_id = $1', [uid])).rows[0].balance_base_units
  const sum = (await pool.query('SELECT COALESCE(SUM(amount_base_units),0) AS s FROM ledger_entries WHERE user_id = $1 AND direction = $2', [uid, 'debit'])).rows[0].s
  console.log(`outer threw=${threw} | balance after outer rollback = ${bal} | posted debits = ${sum}`)
  const broke = bal !== '1000' || sum !== '0'
  console.log(broke
    ? 'EXPECTED — the flag-based helper committed its debit in a SECOND transaction that the outer rollback never undid (pool +1 connection, atomicity broken)'
    : 'UNEXPECTED — naive version held atomicity; Part D would not discriminate')
  return broke
}

// ---------------------------------------------------------------- main
const main = async () => {
  console.log(`target: ${repoRoot}`)
  console.log(`scratch DB: ${db.url.replace(/:[^:@/]+@/, ':***@')} (isolated, dropped on teardown)`)
  const firedA = await partA()
  const firedB = await partB(`${serverDir}/services/ledger.js`, 'UNFIXED ledger.js')
  const fixedPath = process.env.POC_DIR || fileURLToPath(new URL('./fixed-ledger.js', import.meta.url))
  const fixedB = await partB(fixedPath, 'FIXED ledger.js (withTransaction + FOR UPDATE)')
  const d = await partD(fixedPath, 'FIXED ledger.js')
  const naiveBroke = await partE()
  const { pool } = await targetImport(serverDir, 'db.js')
  await pool.end()

  console.log('\n=== F2 verdict ===')
  console.log(`deterministic interleave fired: ${firedA}`)
  console.log(`real-module parallel race violated invariant: ${firedB}`)
  console.log(`fixed version violated invariant: ${fixedB} (expected false)`)
  console.log(`fixed version preserves outer-rollback atomicity under nesting: ${d.rolledBack}`)
  console.log(`flag-based (original helper) version broke nesting atomicity: ${naiveBroke} (expected true — negative control)`)
  if (firedB && !fixedB && d.rolledBack && naiveBroke) console.log('FINDING F2 FIRED — ledger.js debit() is racy; the patched module closes it and stays atomic when nested.')
  else if (!firedB) console.log('NOTE: parallel run did not fire this time (race is probabilistic); Part A is the deterministic proof.')
  else console.log('FINDING F2 UNPROVEN')
  return (firedA && firedB && !fixedB && d.rolledBack && naiveBroke) ? 0 : (firedA ? 0 : 1)
}

let exitCode = 0
main()
  .then((code) => { exitCode = code })
  .catch((e) => { console.error('F2 PoC error:', e); exitCode = 2 })
  .finally(async () => {
    // Tear the scratch DB down BEFORE exiting so no PoC row survives.
    await dropScratchDb(db)
    process.exit(exitCode)
  })
