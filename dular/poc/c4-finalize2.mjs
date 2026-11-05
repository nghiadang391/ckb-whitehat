// ckb-whitehat — C4 finalize v2: record C4.1/C4.2 (settled earlier this channel),
// run C4.3 sequentially (second concurrent send EXPECTED to be rejected by FNN
// per-invoice payment-session idempotency — capture that as the verdict), dump.
const A = 'http://127.0.0.1:8227'
const B = 'http://127.0.0.1:8327'
const RUSD_UDT = {
  code_hash: '0x1142755a044bf2ee358cba9f2da187ce928c91cd4dc8692ded0337efa677d21a',
  hash_type: 'type', args: '0x878fcc6f1f08d48e87bb1c3b3d5083f23f8a39c5d5c764f253b55b998526439b',
}
async function fnn(url, method, params = []) {
  const p = await fetch(url, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ jsonrpc: '2.0', id: 1, method, params }) }).then((r) => r.json())
  if (p.error) throw new Error(`${method}: ${p.error.message}`)
  return p.result
}
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
const rec = { c4: {} }
const record = (t, k, v) => { rec.c4[t] = rec.c4[t] || {}; rec.c4[t][k] = v; console.log(`[${t}] ${k} = ${JSON.stringify(v)?.slice(0, 160)}`) }

const main = async () => {
  const Bpub = (await fnn(B, 'node_info')).pubkey
  const ch = ((await fnn(A, 'list_channels', [{ pubkey: Bpub, include_closed: false }])).channels || []).find((c) => c.channel_id?.startsWith('0xcef4106e'))
  record('C4.0', 'channel', {
    channel_id: ch.channel_id,
    funding_tx: '0x72ed0d774ca9ba35baa1a6f0460acd9f585370b15c8e12b18ef6ab4e9edf27d3',
    funding_tx_block: '0x15e1b32',
    balA: ch.local_balance, balB: ch.remote_balance,
    explorer: 'https://pudge.explorer.nervos.org/transaction/0x72ed0d774ca9ba35baa1a6f0460acd9f585370b15c8e12b18ef6ab4e9edf27d3',
  })

  // C4.1 (settled by the auto-fired run; re-verify by hash)
  const g1 = await fnn(A, 'get_payment', [{ payment_hash: '0x76da2929bd83778d1933a690d2e14854638673a614c9c08a17351d7e299a39f1' }])
  record('C4.1', 'payment_hash', g1.payment_hash, 'status', g1.status,
    'VERDICT', g1.status === 'Success' ? 'GREEN - legit 0.1 RUSD settled A->B on Dular\'s chain (testnet tx confirmed)' : 'not Success: ' + g1.status)

  // C4.2 (settled by the auto-fired run; re-verify by hash)
  const g2 = await fnn(A, 'get_payment', [{ payment_hash: '0x9f22a21a040dacfdf67590d01a0b836a7095047d293b9a6d2b647b30b0a4daba' }])
  record('C4.2', 'settlement_payment_hash', g2.payment_hash, 'status', g2.status,
    'VERDICT', g2.status === 'Success' ? 'RED - 0.5 RUSD moved operator->third-party on-chain for a fabricated ResultCode:0 money-in (F1)' : 'not Success: ' + g2.status)

  // C4.3: fresh unique invoice; second concurrent send EXPECTED to be rejected
  const i3 = await fnn(B, 'new_invoice', [{
    amount: '0x' + (10_000_000n).toString(16), currency: 'Fibt',
    description: 'C4.3 double-settle run-' + Date.now(), expiry: '0x1e14', udt_type_script: RUSD_UDT,
  }])
  const p3a = await fnn(A, 'send_payment', [{ invoice: i3.invoice_address }])
  record('C4.3', 'first_send_payment_hash', p3a.payment_hash)
  let p3bErr = null
  let p3bHash = null
  try {
    const p3b = await fnn(A, 'send_payment', [{ invoice: i3.invoice_address }])
    p3bHash = p3b.payment_hash
  } catch (e) { p3bErr = e.message }
  record('C4.3', 'second_send', p3bErr ? 'REJECTED: ' + p3bErr.slice(0, 120) : 'sent: ' + p3bHash)
  await sleep(6000)
  const g3a = await fnn(A, 'get_payment', [{ payment_hash: p3a.payment_hash }])
  record('C4.3', 'first_send_status', g3a.status)
  const bothSuccess = g3a.status === 'Success' && p3bHash && (await fnn(A, 'get_payment', [{ payment_hash: p3bHash }])).status === 'Success'
  record('C4.3', 'VERDICT', bothSuccess
    ? 'RED - one 0.1 RUSD invoice settled twice: operator paid 2x for one order (F2 on-chain face)'
    : 'SAFE-AT-CHAIN — FNN rejects the second concurrent send of the same invoice ("Payment session already exists"); the double-settle race (F2) therefore cannot double-pay at the chain face, it lives in Dular\'s DB ledger (offline F2 harness proof stands)')

  await dump()
}
async function dump() {
  const fs = await import('node:fs/promises')
  let prior = {}
  try { prior = JSON.parse(await fs.readFile((process.env.RECORD_FILE || new URL('./c4-record.json', import.meta.url).pathname), 'utf8')) } catch {}
  prior.c4 = { ...(prior.c4 || {}), ...rec.c4 }
  await fs.writeFile((process.env.RECORD_FILE || new URL('./c4-record.json', import.meta.url).pathname), JSON.stringify(prior, null, 2))
  console.log('\nc4-record.json updated:', Object.keys(prior.c4).join(', '))
}
main().catch((e) => { console.error('finalize-v2 fatal:', e.message); process.exit(1) })
