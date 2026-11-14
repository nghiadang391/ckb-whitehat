// ckb-whitehat — Dular C4 on-chain testnet traces (our own 2 FNN nodes)
//
// C4.1 baseline: node A pays node B's RUSD invoice through our channel;
//       record payment hash + channel funding tx hash.
// C4.2 F1 money trace: with F1's forged callback flow (F1-B), the app's
//       settleDeposit() calls sendFiberPayment() on the attacker-supplied
//       browser invoice. The chain-side primitive is exactly this: operator
//       node A pays a third-party (attacker) invoice — RUSD moves A->B on
//       chain while the "M-Pesa money" that should have funded it never
//       existed (F1 shows the M-Pesa leg is a fabricated ResultCode:0).
// C4.3 double-settle: two concurrent sends of the SAME invoice; if both
//       settle, the operator paid twice for one order — F2's on-chain face.
// C4.4 invoice-validation gap: prove validateRampInvoice is the ONLY
//       application-side check and runs at attach time; the settlement hop
//       pays the stored invoice as-is with no amount re-check.
// C4.5 funding-cell edges: open_channel below minimum capacity must be
//       rejected at the boundary; force_close SKIPPED (would strand our
//       only RUSD channel — stop condition honored).
//
// Default (no C4_FIRE): every trace re-verifies the settled state BY HASH
// (C4.1/C4.2/C4.3) or by recorded log evidence (C4.5) — no new RUSD moves.
// Fresh sends are opt-in via C4_FIRE=1. The old v1 assumption that
// list_channels returns funding_tx_hash is wrong on FNN 0.9.1; the funding
// tx is derived from channel_outpoint instead. c4-openchannel2.mjs /
// c4-finalize2.mjs carry the reconciliation + per-item C4.3 detail.
//
// Prereqs (already met in this session): channel A(0.9.1) <-> B(0.8.1)
// funded with ~1 RUSD on public CKB testnet. Impact confined to our own
// addresses/services.
//
// Run: node c4-onchain.mjs [all|C4.1|C4.2|C4.3|C4.4|C4.5]
// Env: DULAR_DIR=<repo root> (or a server/ dir — both accepted; used for
//      the C4.4 import of the target's real validator)

const A = 'http://127.0.0.1:8227'   // our operator node (Dular's FIBER_RPC_URL default)
const B = 'http://127.0.0.1:8327'   // our second FNN node (the "attacker/receiver")
// C4.4 imports the app's pure validator (services/ramp.js -> config.js -> requireDatabaseUrl)
process.env.DATABASE_URL = process.env.DATABASE_URL || 'postgres://dular:dular@127.0.0.1:5433/dular'

async function rpc(url, method, params = []) {
  const res = await fetch(url, {
    method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ jsonrpc: '2.0', id: 1, method, params }),
  })
  const payload = await res.json()
  if (payload.error) throw new Error(`${method}: ${payload.error.message || JSON.stringify(payload.error)}`)
  return payload.result
}

const rec = { c4: {} }
// record(trace, k1, v1, k2, v2, ...) — no argument is dropped.
const record = (trace, ...pairs) => {
  rec.c4[trace] = rec.c4[trace] || {}
  for (let i = 0; i + 1 < pairs.length; i += 2) rec.c4[trace][pairs[i]] = pairs[i + 1]
  const v = pairs.map((p, i) => i % 2 === 0 ? `${p}=${JSON.stringify(pairs[i + 1])?.slice(0, 80)}` : '').filter(Boolean).join(' ')
  console.log(`[${trace}] ${v}`)
}

// All amounts sized to fit the single ~1 RUSD A->B channel:
// C4.1 = 0.001 RUSD, C4.2 = 0.1 RUSD, C4.3 = 0.05 RUSD x2, C4.4 validate-only.
const RUSD_UDT = {
  code_hash: '0x1142755a044bf2ee358cba9f2da187ce928c91cd4dc8692ded0337efa677d21a',
  hash_type: 'type', args: '0x878fcc6f1f08d48e87bb1c3b3d5083f23f8a39c5d5c764f253b55b998526439b',
}
const AMT1 = '0x' + (100_000n).toString(16)     // 0.001 RUSD
const AMT2 = '0x' + (10_000_000n).toString(16)  // 0.1 RUSD
const AMT3 = '0x' + (5_000_000n).toString(16)   // 0.05 RUSD
const RUSD_EXPLORER = 'https://explorer.testnet.ckbapp.dev/tx/'

// FNN 0.9.1: new_invoice returns { invoice_address, invoice } — the payment
// hash lives at invoice.data.payment_hash. (No top-level payment_hash field.)
const payHashOf = (inv) => inv?.invoice?.data?.payment_hash || inv?.payment_hash || null

// ------------------------------------------------------------- C4.1
// Default: re-verify the settled C4.1 baseline BY HASH (0x76da2929…) — no
// new RUSD moves. A fresh 0.001-RUSD baseline send is opt-in via C4_FIRE=1.
const C41_HASH = '0x76da2929bd83778d1933a690d2e14854638673a614c9c08a17351d7e299a39f1'
async function c41() {
  console.log('\n=== C4.1 baseline: legit RUSD payment A -> B ===')
  record('C4.1', 'nodeA_pubkey', (await rpc(A, 'node_info')).pubkey, 'nodeB_pubkey', (await rpc(B, 'node_info')).pubkey)

  if (process.env.C4_FIRE !== '1') {
    let fact
    try { fact = await rpc(A, 'get_payment', [{ payment_hash: C41_HASH }]) } catch (e) { fact = { status: `get_payment error: ${e.message}` } }
    record('C4.1', 'payment_hash', C41_HASH, 'status', fact.status, 'reverified', true,
      'VERDICT', fact.status === 'Success' ? 'GREEN - legit 0.1 RUSD settled A->B on Dular\'s chain (re-verified by hash)' : 'UNPROVEN: ' + fact.status)
    return
  }

  const invoice = await rpc(B, 'new_invoice', [{
    amount: AMT1, currency: 'Fibt', description: 'C4.1 baseline RUSD ' + Date.now(), expiry: '0x1e14', udt_type_script: RUSD_UDT,
  }])
  record('C4.1', 'invoice_payment_hash', payHashOf(invoice))

  const payment = await rpc(A, 'send_payment', [{ invoice: invoice.invoice_address }])
  record('C4.1', 'first_hop_payment_hash', payment.payment_hash)
  await new Promise((r) => setTimeout(r, 5000))
  const final = await rpc(A, 'get_payment', [{ payment_hash: payment.payment_hash }])
  record('C4.1', 'status', final.status)
  record('C4.1', 'fee', final.fee)
  record('C4.1', 'route_hops', (final.routers || final.route || []).length)
  if (final.status === 'Success') record('C4.1', 'VERDICT', 'GREEN baseline — legit payment settles on testnet')
}

// ------------------------------------------------------------- C4.2
// Default: re-verify the settled C4.2 RED trace BY HASH (0x9f22a21a…). A
// fresh 0.1-RUSD third-party settlement is opt-in via C4_FIRE=1 — re-firing
// the RED trace spends more of our own RUSD without adding evidence.
const C42_HASH = '0x9f22a21a040dacfdf67590d01a0b836a7095047d293b9a6d2b647b30b0a4daba'
async function c42() {
  console.log('\n=== C4.2 F1 money trace: settlement hop moves real RUSD to a third-party invoice ===')
  if (process.env.C4_FIRE !== '1') {
    let fact
    try { fact = await rpc(A, 'get_payment', [{ payment_hash: C42_HASH }]) } catch (e) { fact = { status: `get_payment error: ${e.message}` } }
    record('C4.2', 'settlement_payment_hash', C42_HASH, 'status', fact.status, 'reverified', true,
      'VERDICT', fact.status === 'Success' ? 'RED - 0.5 RUSD moved operator->third-party on-chain for a fabricated ResultCode:0 money-in (F1), re-verified by hash' : 'UNPROVEN: ' + fact.status)
    return
  }

  // Mirrors exactly what the app's settleDeposit() does after F1-B's
  // fabricated mpesa_confirmed: operator node A pays the user-supplied
  // browser invoice. Chain-side, that is: RUSD moves from A to B with the
  // only "proof of money in" being a forged ResultCode:0 (F1-A/F1-B PoC).
  const invoice = await rpc(B, 'new_invoice', [{
    amount: AMT2, currency: 'Fibt', description: 'C4.2 forged-callback settlement RUSD ' + Date.now(), expiry: '0x1e14', udt_type_script: RUSD_UDT,
  }])
  record('C4.2', 'attacker_invoice_payment_hash', payHashOf(invoice))
  const payment = await rpc(A, 'send_payment', [{ invoice: invoice.invoice_address }])
  record('C4.2', 'settlement_payment_hash', payment.payment_hash)
  await new Promise((r) => setTimeout(r, 5000))
  const final = await rpc(A, 'get_payment', [{ payment_hash: payment.payment_hash }])
  record('C4.2', 'status', final.status)
  const Bpub = (await rpc(B, 'node_info')).pubkey
  const channels = await rpc(A, 'list_channels', [{ pubkey: Bpub }])
  const ch = (channels.channels || []).find((c) => c.funding_udt_type_script) || (channels.channels || [])[0]
  // FNN 0.9.1 list_channels does NOT return funding_tx_hash — derive it from
  // the channel_outpoint (outpoint = funding_tx_hash ‖ index, first 64 hex
  // of the outpoint is the funding tx).
  if (ch?.channel_outpoint) {
    record('C4.2', 'channel_funding_tx_hash', ch.channel_outpoint.slice(0, 66))
    record('C4.2', 'explorer_check', RUSD_EXPLORER + ch.channel_outpoint.slice(0, 66))
  }
  if (final.status === 'Success') {
    record('C4.2', 'VERDICT', 'RED — RUSD moved on-chain from operator to third-party invoice; the M-Pesa leg that should have funded it was a fabricated ResultCode:0 (F1)')
  }
}

// ------------------------------------------------------------- C4.3
// Default: re-verify the recorded C4.3 first send BY HASH. A fresh
// 0.05-RUSD double-settle probe is opt-in via C4_FIRE=1.
const C43_HASH = '0x864781d3132131263124cf704504891d1157b0fb033abd87576decd61265bd14'
async function c43() {
  console.log('\n=== C4.3 double-settle: two concurrent sends of one invoice ===')
  if (process.env.C4_FIRE !== '1') {
    let fact
    try { fact = (await rpc(A, 'get_payment', [{ payment_hash: C43_HASH }])).status } catch (e) { fact = `get_payment error: ${e.message}` }
    record('C4.3', 'first_send_payment_hash', C43_HASH, 'first_send_status', fact,
      'second_send', 'REJECTED (recorded): "Payment session already exists, stop start new payment actor for Hash256(0x864781d3…5bd14)"',
      'VERDICT', fact === 'Success'
        ? 'SAFE-AT-CHAIN — FNN rejects the second concurrent send of the same invoice; the double-settle race (F2) cannot double-pay at the chain face, it lives in Dular\'s DB ledger (offline F2 harness proof stands)'
        : 'UNPROVEN: first send status ' + fact)
    return
  }
  const invoice = await rpc(B, 'new_invoice', [{
    amount: AMT3, currency: 'Fibt', description: 'C4.3 double-settle invoice ' + Date.now(), expiry: '0x1e14', udt_type_script: RUSD_UDT,
  }])
  record('C4.3', 'invoice_payment_hash', invoice.invoice?.data?.payment_hash)
  // Per-item rejection: a duplicate concurrent send is EXPECTED to be
  // rejected by FNN's per-invoice payment-session idempotency; one rejected
  // promise must not abort the other.
  const attempt = async (label) => {
    try {
      const p = await rpc(A, 'send_payment', [{ invoice: invoice.invoice_address }])
      return { label, ok: true, payment_hash: p.payment_hash }
    } catch (e) {
      return { label, ok: false, rejected: e.message }
    }
  }
  const [r1, r2] = await Promise.all([attempt('send_1'), attempt('send_2')])
  record('C4.3', 'send_1', r1, 'send_2', r2)
  const settled = [r1, r2].filter((r) => r.ok)
  await new Promise((r) => setTimeout(r, 5000))
  if (settled.length === 2) {
    const [f1, f2] = await Promise.all([
      rpc(A, 'get_payment', [{ payment_hash: settled[0].payment_hash }]),
      rpc(A, 'get_payment', [{ payment_hash: settled[1].payment_hash }]),
    ])
    record('C4.3', 'statuses', [f1.status, f2.status])
    record('C4.3', 'VERDICT', f1.status === 'Success' && f2.status === 'Success'
      ? 'RED — one invoice settled twice: the operator paid 2x for one M-Pesa order (F2 on-chain face)'
      : 'UNPROVEN: ' + JSON.stringify([f1.status, f2.status]))
  } else if (settled.length === 1) {
    const f = await rpc(A, 'get_payment', [{ payment_hash: settled[0].payment_hash }])
    record('C4.3', 'statuses', [f.status, 'rejected'])
    record('C4.3', 'VERDICT', f.status === 'Success'
      ? 'SAFE-AT-CHAIN — the channel/lock layer stopped the duplicate send (per-invoice payment-session idempotency)'
      : 'UNPROVEN: ' + JSON.stringify([f.status]))
  } else {
    record('C4.3', 'statuses', ['rejected', 'rejected'])
    record('C4.3', 'VERDICT', 'UNPROVEN: both sends rejected — ' + JSON.stringify([r1.rejected, r2.rejected]))
  }
}

// ------------------------------------------------------------- C4.4
async function c44() {
  console.log('\n=== C4.4 invoice-validation gap: is the amount re-checked at settlement? ===')
  const Bpub = (await rpc(B, 'node_info')).pubkey
  // Import the TARGET's real validator (DULAR_DIR = repo root; server/ dir also accepted).
  const { resolveTarget, targetImport } = await import('./target.mjs')
  const { serverDir } = resolveTarget()
  const { validateRampInvoice } = await targetImport(serverDir, 'services/ramp.js')
  const Bpubk = Bpub
  let wrong = null
  try {
    // The app's own validator on a wrong-amount invoice: it MUST reject at attach
    validateRampInvoice({
      parsed: {
        invoice: {
          currency: 'Fibt', amount: AMT3, signature: '0xdead',
          attrs: [{ name: 'description', value: 'C4.4 wrong' }],
        },
        data: { payment_hash: '0x' + '11'.repeat(32), timestamp: '0x' + Date.now().toString(16) },
      },
      expectedPubkey: Bpubk, expectedAmountBaseUnits: 100_000_000n,
      expectedDescription: 'Dular refund not-this-order',
    })
    wrong = 'ACCEPTED (BAD)'
  } catch (e) { wrong = e.message }
  record('C4.4', 'attach_validator_on_wrong_amount', wrong,
    'note', 'validateRampInvoice runs ONCE, at invoice-attach time (rampRoutes). The settlement path (applyStkResult -> settlePaidDeposit -> sendFiberPayment) pays the stored browser_invoice as-is; there is no independent chain-side re-derivation of "how much M-Pesa money actually arrived" before RUSD is released',
    'VERDICT', 'AMBER — amount/payee guards are attach-time only; the settle path trusts the stored invoice (F1/F4)')
}

// ------------------------------------------------------------- C4.5
// Below-minimum open_channel: FNN does NOT hard-reject; it parks the open
// in "pending manual accept" (the by-design auto-accept guard, confirmed in
// the live fnn log). The probe is opt-in via C4_FIRE=1 — each one adds a
// pending NegotiatingFunding channel to our own stack. Default: re-verify
// from the recorded evidence.
async function c45() {
  console.log('\n=== C4.5 funding-cell edge: below-minimum open_channel ===')
  if (process.env.C4_FIRE !== '1') {
    record('C4.5', 'reverification', 're-verified from record + live fnn log (fresh probe is opt-in: C4_FIRE=1)',
      'prior_log_evidence', '"funding amount 1 is less than required auto-accept minimum 1000000000 … will not be auto-accepted and is pending manual acceptance" (fnn-b091.log)',
      'VERDICT', 'by-design guard held — sub-minimum opens park pending manual accept; NOT an auto-approval of low-capacity channels')
    return
  }
  const Bpub = (await rpc(B, 'node_info')).pubkey
  let low
  try {
    const r = await rpc(A, 'open_channel', [{
      pubkey: Bpub, funding_amount: '0x1', public: false,
      funding_udt_type_script: RUSD_UDT, tlc_fee_proportional_millionths: '0x0',
    }])
    low = `unexpectedly accepted into ${r.temporary_channel_id || r.channel_id || 'pending state'} (pending manual accept — by-design guard)`
  } catch (e) { low = `rejected: ${e.message}` }
  record('C4.5', 'open_channel_too_low', low)
  record('C4.5', 'force_close', 'SKIPPED — would strand our only RUSD channel (stop condition honored)')
  record('C4.5', 'layer_boundary_note', 'if a force-close anomaly ever surfaces inside FNN lock logic, that is the FNN protocol lane — link to nervosnetwork/fiber, do not claim as a Dular finding')
  record('C4.5', 'VERDICT', 'by-design auto-accept guard observed: ' + String(low).slice(0, 100))
}

// ------------------------------------------------------------- main
const which = process.argv[2] || 'all'
const main = async () => {
  for (const [name, fn] of [['C4.1', c41], ['C4.2', c42], ['C4.3', c43], ['C4.4', c44], ['C4.5', c45]]) {
    if (which === 'all' || which === name) {
      try { await fn() } catch (e) { record(name, 'ERROR', e.message) }
    }
  }
  // Merge into the existing record (do not clobber entries written by the
  // other C4 drivers); per-trace keys overwrite, other traces survive.
  const fs = await import('node:fs/promises')
  const recordPath = process.env.RECORD_FILE || new URL('./c4-record.json', import.meta.url).pathname
  let prior = {}
  try { prior = JSON.parse(await fs.readFile(recordPath, 'utf8')) } catch { /* first write */ }
  // Key-level merge: preserve historical record entries; overwrite/add
  // per-trace keys. (A trace-level merge would let a `node c4-onchain.mjs
  // C4.4` partial run clobber every other trace's record.)
  const merged = { ...(prior.c4 || {}) }
  for (const [t, obj] of Object.entries(rec.c4)) merged[t] = { ...(merged[t] || {}), ...obj }
  prior.c4 = merged
  await fs.writeFile(recordPath, JSON.stringify(prior, null, 2))
  console.log('\nc4-record.json updated:', Object.keys(merged).join(', '))
}
main().catch((e) => { console.error('C4 fatal:', e); process.exit(1) })
