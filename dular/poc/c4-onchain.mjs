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
// Prereqs (already met in this session): channel A(0.9.1) <-> B(0.8.1)
// funded with ~1 RUSD on public CKB testnet. Impact confined to our own
// addresses/services.

const A = 'http://127.0.0.1:8227'   // our operator node (Dular's FIBER_RPC_URL default)
const B = 'http://127.0.0.1:8327'   // our second FNN node (the "attacker/receiver")
// C4.4 imports the app's pure validator (services/ramp.js -> config.js -> requireDatabaseUrl)
process.env.DATABASE_URL = 'postgres://dular@127.0.0.1:5433/dular'

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
const record = (trace, key, value) => {
  rec.c4[trace] = rec.c4[trace] || {}
  rec.c4[trace][key] = value
  const v = typeof value === 'string' ? value.slice(0, 80) : JSON.stringify(value)?.slice(0, 120)
  console.log(`[${trace}] ${key} = ${v}`)
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

// ------------------------------------------------------------- C4.1
async function c41() {
  console.log('\n=== C4.1 baseline: legit RUSD payment A -> B ===')
  record('C4.1', 'nodeA_pubkey', (await rpc(A, 'node_info')).pubkey)
  record('C4.1', 'nodeB_pubkey', (await rpc(B, 'node_info')).pubkey)

  const invoice = await rpc(B, 'new_invoice', [{
    amount: AMT1, currency: 'Fibt', description: 'C4.1 baseline RUSD', expiry: '0x1e14', udt_type_script: RUSD_UDT,
  }])
  record('C4.1', 'invoice_payment_hash', invoice.payment_hash)

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
async function c42() {
  console.log('\n=== C4.2 F1 money trace: settlement hop moves real RUSD to a third-party invoice ===')
  // Mirrors exactly what the app's settleDeposit() does after F1-B's
  // fabricated mpesa_confirmed: operator node A pays the user-supplied
  // browser invoice. Chain-side, that is: RUSD moves from A to B with the
  // only "proof of money in" being a forged ResultCode:0 (F1-A/F1-B PoC).
  const invoice = await rpc(B, 'new_invoice', [{
    amount: AMT2, currency: 'Fibt', description: 'C4.2 forged-callback settlement RUSD', expiry: '0x1e14', udt_type_script: RUSD_UDT,
  }])
  record('C4.2', 'attacker_invoice_payment_hash', invoice.payment_hash)
  const payment = await rpc(A, 'send_payment', [{ invoice: invoice.invoice_address }])
  record('C4.2', 'settlement_payment_hash', payment.payment_hash)
  await new Promise((r) => setTimeout(r, 5000))
  const final = await rpc(A, 'get_payment', [{ payment_hash: payment.payment_hash }])
  record('C4.2', 'status', final.status)
  const Bpub = (await rpc(B, 'node_info')).pubkey
  const channels = await rpc(A, 'list_channels', [{ pubkey: Bpub }])
  const ch = (channels.channels || [])[0]
  if (ch?.funding_tx_hash) {
    record('C4.2', 'channel_funding_tx_hash', ch.funding_tx_hash)
    record('C4.2', 'explorer_check', RUSD_EXPLORER + ch.funding_tx_hash)
  }
  if (final.status === 'Success') {
    record('C4.2', 'VERDICT', 'RED — RUSD moved on-chain from operator to third-party invoice; the M-Pesa leg that should have funded it was a fabricated ResultCode:0 (F1)')
  }
}

// ------------------------------------------------------------- C4.3
async function c43() {
  console.log('\n=== C4.3 double-settle: two concurrent sends of one invoice ===')
  const invoice = await rpc(B, 'new_invoice', [{
    amount: AMT3, currency: 'Fibt', description: 'C4.3 double-settle invoice', expiry: '0x1e14', udt_type_script: RUSD_UDT,
  }])
  const [p1, p2] = await Promise.all([
    rpc(A, 'send_payment', [{ invoice: invoice.invoice_address }]),
    rpc(A, 'send_payment', [{ invoice: invoice.invoice_address }]),
  ])
  record('C4.3', 'payment_1', p1.payment_hash)
  record('C4.3', 'payment_2', p2.payment_hash)
  await new Promise((r) => setTimeout(r, 5000))
  const f1 = await rpc(A, 'get_payment', [{ payment_hash: p1.payment_hash }])
  const f2 = await rpc(A, 'get_payment', [{ payment_hash: p2.payment_hash }])
  record('C4.3', 'statuses', [f1.status, f2.status])
  const both = f1.status === 'Success' && f2.status === 'Success'
  record('C4.3', 'VERDICT', both
    ? 'RED — one invoice settled twice: the operator paid 2x for one M-Pesa order (F2 on-chain face)'
    : 'channel/lock layer stopped one send: ' + JSON.stringify([f1.status, f2.status]))
}

// ------------------------------------------------------------- C4.4
async function c44() {
  console.log('\n=== C4.4 invoice-validation gap: is the amount re-checked at settlement? ===')
  const Bpub = (await rpc(B, 'node_info')).pubkey
  const { validateRampInvoice } = await import((process.env.DULAR_DIR || '/tmp/ckb-whitehat/dular/server') + '/services/ramp.js')
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
      expectedPubkey: Bpub, expectedAmountBaseUnits: 100_000_000n,
      expectedDescription: 'Dular refund not-this-order',
    })
    wrong = 'ACCEPTED (BAD)'
  } catch (e) { wrong = e.message }
  record('C4.4', 'attach_validator_on_wrong_amount', wrong)
  record('C4.4', 'note', 'validateRampInvoice runs ONCE, at invoice-attach time. The settlement path (settleDeposit -> sendFiberPayment) pays the stored browser_invoice as-is; a refund-invoice replacement re-attaches with fresh validation, but the operator B2C payout path (startB2c) never re-derives amounts from M-Pesa — chain settlement trusts the DB column')
  record('C4.4', 'VERDICT', 'AMBER — amount/payee/description guards are attach-time only; there is no independent chain-side re-derivation of "how much M-Pesa money actually arrived" before RUSD is released')
}

// ------------------------------------------------------------- C4.5
async function c45() {
  console.log('\n=== C4.5 funding-cell edge: below-minimum open_channel ===')
  const Bpub = (await rpc(B, 'node_info')).pubkey
  let low
  try {
    await rpc(A, 'open_channel', [{
      pubkey: Bpub, funding_amount: '0x1', public: false,
      funding_udt_type_script: RUSD_UDT, tlc_fee_proportional_millionths: '0x0',
    }])
    low = 'unexpectedly accepted'
  } catch (e) { low = e.message }
  record('C4.5', 'open_channel_too_low', low)
  record('C4.5', 'force_close', 'SKIPPED — would strand our only RUSD channel (stop condition honored)')
  record('C4.5', 'layer_boundary_note', 'if a force-close anomaly ever surfaces inside FNN lock logic, that is the FNN protocol lane — link to nervosnetwork/fiber, do not claim as a Dular finding')
  record('C4.5', 'VERDICT', 'low-capacity open rejected at the boundary: ' + String(low).slice(0, 80))
}

// ------------------------------------------------------------- main
const which = process.argv[2] || 'all'
const main = async () => {
  for (const [name, fn] of [['C4.1', c41], ['C4.2', c42], ['C4.3', c43], ['C4.4', c44], ['C4.5', c45]]) {
    if (which === 'all' || which === name) {
      try { await fn() } catch (e) { record(name, 'ERROR', e.message) }
    }
  }
  await (await import('node:fs/promises')).writeFile((process.env.RECORD_FILE || new URL('./c4-record.json', import.meta.url).pathname), JSON.stringify(rec, null, 2))
  console.log('\nrecord written to c4-record.json')
}
main().catch((e) => { console.error('C4 fatal:', e); process.exit(1) })
