// ckb-whitehat PoC — Dular: F3 demo-mode fabricates provider success
//
// daraja.js: when DEMO_MODE=true, initiateStkPush / queryStkPushStatus
// early-return canned ResultCode '0' ("demo token / demo success") with NO
// vendor call. config.js: MPESA_ENVIRONMENT defaults to 'production'.
// Nothing in config.js, db.js, or the app boot refuses to start — or to
// credit deposits — when demoMode && environment === 'production' coexist.
//
// This PoC loads the TARGET's real server/config.js and server/services/daraja.js
// (unmodified) under DEMO_MODE=true + production env, calls the real
// initiateStkPush / queryStkPushStatus, and asserts the fabricated success.
// It then checks the target's own config.js for a startup guard that refuses
// the demoMode+production pair.
//
// Run: node f3-demo-mode.mjs
// Env:  DULAR_DIR=<repo root>   (or a server/ dir — both accepted)

import { readFile } from 'node:fs/promises'
import path from 'node:path'
import { resolveTarget, targetImport, line } from './target.mjs'

process.env.DEMO_MODE = 'true'
process.env.MPESA_ENVIRONMENT = 'production'   // the config.js default anyway
process.env.DATABASE_URL = process.env.DATABASE_URL || 'postgres://dular:dular@127.0.0.1:5433/dular'

const main = async () => {
  const { repoRoot, serverDir } = resolveTarget()
  console.log(`target: ${repoRoot} (${serverDir})`)

  line('F3 — demo-mode provider fabrication under a production configuration')
  const { config } = await targetImport(serverDir, 'config.js')
  const { initiateStkPush, queryStkPushStatus } = await targetImport(serverDir, 'services/daraja.js')

  // The target's own demo-mode code path, called directly.
  const stk = await initiateStkPush({ phone: '+254712345678', amountKes: 100, accountReference: 'F3POC' })
  const stkq = await queryStkPushStatus({ checkoutRequestId: 'ws_CO_f3_demo' })
  console.log('config: demoMode =', config.demoMode, '| mpesa.environment =', config.mpesa.environment)
  console.log('initiateStkPush ->', JSON.stringify(stk))
  console.log('queryStkPushStatus ->', JSON.stringify({ ResultCode: stkq.ResultCode, ResultDesc: stkq.ResultDesc, CheckoutRequestID: stkq.CheckoutRequestID }))

  const fabricated = stk.ResponseCode === '0' && stkq.ResultCode === '0' && config.mpesa.environment === 'production' && config.demoMode
  console.log(fabricated
    ? 'F3 FIRED: a deployment configured for PRODUCTION M-Pesa will self-confirm every STK deposit and B2C payout with zero provider calls when DEMO_MODE=true'
    : 'expected demo fabrication — inspect output above')

  // Is there a startup guard in the target's own config.js that refuses the
  // demoMode + production pair? A real guard would reference both flags in one
  // check that throws/refuses. (A throw whose MESSAGE merely mentions
  // DEMO_MODE, like requireDatabaseUrl's, is not such a guard.)
  const configSrc = await readFile(path.join(serverDir, 'config.js'), 'utf8')
  const guard = configSrc.split(/\n/).some((l) =>
    /throw/.test(l)
    && /demo/i.test(l)
    && /(production|MPESA_ENVIRONMENT|mpesa\.environment)/i.test(l))
  console.log('startup guard in config.js refusing demoMode+production:', guard ? 'present' : 'ABSENT — config.js reads the flags independently; no cross-check exists')

  const fired = fabricated && !guard
  console.log(fired
    ? 'FINDING F3 FIRED'
    : 'FINDING F3 UNPROVEN')
  process.exit(fired ? 0 : 1)
}

main().catch((e) => { console.error('F3 PoC error:', e); process.exit(2) })
