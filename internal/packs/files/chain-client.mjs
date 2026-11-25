// Zero-dependency CKB + 2-node Fiber (FNN) JSON-RPC helper for ckb-whitehat PoCs.
// Reads $CKB_WHITEHAT_CHAIN_CONFIG (~/.ckb-whitehat/chain.json) and records
// verified on-chain hashes into <workspace>/poc/chain-evidence.json.
import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'

export function loadChainManifest() {
  const p = process.env.CKB_WHITEHAT_CHAIN_CONFIG ||
    path.join(os.homedir(), '.ckb-whitehat', 'chain.json')
  if (!fs.existsSync(p)) {
    throw new Error(`chain manifest not found at ${p} — run: ckb-whitehat chain up`)
  }
  return JSON.parse(fs.readFileSync(p, 'utf8'))
}

export async function rpc(url, method, params = []) {
  const res = await fetch(url, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ jsonrpc: '2.0', id: 1, method, params }),
  })
  if (!res.ok) {
    const text = await res.text().catch(() => '')
    throw new Error(`RPC ${method} HTTP ${res.status}: ${text.slice(0, 200)}`)
  }
  const body = await res.json()
  if (body.error) {
    throw new Error(`RPC ${method} failed: ${body.error.message || JSON.stringify(body.error)}`)
  }
  return body.result
}

export async function generateBlocks(ckbUrl, count = 1) {
  const hashes = []
  for (let i = 0; i < count; i++) {
    hashes.push(await rpc(ckbUrl, 'generate_block', []))
  }
  return hashes
}

export async function newInvoice(fnnUrl, {
  amountHex,
  currency = 'Fibd',
  description = 'ckb-whitehat-poc',
  udtTypeScript = null,
  paymentPreimage = null,
  paymentHash = null,
  hashAlgorithm = null,
}) {
  const params = { amount: amountHex, currency, description }
  if (udtTypeScript) params.udt_type_script = udtTypeScript
  if (paymentPreimage) params.payment_preimage = paymentPreimage
  if (paymentHash) params.payment_hash = paymentHash
  if (hashAlgorithm) params.hash_algorithm = hashAlgorithm
  return rpc(fnnUrl, 'new_invoice', [params])
}

export async function sendPayment(fnnUrl, invoiceAddress, extra = {}) {
  return rpc(fnnUrl, 'send_payment', [{ invoice: invoiceAddress, ...extra }])
}

export async function waitPayment(fnnUrl, paymentHash, timeoutMs = 15000) {
  const deadline = Date.now() + timeoutMs
  while (Date.now() < deadline) {
    const res = await rpc(fnnUrl, 'get_payment', [{ payment_hash: paymentHash }])
    if (res && (res.status === 'Success' || res.status === 'Failed')) return res
    await new Promise((r) => setTimeout(r, 250))
  }
  throw new Error(`timed out waiting for payment ${paymentHash}`)
}

export function recordChainEvidence(entry) {
  const ws = process.env.CKB_WHITEHAT_WORKSPACE || process.cwd()
  const evPath = path.join(ws, 'poc', 'chain-evidence.json')
  fs.mkdirSync(path.dirname(evPath), { recursive: true })
  let list = []
  if (fs.existsSync(evPath)) {
    try {
      const parsed = JSON.parse(fs.readFileSync(evPath, 'utf8'))
      if (Array.isArray(parsed)) list = parsed
    } catch {}
  }
  list.push({ recorded_at: new Date().toISOString(), ...entry })
  fs.writeFileSync(evPath, JSON.stringify(list, null, 2) + '\n')
  return evPath
}
