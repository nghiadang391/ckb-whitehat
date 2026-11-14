// PATCHED ledger.js — the regression proof for F2.
//
// Identical API to the repo's server/services/ledger.js, but the
// check-then-act in credit()/debit() is wrapped in a transaction that locks
// the account row (SELECT ... FOR UPDATE) before the balance is read, so
// concurrent debits serialize instead of both passing the guard.
//
// This is exactly the fix the repo's own db.js primitives (withTransaction)
// make available — the bug is that ledger.js never used them.
//
// ---------------------------------------------------------------------------
// NESTING RULE (this is the part the original PoC got wrong)
// ---------------------------------------------------------------------------
// The target calls ledger.credit()/debit() from INSIDE an outer
// withTransaction() — e.g. server/index.js:1763, 2250/2257 and
// server/services/settlement.js:146 all pass the transaction's `client`.
//
// If ledgerMutation() opened its OWN withTransaction() in that case it would
// take a SECOND pool connection and a SECOND, INDEPENDENT transaction:
//   - pool starvation (the pool's max clients are finite, and every nested
//     call holds two connections instead of one), and
//   - broken rollback atomicity: the inner transaction would COMMIT its
//     ledger write even when the outer transaction later ROLLED BACK.
//
// So ledgerMutation() must REUSE the caller's client when that client is
// already inside a transaction. Detection is NOT a flag on the client (the
// target's withTransaction never sets one) — it asks the driver:
//
//   client.getTransactionStatus()  ->  'I' idle | 'T' in transaction | 'E' failed
//
// which is exactly what pg exposes for a connected Client. A pg Pool has no
// such method and is by definition not inside a transaction, so it takes the
// "open our own transaction" path.
//
// The same rule applies to the FOR UPDATE row lock: when we reuse the outer
// client we are already in the outer transaction, so the lock is held until
// the outer COMMIT/ROLLBACK — which is what makes the fix atomic end to end.
//
// ---------------------------------------------------------------------------

import { resolveTarget, targetImport } from './target.mjs'

const { repoRoot, serverDir } = resolveTarget()
const { withTransaction } = await targetImport(serverDir, 'db.js')

export { repoRoot, serverDir }

/** True when `client` is a pg client that is currently inside a transaction. */
export function isInTransaction(client) {
  if (!client) return false
  if (typeof client.getTransactionStatus === 'function') {
    const status = client.getTransactionStatus()
    return status === 'T' || status === 'E'
  }
  // A pg Pool is never inside a transaction (it hands out separate clients).
  return false
}

export async function ensureLedgerAccount(client, userId) {
  await client.query(
    `INSERT INTO ledger_accounts (user_id, balance_base_units)
     VALUES ($1, 0)
     ON CONFLICT (user_id) DO NOTHING`,
    [userId],
  )
}

export async function getBalance(client, userId) {
  const result = await client.query(
    `SELECT balance_base_units FROM ledger_accounts WHERE user_id = $1`,
    [userId],
  )
  return BigInt(result.rows[0]?.balance_base_units || '0')
}

// Core of the fix: run the whole entry+balance update atomically, with the
// account row locked for the duration of the transaction.
async function ledgerMutation(client, op) {
  // If the caller already holds a transactional client (in-transaction),
  // reuse it — so we join the caller's transaction instead of opening a
  // second connection and a second, independently-committing transaction.
  if (isInTransaction(client)) {
    return op(client)
  }
  return withTransaction((tx) => op(tx))
}

export async function credit(client, { userId, amount, sourceType, sourceId, metadata = {} }) {
  return ledgerMutation(client, async (c) => {
    await ensureLedgerAccount(c, userId)
    const inserted = await c.query(
      `INSERT INTO ledger_entries (user_id, direction, amount_base_units, source_type, source_id, metadata)
       VALUES ($1, 'credit', $2, $3, $4, $5)
       ON CONFLICT (source_type, source_id, direction, user_id) DO NOTHING
       RETURNING id`,
      [userId, amount.toString(), sourceType, sourceId, metadata],
    )
    if (inserted.rowCount === 0) return false
    await c.query(
      `UPDATE ledger_accounts
       SET balance_base_units = balance_base_units + $2, updated_at = now()
       WHERE user_id = $1`,
      [userId, amount.toString()],
    )
    return true
  })
}

export async function debit(client, { userId, amount, sourceType, sourceId, metadata = {} }) {
  return ledgerMutation(client, async (c) => {
    await ensureLedgerAccount(c, userId)
    // Lock the account row BEFORE reading the balance so two concurrent
    // debits cannot both observe the pre-deduction balance.
    const row = (await c.query(
      `SELECT balance_base_units FROM ledger_accounts WHERE user_id = $1 FOR UPDATE`,
      [userId],
    )).rows[0]
    const balance = BigInt(row?.balance_base_units || '0')
    if (balance < amount) throw new Error('Insufficient RUSD balance')

    const inserted = await c.query(
      `INSERT INTO ledger_entries (user_id, direction, amount_base_units, source_type, source_id, metadata)
       VALUES ($1, 'debit', $2, $3, $4, $5)
       ON CONFLICT (source_type, source_id, direction, user_id) DO NOTHING
       RETURNING id`,
      [userId, amount.toString(), sourceType, sourceId, metadata],
    )
    if (inserted.rowCount === 0) return false
    await c.query(
      `UPDATE ledger_accounts
       SET balance_base_units = balance_base_units - $2, updated_at = now()
       WHERE user_id = $1`,
      [userId, amount.toString()],
    )
    return true
  })
}
