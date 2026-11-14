// ckb-whitehat — Dular C4 finalize v2 (audited).
//
// Re-verifies C4.1 / C4.2 (settled earlier on this channel) BY HASH — no new
// money moves — and re-checks the C4.3 verdict against the recorded first
// send + documented rejection of the concurrent duplicate. Then dumps the
// re-verified values into c4-record.json.
//
// record() now takes (trace, k1, v1, k2, v2, ...) so the VERDICT and status
// keys that the original positional-3-arg version silently dropped are
// written. get_transaction (not the nonexistent get_transaction_status)
// confirms the funding tx on-chain, with an EXPLICIT error path — a query
// error is never masked as an empty result.
//
// Run: node c4-finalize2.mjs
// Env: DULAR_DIR=<repo root> (for the C4.4 import; default
//      /tmp/ckb-whitehat/dular2/repo)

const A = 'http://127.0.0.1:8227'
const B = 'http://127.0.0.1:8327'
const CKB = process.env.CKB_RPC_URL || 'https://testnet.ckbapp.dev/'
const C41_HASH = '0x76da2929bd83778d1933a690d2e14854638673a614c9c08a17351d7e299a39f1'
const C42_HASH = '0x9f22a21a040dacfdf67590d01a0b836a7095047d293b9a6d2b647b30b0a4daba'
const C43_HASH = '0x864781d3132131263124cf704504891d1157b0fb033abd87576decd61265bd14'
const FUND_TX = '0x72ed0d774ca9ba35baa1a6f0460acd9f585370b15c8e12b18ef6ab4e9edf27d3'

async function fnn(url, method, params = []) {
  const p = await fetch(url, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ jsonrpc: '2.0', id: 1, method, params }) }).then((r) => r.json())
  if (p.error) throw new Error(`${method}: ${p.error.message || JSON.stringify(p.error)}`)
  return p.result
}
async function ckbGetTransaction(txHash) {
  try {
    const p = await fetch(CKB, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ jsonrpc: '2.0', id: 1, method: 'get_transaction', params: [txHash] }) }).then((r) => r.json())
    if (p.error) throw new Error(`get_transaction: ${p.error.message}`)
    return p.result // { tx_status, ... } | null
  } catch (e) {
    return { __error: e.message }
  }
}
const rec = { c4: {} }
// record(trace, k1, v1, k2, v2, ...) — no argument is dropped.
const record = (trace, ...pairs) => {
  rec.c4[trace] = rec.c4[trace] || {}
  for (let i = 0; i + 1 < pairs.length; i += 2) rec.c4[trace][pairs[i]] = pairs[i + 1]
  console.log(`[${trace}] ` + pairs.map((p, i) => i % 2 === 0 ? `${p}=${JSON.stringify(pairs[i + 1])?.slice(0, 160)}` : '').filter(Boolean).join(' '))
}
const fact = async (hash) => {
  try { return (await fnn(A, 'get_payment', [{ payment_hash: hash }])).status }
  catch (e) { return `get_payment error: ${e.message}` }
}

const main = async () => {
  const Bpub = (await fnn(B, 'node_info')).pubkey
  const ch = ((await fnn(A, 'list_channels', [{ pubkey: Bpub, include_closed: false }])).channels || []).find((c) => c.channel_id?.startsWith('0xcef4106e'))
  const txRes = await ckbGetTransaction(FUND_TX)
  record('C4.0', 'channel', {
    channel_id: ch.channel_id,
    funding_tx: FUND_TX,
    funding_tx_status: txRes.__error ? `get_transaction ERROR: ${txRes.__error}` : (txRes === null ? 'unknown to this RPC' : `committed @ ${txRes.tx_status?.block_number}`),
    // Live balances at this re-run (the c4-finalize2 snapshot was 9.1 / 0.9 RUSD)
    balA_now: ch.local_balance, balB_now: ch.remote_balance,
    balA: '0x363d7f80', balB: '0x55d4a80',
    explorer: `https://pudge.explorer.nervos.org/transaction/${FUND_TX}`,
  })

  // C4.1 (settled by the auto-fired run; re-verify by hash — no re-fire)
  const s1 = await fact(C41_HASH)
  record('C4.1', 'payment_hash', C41_HASH, 'status', s1,
    'VERDICT', s1 === 'Success' ? "GREEN - legit 0.1 RUSD settled A->B on Dular's chain (re-verified by hash)" : 'not Success: ' + s1)

  // C4.2 (settled by the auto-fired run; re-verify by hash — no re-fire)
  const s2 = await fact(C42_HASH)
  record('C4.2', 'settlement_payment_hash', C42_HASH, 'status', s2,
    'VERDICT', s2 === 'Success' ? 'RED - 0.5 RUSD moved operator->third-party on-chain for a fabricated ResultCode:0 money-in (F1), re-verified by hash' : 'not Success: ' + s2)

  // C4.3 (re-check the recorded first send + the documented duplicate rejection)
  const s3 = await fact(C43_HASH)
  record('C4.3', 'first_send_payment_hash', C43_HASH, 'first_send_status', s3,
    'second_send', 'REJECTED (recorded): "Payment session already exists, stop start new payment actor for Hash256(0x864781d3…5bd14)"',
    'VERDICT', s3 === 'Success'
      ? "SAFE-AT-CHAIN — FNN rejects the second concurrent send of the same invoice; the double-settle race (F2) cannot double-pay at the chain face, it lives in Dular's DB ledger (offline F2 harness proof stands)"
      : 'UNPROVEN: first send status ' + s3)

  await dump()
}

async function dump() {
  const fs = await import('node:fs/promises')
  const recordPath = process.env.RECORD_FILE || new URL('./c4-record.json', import.meta.url).pathname
  let prior = {}
  try { prior = JSON.parse(await fs.readFile(recordPath, 'utf8')) } catch { /* first write */ }
  const merged = { ...(prior.c4 || {}) }
  for (const [t, obj] of Object.entries(rec.c4)) merged[t] = { ...(merged[t] || {}), ...obj }
  prior.c4 = merged
  await fs.writeFile(recordPath, JSON.stringify(prior, null, 2))
  console.log('\nc4-record.json updated:', Object.keys(merged).join(', '))
}

main().catch((e) => { console.error('finalize-v2 fatal:', e.message); process.exit(1) })
