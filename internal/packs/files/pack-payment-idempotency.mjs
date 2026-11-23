// Reusable CKB/Fiber attack pack: Payment Idempotency & Concurrent Double-Settle.
// Import into a target PoC to test whether concurrent invocations of a target's
// settlement/callback handler pay the same Fiber invoice or order twice.
import crypto from 'node:crypto'
import { loadChainManifest, newInvoice, sendPayment, waitPayment, recordChainEvidence } from './chain-client.mjs'

export async function probeConcurrentFiberDoublePay({ amountHex = '0x5f5e100', useUDT = true, concurrency = 2 } = {}) {
  const m = loadChainManifest()
  const preimage = '0x' + crypto.randomBytes(32).toString('hex')
  const inv = await newInvoice(m.node_b.rpc_url, {
    amountHex,
    description: 'ckb-whitehat idempotency probe',
    udtTypeScript: useUDT && m.udt ? m.udt.script : null,
    paymentPreimage: preimage,
  })
  const results = await Promise.allSettled(
    Array.from({ length: concurrency }, () => sendPayment(m.node_a.rpc_url, inv.invoice_address))
  )
  const paymentHash = inv.invoice?.data?.payment_hash || results.find((r) => r.status === 'fulfilled')?.value?.payment_hash
  const final = paymentHash ? await waitPayment(m.node_a.rpc_url, paymentHash) : null
  if (paymentHash && final?.status === 'Success') {
    recordChainEvidence({ pack: 'payment-idempotency', payment_hash: paymentHash, status: final.status })
  }
  return { invoice: inv.invoice_address, paymentHash, attempts: results, final }
}

// Generic concurrent race runner for testing target settlement functions.
export async function raceConcurrent(fn, count = 4) {
  const settled = await Promise.allSettled(Array.from({ length: count }, (_, i) => fn(i)))
  const succeeded = settled.filter((r) => r.status === 'fulfilled')
  return { total: count, succeededCount: succeeded.length, settled }
}
