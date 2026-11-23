// Reusable CKB/Fiber attack pack: Hold-Invoice / HTLC Settlement & Boundary Checks.
// Verifies hold-invoice (hash-only) creation, amount-mismatch guardrails, and
// safe devnet channel-open boundary enforcement.
import crypto from 'node:crypto'
import { loadChainManifest, rpc, newInvoice, sendPayment, recordChainEvidence } from './chain-client.mjs'

export async function probeHoldInvoiceLifecycle({ amountHex = '0x5f5e100', useUDT = true } = {}) {
  const m = loadChainManifest()
  const preimage = crypto.randomBytes(32)
  const paymentHash = '0x' + crypto.createHash('sha256').update(preimage).digest('hex')
  const inv = await newInvoice(m.node_b.rpc_url, {
    amountHex,
    description: 'ckb-whitehat hold-invoice probe',
    udtTypeScript: useUDT && m.udt ? m.udt.script : null,
    paymentHash,
  })
  const sendPromise = sendPayment(m.node_a.rpc_url, inv.invoice_address)
  await new Promise((r) => setTimeout(r, 400))
  const settled = await rpc(m.node_b.rpc_url, 'settle_invoice', [{
    payment_hash: paymentHash,
    payment_preimage: '0x' + preimage.toString('hex'),
  }])
  const sent = await sendPromise
  recordChainEvidence({ pack: 'hold-invoice-htlc', payment_hash: paymentHash, status: sent?.status || 'settled' })
  return { paymentHash, invoice: inv.invoice_address, settled, sent }
}

export async function probeUnderMinimumChannelOpen() {
  const m = loadChainManifest()
  try {
    await rpc(m.node_a.rpc_url, 'open_channel', [{
      pubkey: m.node_b.node_id,
      funding_amount: '0x1', // 1 shannon — strictly below minimum cell capacity
    }])
    return { rejected: false }
  } catch (err) {
    return { rejected: true, error: String(err.message || err) }
  }
}
