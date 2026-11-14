// ckb-whitehat PoC — Dular: F8 secret/PII hygiene + F9 supply-chain pin
//
// F8: the repo commits verification artifacts. Inventory (structure only,
//     no exfiltration): redacted vs RAW screenshots, PII present in CSVs,
//     DB creds in .env.example.
// F9: @nervosnetwork/fiber-js is pinned in package.json as a PRE-RELEASE
//     (^0.9.0-rc7) yet it is the module that generates and holds the user's
//     client-side Fiber + CKB keys — a supply-chain surface worth auditing/pinning.
//
// Both checks read the target REPO ROOT (DULAR_DIR points at the root; a
// server/ dir is also accepted and the root is derived from it).
//
// Run: node f8f9-hygiene-supply.mjs
// Env:  DULAR_DIR=<repo root>   (or a server/ dir — both accepted)

import { readFile, readdir } from 'node:fs/promises'
import path from 'node:path'
import { resolveTarget, line } from './target.mjs'

const main = async () => {
  const { repoRoot } = resolveTarget()
  console.log(`target repo root: ${repoRoot}`)

  line('F8 — committed verification artifacts: secret/PII inventory')
  const rawDir = path.join(repoRoot, 'verification', 'milestone-1', 'screenshots', 'raw')
  const redactedDir = path.join(repoRoot, 'verification', 'milestone-1', 'screenshots')
  const raw = await readdir(rawDir).catch(() => [])
  const redacted = (await readdir(redactedDir).catch(() => [])).filter((f) => f !== 'raw')
  console.log('raw (unredacted) screenshots committed:', raw.length ? raw.join(', ') : '(none)')
  console.log('redacted screenshots committed:', redacted.length ? redacted.join(', ') : '(none)')

  // PII in the milestone-2 USSD session CSVs: count DISTINCT phone numbers.
  const csvDir = path.join(repoRoot, 'verification', 'milestone-2')
  const csvFiles = (await readdir(csvDir).catch(() => [])).filter((f) => f.endsWith('.csv'))
  const phones = new Set()
  for (const f of csvFiles) {
    const text = await readFile(path.join(csvDir, f), 'utf8').catch(() => '')
    for (const m of text.matchAll(/\+?254\d{9}/g)) phones.add(m[0])
  }
  console.log(`ussd session CSVs: ${csvFiles.length} file(s), distinct Kenyan phone numbers present: ${phones.size}`)

  const envExample = await readFile(path.join(repoRoot, '.env.example'), 'utf8').catch(() => '')
  const envDbCreds = /postgres:\/\/[^:\s]+:[^@\s]+@/.test(envExample)
  const envDemo = /DEMO_MODE=true/.test(envExample)
  console.log('.env.example ships a postgres://user:password@ credential:', envDbCreds)
  console.log('.env.example ships DEMO_MODE=true:', envDemo)

  const f8 = raw.length > 0 && phones.size > 0
  console.log(f8
    ? 'F8 OBSERVED: un-redacted M-Pesa/OTP screenshots AND live Kenyan phone PII are committed to the repo, alongside DB credentials in .env.example'
    : 'F8 not observed: expected raw screenshots / PII to be present — inspect above')

  line('F9 — supply chain: client-key module on a pre-release')
  const pkg = JSON.parse(await readFile(path.join(repoRoot, 'package.json'), 'utf8'))
  const fiberJs = pkg.dependencies?.['@nervosnetwork/fiber-js']
  const preRelease = /rc|alpha|beta|canary|next/i.test(fiberJs || '')
  console.log('package.json declares @nervosnetwork/fiber-js =', fiberJs)
  console.log('pre-release:', preRelease)
  console.log("this module generates/holds the user's Fiber + CKB private keys in the browser -> an rc pin with no audit is a supply-chain surface")

  // Does the lockfile pin an actual rc build?
  const lock = await readFile(path.join(repoRoot, 'package-lock.json'), 'utf8').catch(() => '')
  const lockRc = /"node_modules\/@nervosnetwork\/fiber-js"[\s\S]{0,400}?0\.9\.0-rc\d+/.test(lock)
  console.log('package-lock.json pins a concrete rc build:', lockRc)

  const f9 = preRelease && lockRc
  console.log(f9
    ? 'F9 OBSERVED: the browser key-holding module is locked to a release-candidate build'
    : 'F9 not observed: expected an rc pin — inspect above')

  // F8/F9 are static, code-shape observations (a committed-file inventory and
  // a lockfile pin). The harness verifies them against the repo's real state —
  // it is not a runnable exploit, so the finding class is STRUCTURAL.
  console.log(f8 ? 'FINDING F8 STRUCTURAL' : 'FINDING F8 UNPROVEN')
  console.log(f9 ? 'FINDING F9 STRUCTURAL' : 'FINDING F9 UNPROVEN')
  process.exit(f8 && f9 ? 0 : 1)
}

main().catch((e) => { console.error('F8/F9 PoC error:', e); process.exit(2) })
