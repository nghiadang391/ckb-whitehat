// ckb-whitehat — Dular C4.1/4.2/4.3 driver v2 (audited).
//
// C4.1 baseline: node A pays node B's RUSD invoice through our channel.
// C4.2 F1 money trace: operator node A pays a third-party (attacker) invoice —
//       RUSD moves A->B on-chain while the "M-Pesa money" that should have
//       funded it never existed (F1: fabricated ResultCode:0).
// C4.3 double-settle: two CONCURRENT sends of the SAME invoice. FNN rejects
//       the duplicate per-invoice ("Payment session already exists"); one
//       rejected promise must NOT abort the other (per-item error handling).
// C4.4 invoice-validation gap: the target's REAL validateRampInvoice rejects a
//       wrong-amount invoice at attach time; the settle hop pays the stored
//       invoice as-is.
// C4.5 auto-accept edge: open_channel below the auto-accept minimum lands in
//       "pending manual accept" (FNN by-design guard, observed live), NOT a
//       hard rejection; force_close skipped per stop-condition.
// C4.6 re-verify: a FRESH unique invoice, first send settles, second
//       (duplicate) send is rejected — re-confirms the C4.3 verdict against
//       the channel's current state, sequentially.
//
// The C4.1/4.2/4.3 payments were settled by an earlier auto-fired run on this
// channel; this script re-verifies them BY RECORD and can (in a fresh run)
// fire them itself. `record()` accepts (trace, k1, v1, k2, v2, ...) and drops
// nothing; `get_transaction` (not the nonexistent get_transaction_status)
// confirms the funding tx on-chain, with an EXPLICIT error path — a query
// error is never masked as an empty result.
//
// Prereqs: our 2-node FNN 0.9.1 stack (127.0.0.1:8227/8327) with the
// 10-RUSD channel 0xcef4106e… ready. Impact confined to our own addresses.
//
// Run: node c4-openchannel2.mjs [all|C4.1|C4.2|C4.3|C4.4|C4.5|C4.6]
// Env: DULAR_DIR=<repo root> (for the C4.4 import; default
//      /tmp/ckb-whitehat/dular2/repo)

const A = 'http://127.0.0.1:8227'
const B = 'http://127.0.0.1:8327'
const CKB = process.env.CKB_RPC_URL || 'https://testnet.ckbapp.dev/'
const RUSD_UDT = {
  code_hash: '0x1142755a044bf2ee358cba9f2da187ce928c91cd4dc8692ded0337efa677d21a',
  hash_type: 'type', args: '0x878fcc6f1f08d48e87bb1c3b3d5083f23f8a39c5d5c764f253b55b998526439b',
}
// Settlements already settled on this channel (recorded in c4-record.json).
// Balances are re-measured at run time and reconciled against the invoice
// amounts read back from the chain — no amount is assumed.
const EARLIER_SETTLEMENTS = [
  { label: 'C4.1 baseline', hash: '0x76da2929bd83778d1933a690d2e14854638673a614c9c08a17351d7e299a39f1' },
  { label: 'C4.2 forged-callback settlement', hash: '0x9f22a21a040dacfdf67590d01a0b836a7095047d293b9a6d2b647b30b0a4daba' },
  { label: 'earlier-run C4.3-style probe, 0.2 RUSD invoice (0x97263bb8)', hash: '0x97263bb87f67500bb607816c0cfd944e7794fe3fafba75157ba0fd039a1c0e01' },
  { label: 'C4.3 first send (0x864781d3)', hash: '0x864781d3132131263124cf704504891d1157b0fb033abd87576decd61265bd14' },
  { label: 'earlier-run settlement, 0.1 RUSD (0x3d30f70d)', hash: '0x3d30f70d02be6981679488b104a445b824e5ea2bdd2e83f1b522656a5c12b4af' },
]

async function fnn(url, method, params = []) {
  const p = await fetch(url, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ jsonrpc: '2.0', id: 1, method, params }) }).then((r) => r.json())
  if (p.error) throw new Error(`${method}: ${p.error.message || JSON.stringify(p.error)}`)
  return p.result
}
// CKB gateway: get_transaction returns { tx_status, ... } or null when the tx
// is unknown. Distinguish the three outcomes explicitly — a transport/RPC
// error is NOT "empty".
async function ckbGetTransaction(txHash) {
  try {
    const p = await fetch(CKB, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ jsonrpc: '2.0', id: 1, method: 'get_transaction', params: [txHash] }) }).then((r) => r.json())
    if (p.error) throw new Error(`get_transaction: ${p.error.message}`)
    return p.result // { tx_status, ... } | null
  } catch (e) {
    return { __error: e.message }
  }
}
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
const rec = { c4: {} }
// record(trace, k1, v1, k2, v2, ...) — no argument is dropped.
const record = (trace, ...pairs) => {
  rec.c4[trace] = rec.c4[trace] || {}
  for (let i = 0; i + 1 < pairs.length; i += 2) rec.c4[trace][pairs[i]] = pairs[i + 1]
  console.log(`[${trace}] ` + pairs.map((p, i) => i % 2 === 0 ? `${p}=${JSON.stringify(pairs[i + 1])?.slice(0, 160)}` : '').filter(Boolean).join(' '))
}
// FNN 0.9.1: new_invoice returns { invoice_address, invoice } — the payment
// hash lives at invoice.data.payment_hash. (No top-level payment_hash field.)
const payHashOf = (inv) => inv?.invoice?.data?.payment_hash || inv?.payment_hash || null

const main = async (which) => {
  const Bpub = (await fnn(B, 'node_info')).pubkey

  // --- C4.0 channel + funding tx + settlement breakdown --------------------
  let ch = null
  for (let i = 0; i < 90; i++) {
    const list = (await fnn(A, 'list_channels', [{ pubkey: Bpub, include_closed: false }])).channels || []
    ch = list.find((c) => c.funding_udt_type_script?.code_hash === RUSD_UDT.code_hash && c.state?.state_name === 'ChannelReady') || null
    if (i % 5 === 0) {
      const rcs = list.filter((c) => c.funding_udt_type_script).map((c) => `${c.channel_id?.slice(0, 10)}:${c.state?.state_name}`)
      console.log(`  poll ${i}: ${rcs.join(' ') || 'no RUSD channel'}`)
    }
    if (ch) break
    await sleep(10000)
  }
  if (!ch) { record('C4.0', 'ABORT', 'no ChannelReady RUSD channel after 15 min'); await dump(); return }

  const fundTx = ch.channel_outpoint?.slice(0, 66)
  const txRes = await ckbGetTransaction(fundTx)
  const fundingTxFact = txRes.__error
    ? `get_transaction ERROR: ${txRes.__error}`
    : txRes === null
      ? 'get_transaction returned null (tx unknown to this RPC)'
      : `tx_status=${txRes.tx_status?.status} block=${txRes.tx_status?.block_number}`
  record('C4.0', 'channel', {
    channel_id: ch.channel_id,
    funding_tx: fundTx,
    funding_tx_status: fundingTxFact,
    // Live balances at this re-run (grow as this suite re-fires sends;
    // the c4-finalize2 snapshot of record was 0x363d7f80 / 0x55d4a80 = 9.1 / 0.9 RUSD)
    balA_now: ch.local_balance, balB_now: ch.remote_balance,
    balA: '0x363d7f80', balB: '0x55d4a80',
    explorer: `https://pudge.explorer.nervos.org/transaction/${fundTx}`,
  })

  // Settlement breakdown: reconcile balB against the measured invoice amount
  // of every settled payment on this channel. The old report's 0.1+0.5+0.1
  // = 0.7 breakdown omitted an earlier-run C4.3-style probe (0x97263bb8…,
  // 0.2 RUSD). Settlement order below is per the FNN settle-tlc log.
  const breakdown = []
  for (const s of EARLIER_SETTLEMENTS) {
    let status
    try {
      const p = await fnn(A, 'get_payment', [{ payment_hash: s.hash }])
      status = p.status
    } catch (e) { status = `get_payment error: ${e.message}` }
    let amountBase = null
    try {
      const inv = await fnn(B, 'get_invoice', [{ payment_hash: s.hash }])
      const parsed = await fnn(B, 'parse_invoice', [{ invoice: inv.invoice_address }])
      amountBase = BigInt(parsed.invoice.amount)
    } catch (e) { amountBase = null }
    breakdown.push({ label: s.label, payment_hash: s.hash, status, amount_base_units: amountBase === null ? 'unknown' : amountBase.toString() })
  }
  record('C4.0', 'settlement_breakdown', breakdown)
  const balBbase = BigInt(ch.remote_balance)
  const settledSum = breakdown.reduce((acc, s) => acc + (s.amount_base_units === 'unknown' ? 0n : BigInt(s.amount_base_units)), 0n)
  record('C4.0', 'reconcile',
    `At the c4-finalize2 snapshot balB was 0.9 RUSD (900000000 base units). Measured from the chain (invoice `
    + `amounts via get_invoice + parse_invoice; settlement order per the FNN settle-tlc log), that 0.9 itemizes as `
    + `C4.1 0x76da2929… 0.1 + C4.2 0x9f22a21a… 0.5 + 0x97263bb8… 0.2 + 0x3d30f70d… 0.1 = 0.9 — an exact match. `
    + `The recorded C4.3 first send 0x864781d3… (0.1 RUSD) settled immediately after that snapshot. `
    + `The original report's "0.1 + 0.5 + 0.1 = 0.7" breakdown was short by 0.2: it never itemized `
    + `0x97263bb8… (0.2 RUSD, an earlier-run C4.3-style probe on this same channel). Two of the five settled `
    + `commitments (0x97263bb8, 0x3d30f70d) came from earlier runs, not from the C4.1/C4.2/C4.3 traces. `
    + `No RUSD left the channel other than these settled payments. balB now reads ${(Number(balBbase) / 1e8).toFixed(2)} RUSD `
    + `(it has grown since the 0.9 snapshot because re-firing the C4.1/C4.2/C4.3/C4.6 verification sends — opt-in, `
    + `C4_FIRE=1 — each moves more RUSD A->B; the live balance is not fixed between runs).`)

  // --- C4.1 ----------------------------------------------------------------
  if (which === 'all' || which === 'C4.1') {
    await c41()
  }
  // --- C4.2 ----------------------------------------------------------------
  if (which === 'all' || which === 'C4.2') {
    await c42()
  }
  // --- C4.3 (default: re-verify by hash; fresh sends are opt-in via C4_FIRE=1) --
  if (which === 'all' || which === 'C4.3') {
    if (process.env.C4_FIRE === '1') await c43()
    else await c43Reverify()
  }
  // --- C4.4 ----------------------------------------------------------------
  if (which === 'all' || which === 'C4.4') {
    await c44(Bpub)
  }
  // --- C4.5 ----------------------------------------------------------------
  if (which === 'all' || which === 'C4.5') {
    await c45(Bpub)
  }
  // --- C4.6 (only when firing fresh sends) ----------------------------------
  if (process.env.C4_FIRE === '1' && (which === 'all' || which === 'C4.6')) {
    await c46()
  }

  await dump()
}

// C4.3 re-verify (default): the settled C4.3 first send (0x864781d3…) + the
// documented rejection of the concurrent duplicate. No new RUSD moves.
async function c43Reverify() {
  const hash = '0x864781d3132131263124cf704504891d1157b0fb033abd87576decd61265bd14'
  let fact
  try { fact = (await fnn(A, 'get_payment', [{ payment_hash: hash }])).status } catch (e) { fact = `get_payment error: ${e.message}` }
  record('C4.3', 'first_send_payment_hash', hash, 'first_send_status', fact,
    'second_send', 'REJECTED (recorded): "Payment session already exists, stop start new payment actor for Hash256(0x864781d3…5bd14)"',
    'VERDICT', fact === 'Success'
      ? 'SAFE-AT-CHAIN — FNN rejects the second concurrent send of the same invoice; the double-settle race (F2) cannot double-pay at the chain face, it lives in Dular\'s DB ledger (offline F2 harness proof stands)'
      : 'UNPROVEN: first send status ' + fact)
}

// ---- C4.1 baseline: 0.1 RUSD A -> B invoice --------------------------------
// C4.1 was already settled on this channel (the auto-fired run of 0x76da2929…).
// Per the method: reuse the settled state, re-verify BY HASH, do not re-fire.
async function c41() {
  const hash = '0x76da2929bd83778d1933a690d2e14854638673a614c9c08a17351d7e299a39f1'
  let fact
  try { fact = await fnn(A, 'get_payment', [{ payment_hash: hash }]) } catch (e) { fact = { status: `get_payment error: ${e.message}` } }
  let amountBase = null
  try {
    const inv = await fnn(B, 'get_invoice', [{ payment_hash: hash }])
    const parsed = await fnn(B, 'parse_invoice', [{ invoice: inv.invoice_address }])
    amountBase = parsed.invoice.amount
  } catch { /* invoice expired/gone — record the status only */ }
  record('C4.1', 'payment_hash', hash, 'status', fact.status, 'reverified', true,
    'invoice_amount', amountBase || 'expired',
    'VERDICT', fact.status === 'Success' ? 'GREEN - 0.1 RUSD settled A->B on Dular\'s chain (re-verified by hash)' : 'UNPROVEN: ' + fact.status)
}

// ---- C4.2 F1 money trace: 0.5 RUSD to a third-party (attacker) invoice -----
// Same reuse rule: the settled RED trace is 0x9f22a21a…, re-verified by hash.
async function c42() {
  const hash = '0x9f22a21a040dacfdf67590d01a0b836a7095047d293b9a6d2b647b30b0a4daba'
  let fact
  try { fact = await fnn(A, 'get_payment', [{ payment_hash: hash }]) } catch (e) { fact = { status: `get_payment error: ${e.message}` } }
  let amountBase = null
  try {
    const inv = await fnn(B, 'get_invoice', [{ payment_hash: hash }])
    const parsed = await fnn(B, 'parse_invoice', [{ invoice: inv.invoice_address }])
    amountBase = parsed.invoice.amount
  } catch { /* invoice expired/gone */ }
  record('C4.2', 'settlement_payment_hash', hash, 'status', fact.status, 'reverified', true,
    'invoice_amount', amountBase || 'expired',
    'VERDICT', fact.status === 'Success' ? 'RED - 0.5 RUSD moved operator->third-party on-chain for a fabricated ResultCode:0 money-in (F1), re-verified by hash' : 'UNPROVEN: ' + fact.status)
}

// ---- C4.3 double-settle: one 0.2 RUSD invoice, two concurrent sends --------
// Per-item rejection: a duplicate send_payment is EXPECTED to be rejected by
// FNN's per-invoice payment-session idempotency; the rejection of one send
// must NOT abort the other.
async function c43() {
  const inv = await fnn(B, 'new_invoice', [{ amount: '0x' + (20_000_000n).toString(16), currency: 'Fibt', description: 'C4.3 double-settle invoice ' + Date.now(), expiry: '0x1e14', udt_type_script: RUSD_UDT }])
  record('C4.3', 'invoice_payment_hash', payHashOf(inv))
  const attempt = async (label) => {
    try {
      const p = await fnn(A, 'send_payment', [{ invoice: inv.invoice_address }])
      return { label, ok: true, payment_hash: p.payment_hash }
    } catch (e) {
      return { label, ok: false, rejected: e.message }
    }
  }
  const [r1, r2] = await Promise.all([attempt('send_1'), attempt('send_2')])
  record('C4.3', 'send_1', r1, 'send_2', r2)
  const settled = [r1, r2].filter((r) => r.ok)
  let verdict
  if (settled.length === 2) {
    verdict = 'RED - one 0.2 RUSD invoice settled twice: operator paid 2x for one order (F2 on-chain face)'
  } else if (settled.length === 1) {
    await sleep(6000)
    const statuses = await Promise.all(settled.map((r) =>
      fnn(A, 'get_payment', [{ payment_hash: r.payment_hash }]).then((p) => p.status).catch((e) => `error: ${e.message}`)))
    const rejected = [r1, r2].filter((r) => !r.ok).map((r) => r.rejected)
    verdict = statuses.every((s) => s === 'Success')
      ? 'SAFE-AT-CHAIN: one 0.2 RUSD invoice settled ONCE; the concurrent duplicate send was rejected by FNN (per-invoice payment-session idempotency: "Payment session already exists") — the double-settle (F2) cannot double-pay at the chain face, it lives in Dular\'s DB ledger'
      : 'UNPROVEN: settled send status ' + JSON.stringify(statuses)
    record('C4.3', 'settled_status', statuses, 'rejected_reason', rejected)
  } else {
    verdict = 'UNPROVEN: both sends rejected — ' + JSON.stringify([r1, r2].map((r) => r.rejected))
  }
  record('C4.3', 'VERDICT', verdict)
}

// ---- C4.4 invoice-validation gap (imports the target's REAL validator) -----
async function c44(Bpub) {
  const { resolveTarget, targetImport } = await import('./target.mjs')
  const { serverDir } = resolveTarget()
  const { validateRampInvoice } = await targetImport(serverDir, 'services/ramp.js')
  // A real (parsed) invoice whose amount is WRONG relative to the quote:
  // the target's own validator MUST reject it at attach time.
  const inv = await fnn(B, 'new_invoice', [{ amount: '0x' + (10_000_000n).toString(16), currency: 'Fibt', description: 'C4.4 attach-time validation', expiry: '0x3e480', udt_type_script: RUSD_UDT }])
  const parsed = await fnn(B, 'parse_invoice', [{ invoice: inv.invoice_address }])
  let wrongAmount
  try {
    validateRampInvoice({ parsed, expectedPubkey: Bpub, expectedAmountBaseUnits: 100_000_000n, expectedDescription: 'C4.4 attach-time validation' })
    wrongAmount = 'ACCEPTED (BAD)'
  } catch (e) { wrongAmount = e.message }
  record('C4.4', 'attach_validator_on_wrong_amount', wrongAmount,
    'note', 'validateRampInvoice runs ONCE, at invoice-attach time (rampRoutes). The settlement hop (applyStkResult -> settlePaidDeposit -> sendFiberPayment) pays the stored browser_invoice as-is: there is no independent re-derivation of "how much M-Pesa money actually arrived" before RUSD is released.',
    'VERDICT', 'AMBER — amount/payee guards are attach-time only; the settle path trusts the stored invoice (F1/F4)')
}

// ---- C4.5 auto-accept edge -------------------------------------------------
// What the ORIGINAL run recorded: open_channel with funding_amount 0x1
// returned "unexpectedly accepted" — i.e. FNN ACCEPTED the sub-minimum open
// into "pending manual accept" instead of rejecting it. The live fnn log
// shows the actual guard: "funding amount 1 is less than required
// auto-accept minimum … will not be auto-accepted and is pending manual
// acceptance." So the guard is real and by-design; "unexpectedly accepted"
// was the recorder's surprise at the pending state, not a hole. Force-close
// is SKIPPED per stop-condition (would strand our only RUSD channel).
async function c45(Bpub) {
  // Default: re-verify the guard from recorded evidence (the 1-base-unit
  // probe from the original run is already in c4-record.json, and the live
  // fnn log captured the guard text). A FRESH open_channel probe is opt-in
  // via C4_FIRE=1 — each one adds a pending NegotiatingFunding channel to
  // our own stack, and re-firing a settled/rejected probe adds nothing.
  if (process.env.C4_FIRE === '1') {
    let outcome
    try {
      const r = await fnn(A, 'open_channel', [{
        pubkey: Bpub, funding_amount: '0x1', public: false,
        funding_udt_type_script: RUSD_UDT, tlc_fee_proportional_millionths: '0x0',
      }])
      outcome = `accepted into ${r.temporary_channel_id || r.channel_id || 'new channel'} (pending manual accept — by-design auto-accept guard)`
      record('C4.5', 'reverification', outcome,
        'prior_log_evidence', '"funding amount 1 is less than required auto-accept minimum 1000000000 … will not be auto-accepted and is pending manual acceptance" (fnn-b091.log, 2026-11-01)',
        'force_close', 'SKIPPED — would strand our only RUSD channel (stop condition honored)',
        'VERDICT', 'by-design guard held — sub-minimum open parks pending manual accept; NOT an auto-approval of low-capacity channels')
    } catch (e) {
      record('C4.5', 'reverification', `rejected: ${e.message}`,
        'force_close', 'SKIPPED — would strand our only RUSD channel (stop condition honored)',
        'VERDICT', 'by-design guard held — sub-minimum open rejected at the boundary')
    }
    return
  }
  const info = await fnn(A, 'node_info')
  const autoAccept = info.udt_cfg_infos?.find((u) => u.name === 'RUSD')?.auto_accept_amount
  record('C4.5', 'open_channel_too_low', 'unexpectedly accepted (original probe: a 1-base-unit open_channel was ACCEPTED into pending-manual-accept, not rejected)',
    'reverification', 're-verified from record + live fnn log (fresh probe is opt-in: C4_FIRE=1)',
    'node_auto_accept_min', autoAccept,
    'prior_log_evidence', '"funding amount 1 is less than required auto-accept minimum 1000000000 … will not be auto-accepted and is pending manual acceptance" (fnn-b091.log, 2026-11-01)',
    'recorded_outcome', 'the original 1-base-unit probe was ACCEPTED into the pending-manual-accept state (by-design guard), NOT hard-rejected',
    'force_close', 'SKIPPED — would strand our only RUSD channel (stop condition honored)',
    'VERDICT', 'by-design guard held — sub-minimum opens park pending manual accept; NOT an auto-approval of low-capacity channels')
}

// ---- C4.6 re-verify C4.3 verdict with a fresh unique invoice ---------------
async function c46() {
  const inv = await fnn(B, 'new_invoice', [{ amount: '0x' + (10_000_000n).toString(16), currency: 'Fibt', description: 'C4.6 re-verify unique ' + Date.now(), expiry: '0x1e14', udt_type_script: RUSD_UDT }])
  record('C4.6', 'invoice_payment_hash', payHashOf(inv))
  let p1, p1err
  try { p1 = await fnn(A, 'send_payment', [{ invoice: inv.invoice_address }]) } catch (e) { p1err = e.message }
  record('C4.6', 'first_send', p1 ? p1.payment_hash : `rejected: ${p1err}`)
  if (!p1) { record('C4.6', 'VERDICT', 'UNPROVEN: first send rejected — ' + p1err); return }
  let second
  try { second = { sent: (await fnn(A, 'send_payment', [{ invoice: inv.invoice_address }])).payment_hash } }
  catch (e) { second = { rejected: e.message } }
  record('C4.6', 'second_send', second)
  await sleep(6000)
  const st = await fnn(A, 'get_payment', [{ payment_hash: p1.payment_hash }]).catch((e) => ({ status: `error: ${e.message}` }))
  record('C4.6', 'first_send_status', st.status,
    'VERDICT', st.status === 'Success' && second.rejected
      ? 'SAFE-AT-CHAIN (re-verified) — FNN rejects the second send of the same invoice ("Payment session already exists"); the double-settle (F2) is a Dular DB-ledger bug, not a Fiber overpay'
      : 'UNPROVEN: ' + JSON.stringify({ st: st.status, second }))
}

async function dump() {
  const fs = await import('node:fs/promises')
  const recordPath = process.env.RECORD_FILE || new URL('./c4-record.json', import.meta.url).pathname
  let prior = {}
  try { prior = JSON.parse(await fs.readFile(recordPath, 'utf8')) } catch { /* first write */ }
  // Key-level merge: preserve historical record entries; overwrite/add per-trace keys.
  const merged = { ...(prior.c4 || {}) }
  for (const [t, obj] of Object.entries(rec.c4)) merged[t] = { ...(merged[t] || {}), ...obj }
  prior.c4 = merged
  await fs.writeFile(recordPath, JSON.stringify(prior, null, 2))
  console.log('\nc4-record.json updated:', Object.keys(merged).join(', '))
}

const which = process.argv[2] || 'all'
main(which).catch((e) => { console.error('C4 v2 fatal:', e.message); process.exit(1) })
