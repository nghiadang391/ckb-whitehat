// Minimal Fiber JSON-RPC stub. Records every method it serves so a PoC can assert
// "the app triggered a real operator send_payment" without needing live channel
// liquidity. send_payment always succeeds and lands in `payments` (Success), so the
// target's settlement code completes and credits its own ledger.
import http from 'node:http'

const RUSD = {
  code_hash: '0x1142755a044bf2ee358cba9f2da187ce928c91cd4dc8692ded0337efa677d21a',
  hash_type: 'type',
  args: '0x878fcc6f1f08d48e87bb1c3b3d5083f23f8a39c5d5c764f253b55b998526439b',
}

export function startFnnStub({ port, payments = [], channels = {} } = {}) {
  const served = []
  const server = http.createServer((req, res) => {
    let body = ''
    req.on('data', d => { body += d })
    req.on('end', () => {
      let msg = {}
      try { msg = JSON.parse(body || '{}') } catch {}
      const { id, method, params } = msg
      served.push({ method, params, at: new Date().toISOString() })
      let result = null
      if (method === 'node_info') {
        result = {
          pubkey: '0x' + 'ab'.repeat(33),
          channel_count: 1,
          pending_channel_count: 0,
          default_funding_lock_script: {
            code_hash: '0x' + 'cc'.repeat(32), hash_type: 'type', args: '0x' + 'dd'.repeat(20),
          },
        }
      } else if (method === 'list_peers') {
        result = { peers: Object.keys(channels).map((pubkey) => ({ pubkey, address: '/dns4/127.0.0.1/tcp/8248/ws', addr_type: 'ws' })) }
      } else if (method === 'list_channels') {
        const peer = params?.[0]?.pubkey || null
        result = { channels: peer ? (channels[peer] || []) : Object.values(channels).flat() }
      } else if (method === 'list_payments') {
        result = { payments }
      } else if (method === 'get_payment') {
        result = payments.find((p) => p.payment_hash === params?.[0]?.payment_hash) || null
      } else if (method === 'send_payment') {
        const ph = '0x' + 'ef'.repeat(32)
        payments.push({ payment_hash: ph, status: 'Success', fee: '0', routers: [] })
        result = { payment_hash: ph, status: 'Success' }
      } else if (method === 'new_invoice') {
        const ph = '0x' + 'cd'.repeat(32)
        result = {
          payment_hash: ph,
          invoice_address: `fiber:receiver:0x01?amount=${params?.[0]?.amount || '0x0'}&currency=Fibt`,
          expiry: params?.[0]?.expiry || '0x1e14',
        }
      } else if (method === 'get_invoice') {
        result = { status: 'Paid', payment_hash: params?.[0]?.payment_hash }
      }
      res.setHeader('content-type', 'application/json')
      res.end(JSON.stringify({ jsonrpc: '2.0', id, result: result ?? {} }))
    })
  })
  return new Promise((resolve) => {
    server.listen(port, '127.0.0.1', () => resolve({ server, served, payments, channels, close: () => server.close() }))
  })
}

export function startChannelStub(stub, pubkey, localBalance) {
  stub.channels[pubkey] = [{
    channel_id: '0x' + 'a0'.repeat(32),
    temporary_channel_id: '0x' + 'a0'.repeat(32),
    channel_outpoint: '0x' + 'b0'.repeat(32) + ':0x0',
    state: { state_name: 'ChannelReady' },
    is_public: true,
    enabled: true,
    local_balance: '0x' + BigInt(localBalance).toString(16),
    remote_balance: '0x0',
    tlc_fee_proportional_millionths: '0x0',
    funding_udt_type_script: RUSD,
    created_at: String(Date.now()),
  }]
}
