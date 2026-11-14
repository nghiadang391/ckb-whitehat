// ckb-whitehat PoC — Dular: F7 wildcard CORS + internal error-message leak
//
// index.js: `app.use(cors())` with NO options -> every response to an
// Origin'd request carries Access-Control-Allow-Origin: *. The global
// catch-all: `res.status(400).json({ error: error.message })` returns the
// raw internal error string to any unauthenticated client.
//
// This PoC boots the REAL app, sends an Origin'd request and reads back the
// CORS header; POSTs a route whose domain error reaches the catch-all and
// shows the internal message in the 400 body.
//
// The app runs against an ISOLATED scratch database (target's schema.sql),
// dropped on teardown.
//
// Run: node f7-cors-error-leak.mjs
// Env:  DULAR_DIR=<repo root>   (or a server/ dir — both accepted)

import { resolveTarget, targetImport, ensureScratchDb, dropScratchDb, line } from './target.mjs'

const { repoRoot, serverDir } = resolveTarget()
const db = await ensureScratchDb(repoRoot, { name: 'dular_poc_f7' })
process.env.DATABASE_URL = db.url

const main = async () => {
  const { default: app } = await targetImport(serverDir, 'index.js')
  const http = await import('node:http')
  const server = http.createServer(app)
  await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve))
  const base = `http://127.0.0.1:${server.address().port}`

  line('F7 — wildcard CORS + global 400 handler leaking error.message')
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
  const { pool } = await targetImport(serverDir, 'db.js')
  await pool.end()

  console.log(fired ? 'FINDING F7 FIRED' : 'FINDING F7 UNPROVEN')
  return fired ? 0 : 1
}

let exitCode = 0
main()
  .then((code) => { exitCode = code })
  .catch((e) => { console.error('F7 PoC error:', e); exitCode = 2 })
  .finally(async () => {
    // Tear the scratch DB down BEFORE exiting so no PoC row survives.
    await dropScratchDb(db)
    process.exit(exitCode)
  })
