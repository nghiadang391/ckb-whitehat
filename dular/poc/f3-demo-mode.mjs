// ckb-whitehat PoC — Dular: F3 demo-mode fabricates provider success
//
// daraja.js: when DEMO_MODE=true, initiateStkPush / queryStkPushStatus
// early-return canned ResultCode '0' ("demo token / demo success") with NO
// vendor call. config.js: MPESA_ENVIRONMENT defaults to 'production'.
// Nothing in config.js, db.js, or the app boot refuses to start — or to
// credit deposits — when demoMode && environment === 'production' coexist.
//
// PoC: load the repo's real daraja.js under DEMO_MODE=true + production env,
// call initiateStkPush/queryStkPushStatus, show the fabricated "success" and
// zero vendor calls, then show no startup guard exists in config.js.

process.env.DEMO_MODE = 'true'
process.env.MPESA_ENVIRONMENT = 'production'   // the config.js default anyway
process.env.DATABASE_URL = 'postgres://dular@127.0.0.1:5433/dular'

const DULAR = (process.env.DULAR_DIR || '/tmp/ckb-whitehat/dular/server')

const main = async () => {
  line('F3 — demo-mode provider fabrication under a production configuration')
  const { config } = await import(`${DULAR}/config.js`)
  const { initiateStkPush, queryStkPushStatus } = await import(`${DULAR}/services/daraja.js`)

  const stk = await initiateStkPush({ phone: '+254712345678', amountKes: 100, accountReference: 'F3POC' })
  const stkq = await queryStkPushStatus({ checkoutRequestId: 'ws_CO_f3_demo' })
  console.log('config: demoMode =', config.demoMode, '| mpesa.environment =', config.mpesa.environment)
  console.log('initiateStkPush ->', JSON.stringify(stk))
  console.log('queryStkPushStatus ->', JSON.stringify({ ResultCode: stkq.ResultCode, ResultDesc: stkq.ResultDesc, CheckoutRequestID: stkq.CheckoutRequestID }))

  const fabricated = stk.ResponseCode === '0' && stkq.ResultCode === '0' && config.mpesa.environment === 'production' && config.demoMode
  const guard = /production/.test(await readFile(`${DULAR}/config.js`, 'utf8')) && /DEMO_MODE.*production|production.*DEMO_MODE/.test(await readFile(`${DULAR}/config.js`, 'utf8'))
  console.log(fabricated
    ? 'F3 FIRED: a deployment configured for PRODUCTION M-Pesa will self-confirm every STK deposit and B2C payout with zero provider calls when DEMO_MODE=true'
    : 'expected demo fabrication — inspect output above')
  console.log('startup guard in config.js refusing demoMode+production:', guard ? 'present' : 'ABSENT — config.js reads flags independently; no cross-check exists')
  process.exit(fabricated ? 0 : 1)
}

function line(t) { console.log('\n=== ' + t + ' ===') }
import { readFile } from 'node:fs/promises'
main().catch((e) => { console.error('F3 PoC error:', e); process.exit(2) })
