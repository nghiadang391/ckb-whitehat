// ckb-whitehat — C4.1/4.2/4.3 driver v2. Fixes v1: FNN 0.9.1 list_channels
// does NOT return funding_tx_hash, so v1's break-never-fires bug is removed.
// Funding tx hash is derived from channel_outpoint (outpoint = funding_tx_hash
// + index) and confirmed on-chain via CKB RPC get_transaction.
const A = 'http://127.0.0.1:8227'
const B = 'http://127.0.0.1:8327'
const CKB = 'https://testnet.ckbapp.dev/'
const RUSD_UDT = {
  code_hash: '0x1142755a044bf2ee358cba9f2da187ce928c91cd4dc8692ded0337efa677d21a',
  hash_type: 'type', args: '0x878fcc6f1f08d48e87bb1c3b3d5083f23f8a39c5d5c764f253b55b998526439b',
}

async function fnn(url, method, params = []) {
  const p = await fetch(url, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ jsonrpc: '2.0', id: 1, method, params }) }).then((r) => r.json())
  if (p.error) throw new Error(`${method}: ${p.error.message || JSON.stringify(p.error)}`)
  return p.result
}
async function ckb(method, params = []) {
  const p = await fetch(CKB, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ jsonrpc: '2.0', id: 1, method, params }) }).then((r) => r.json())
  if (p.error) throw new Error(`ckb ${method}: ${p.error.message}`)
  return p.result
}
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
const rec = { c4: {} }
const record = (t, k, v) => {
  rec.c4[t] = rec.c4[t] || {}; rec.c4[t][k] = v
  console.log(`[${t}] ${k} = ${JSON.stringify(v)?.slice(0, 160)}`)
}

const main = async () => {
  const Bpub = (await fnn(B, 'node_info')).pubkey

  // find the 10-RUSD channel (reuse if already ChannelReady, else wait for it)
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
  if (!ch) { record('C4.1', 'ABORT', 'no ChannelReady RUSD channel after 15 min'); await dump(); return }
  record('C4.1', 'channel_id', ch.channel_id)

  // funding tx = outpoint's tx_hash half (outpoint = tx_hash || index)
  const fundTx = ch.channel_outpoint?.slice(0, 66)
  record('C4.1', 'channel_funding_tx', fundTx)
  const status = await ckb('get_transaction_status', [fundTx]).catch(() => null)
  record('C4.1', 'funding_tx_status', status ? status.tx_status : 'status-method-unavailable')
  record('C4.1', 'explorer', `https://pudge.explorer.nervos.org/transaction/${fundTx}`)

  const inv = async (amt, desc) => fnn(B, 'new_invoice', [{ amount: amt, currency: 'Fibt', description: desc, expiry: '0x1e14', udt_type_script: RUSD_UDT }])
  const getp = async (h) => fnn(A, 'get_payment', [{ payment_hash: h }])

  // ---- C4.1 baseline: 0.1 RUSD A -> B invoice
  const i1 = await inv('0x' + (10_000_000n).toString(16), 'C4.1 baseline RUSD')
  record('C4.1', 'invoice_payment_hash', i1.payment_hash)
  const p1 = await fnn(A, 'send_payment', [{ invoice: i1.invoice_address }])
  record('C4.1', 'payment_hash', p1.payment_hash)
  await sleep(6000)
  const f1 = await getp(p1.payment_hash)
  record('C4.1', 'status', f1.status)
  record('C4.1', 'VERDICT', f1.status === 'Success' ? 'GREEN - 0.1 RUSD settled A->B on Dular\'s chain' : 'no: ' + f1.status)

  // ---- C4.2 F1 money trace: 0.5 RUSD to a third-party (attacker) invoice
  const i2 = await inv('0x' + (50_000_000n).toString(16), 'C4.2 forged-callback settlement RUSD')
  record('C4.2', 'attacker_invoice_payment_hash', i2.payment_hash)
  const p2 = await fnn(A, 'send_payment', [{ invoice: i2.invoice_address }])
  record('C4.2', 'settlement_payment_hash', p2.payment_hash)
  await sleep(6000)
  const f2 = await getp(p2.payment_hash)
  record('C4.2', 'status', f2.status)
  record('C4.2', 'VERDICT', f2.status === 'Success' ? 'RED - 0.5 RUSD moved operator->third-party on-chain for a fabricated ResultCode:0 money-in (F1)' : 'no: ' + f2.status)

  // ---- C4.3 double-settle: one 0.2 RUSD invoice, two concurrent sends
  const i3 = await inv('0x' + (20_000_000n).toString(16), 'C4.3 double-settle invoice')
  record('C4.3', 'invoice_payment_hash', i3.payment_hash)
  const [p3a, p3b] = await Promise.all([
    fnn(A, 'send_payment', [{ invoice: i3.invoice_address }]),
    fnn(A, 'send_payment', [{ invoice: i3.invoice_address }]),
  ])
  record('C4.3', 'payment_1', p3a.payment_hash, 'payment_2', p3b.payment_hash)
  await sleep(6000)
  const [g3a, g3b] = await Promise.all([getp(p3a.payment_hash), getp(p3b.payment_hash)])
  record('C4.3', 'statuses', JSON.stringify([g3a.status, g3b.status]))
  record('C4.3', 'VERDICT', g3a.status === 'Success' && g3b.status === 'Success' ? 'RED - one 0.2 RUSD invoice settled twice: operator paid 2x for one order (F2 on-chain face)' : 'chain/app stopped one: ' + JSON.stringify([g3a.status, g3b.status]))

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
main().catch((e) => { console.error('C4 v2 fatal:', e.message); process.exit(1) })
