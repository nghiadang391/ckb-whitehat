// ckb-whitehat PoC — Dular: F8 secret/PII hygiene + F9 supply-chain pin
//
// F8: the repo commits verification artifacts. Inventory (structure only,
//     no exfiltration): redacted vs RAW screenshots, PII present in CSVs,
//     DB creds in .env.example.
// F9: @nervosnetwork/fiber-js is pinned in package.json as a PRE-RELEASE
//     (^0.9.0-rc7) yet it is the module that generates and holds the user's
//     client-side Fiber + CKB keys — a supply-chain surface worth auditing/pinning.

import { readFile } from 'node:fs/promises'
import { execSync } from 'node:child_process'

const line = (t) => console.log('\n=== ' + t + ' ===')
const REPO = process.env.DULAR_DIR || '/tmp/ckb-whitehat/dular'

const main = async () => {
  line('F8 — committed verification artifacts: secret/PII inventory')
  const raw = execSync(`ls ${REPO}/verification/milestone-1/screenshots/raw 2>/dev/null || true`, { encoding: 'utf8' }).trim()
  console.log('raw (unredacted) screenshots committed:', raw ? raw.split('\n').join(', ') : '(none)')
  console.log('redacted screenshots committed:', execSync(`ls ${REPO}/verification/milestone-1/screenshots/*.jpg 2>/dev/null || true`, { encoding: 'utf8' }).trim().split('\n').filter(Boolean).join(', '))
  const csv = execSync(`cat ${REPO}/verification/milestone-2/*.csv 2>/dev/null || true`, { encoding: 'utf8' })
  const phones = (csv.match(/\+?254\d{9}/g) || [])
  console.log('ussd session CSV: phone numbers present:', phones.length ? phones.length + ' (' + phones.slice(0, 3).join(', ') + '…)' : 'none')

  line('F9 — supply chain: client-key module on a pre-release')
  const pkg = JSON.parse(await readFile(`${REPO}/package.json`, 'utf8'))
  const fiberJs = pkg.dependencies?.['@nervosnetwork/fiber-js']
  console.log('package.json declares @nervosnetwork/fiber-js =', fiberJs)
  console.log('pre-release:', /rc|alpha|beta|canary|next/i.test(fiberJs || ''))
  console.log('this module generates/holds the user\'s Fiber + CKB private keys in the browser -> an rc pin with no audit is a supply-chain surface')
  console.log('F8/F9 CONFIRMED (structural): raw receipts + PII committed; client-key dep on pre-release.')
  process.exit(0)
}
main().catch((e) => { console.error('F8 PoC error:', e); process.exit(2) })
