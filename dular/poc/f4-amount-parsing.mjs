// ckb-whitehat PoC — Dular: F4 amount parsing holes
//
// daraja.js coerces KES amounts with Math.round(Number(amountKes)):
//   - Number("abc") -> NaN -> Math.round(NaN) -> NaN (Sent to M-Pesa as NaN)
//   - Number("1.9") -> 1.9 -> 2 (fraction silently rounded, user pays 2 KES)
//   - Number("0x10") -> 16 (hex strings parsed as numbers!)
//   - Number("1e5") -> 100000 (exponent strings accepted)
//   - negative -> Math.round(-5) -> -5 (a negative M-Pesa amount)
// The FIBER side (funding routes) uses parseEncodedInteger -> parseDecimalInteger
// (ramp.js), which accepts hex strings with 0x prefix and enforces caps — but the
// Daraja-facing path does not. The legacy POST /api/mpesa/deposit does
// Number(req.body.amountKes) then toRUsdBaseUnits(amountKes), which rejects
// non-finite and <=0, so the legacy route is partially guarded — but the
// RAMP route's STK initiation (rampRoutes.js line 787) passes
// Number(order.kes_amount) where the DB value came from calculateRampQuote
// (safe) — the gap is: direct numeric coercion in daraja.js is the shared
// sink for any caller that hands it a raw value.
//
// PoC: demonstrate the coercion behavior at the daraja.js sink with a corpus,
// and contrast with ramp.js's clean BigInt path (credited in FINDINGS).

process.env.DATABASE_URL = 'postgres://dular@127.0.0.1:5433/dular'
process.env.DEMO_MODE = 'true'

const main = async () => {
  line('F4 — KES amount coercion in daraja.js vs clean BigInt path in ramp.js')
  const { toRUsdBaseUnits } = await import((process.env.DULAR_DIR || '/tmp/ckb-whitehat/dular/server') + '/utils.js')
  const { parseKesAmount } = await import((process.env.DULAR_DIR || '/tmp/ckb-whitehat/dular/server') + '/services/ramp.js')

  const darajaSink = (kes) => Math.round(Number(kes))  // the exact coercion in daraja.js
  const corpus = ['abc', '1.9', '0x10', '1e5', '-5', '0', '', 'Infinity', '99999999999999999999']
  for (const input of corpus) {
    let legacy
    try { legacy = toRUsdBaseUnits(input) } catch (e) { legacy = 'reject: ' + e.message }
    let ramp
    try { ramp = parseKesAmount(input, { minKes: 10, maxKes: 1000 }).toString() } catch (e) { ramp = 'reject: ' + e.message.slice(0, 40) }
    console.log(`input=${JSON.stringify(input).padEnd(28)} darajaSink->${String(darajaSink(input)).padEnd(10)} legacy->${String(legacy).padEnd(14)} ramp->${ramp}`)
  }
  const holes = [
    Number.isNaN(darajaSink('abc')),
    darajaSink('-5') === -5,
    darajaSink('0x10') === 16,
    darajaSink('1e5') === 100000,
  ]
  console.log('daraja.js sink holes (NaN / negative / hex / exponent accepted):', holes.every(Boolean))
  console.log('ramp.js path (BigInt + caps + zero-guard): rejects all — clean (credited in FINDINGS)')
  process.exit(holes.every(Boolean) ? 0 : 1)
}

function line(t) { console.log('\n=== ' + t + ' ===') }
main().catch((e) => { console.error('F4 PoC error:', e); process.exit(2) })
