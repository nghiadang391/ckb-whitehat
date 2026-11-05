// PATCHED ledger.js — the regression proof for F2.
//
// Identical API to the repo's server/services/ledger.js, but the
// check-then-act in credit()/debit() is wrapped in a transaction that locks
// the account row (SELECT ... FOR UPDATE) before the balance is read, so
// concurrent debits serialize instead of both passing the guard.
//
// This is exactly the fix the repo's own db.js primitives (withTransaction)
// make available — the bug is that ledger.js never used them.

import { withTransaction } from '/tmp/ckb-whitehat/dular/server/db.js'

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
  // reuse it; otherwise open a short transaction around the mutation.
  if (client.__inTransaction) {
    return op(client)
  }
  return withTransaction((tx) => { tx.__inTransaction = true; return op(tx) })
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
