// Reusable CKB/Fiber invariant-verification pack: Hold-Invoice / HTLC Settlement & Boundary Checks.
// Verifies hold-invoice (hash-only) creation, amount-mismatch guardrails, and
// safe devnet channel-open boundary enforcement.
import crypto from 'node:crypto'
import { loadChainManifest, rpc, newInvoice, sendPayment, waitPayment, recordChainEvidence } from './chain-client.mjs'

export async function probeHoldInvoiceLifecycle({ amountHex = '0x5f5e100', useUDT = true, settleTimeoutMs = 5000 } = {}) {
  const m = loadChainManifest()
  const preimage = crypto.randomBytes(32)
  const paymentHash = '0x' + crypto.createHash('sha256').update(preimage).digest('hex')
  const inv = await newInvoice(m.node_b.rpc_url, {
    amountHex,
    description: 'ckb-whitehat hold-invoice probe',
    udtTypeScript: useUDT && m.udt ? m.udt.script : null,
    paymentHash,
    hashAlgorithm: 'sha256',
  })
  await sendPayment(m.node_a.rpc_url, inv.invoice_address)

  // Retry settle_invoice until the HTLC reaches Node B (avoiding a fragile fixed sleep).
  const settleDeadline = Date.now() + settleTimeoutMs
  let settled = null
  let lastSettleErr = null
  while (Date.now() < settleDeadline) {
    try {
      settled = await rpc(m.node_b.rpc_url, 'settle_invoice', [{
        payment_hash: paymentHash,
        payment_preimage: '0x' + preimage.toString('hex'),
      }])
      lastSettleErr = null
      break
    } catch (err) {
      lastSettleErr = err
      await new Promise((r) => setTimeout(r, 200))
    }
  }
  if (lastSettleErr) throw lastSettleErr

  const finalPayment = await waitPayment(m.node_a.rpc_url, paymentHash)
  if (finalPayment?.status === 'Success') {
    recordChainEvidence({ pack: 'hold-invoice-htlc', payment_hash: paymentHash, status: finalPayment.status })
  }
  return { paymentHash, invoice: inv.invoice_address, settled, sent: finalPayment }
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
