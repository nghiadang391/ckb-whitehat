// Reusable CKB/Fiber attack pack: UDT Decimal, BigInt & Precision Invariant Corpus.
// Feeds adversarial amount strings/numbers to any target amount-parser or quote
// calculator to catch float precision loss, negative/NaN/Infinity acceptance,
// scientific-notation bypasses, and shannon/UDT decimal drift.

export const ADVERSARIAL_AMOUNTS = [
  '0',
  '-1',
  '-0.00000001',
  '0.000000001',         // sub-satoshi / below 8-decimal UDT precision
  '0.1',                 // IEEE-754 float drift (0.1 + 0.2 !== 0.3)
  '0.29',                // 0.29 * 1e8 === 28999999.999999996 in JS Number!
  '9007199254740993',    // Number.MAX_SAFE_INTEGER + 2 (loses precision in float64)
  '1e-8',                // scientific notation
  '1e30',                // huge scientific notation
  'NaN',
  'Infinity',
  '-Infinity',
  '100_000',
  '0x10',
  '100.5.5',
  ' 100 ',
  '',
  null,
  undefined,
]

export function fuzzAmountParser(parseFn, { expectReject = new Set(['-1', '-0.00000001', 'NaN', 'Infinity', '-Infinity', '100.5.5', '', null, undefined]) } = {}) {
  const violations = []
  for (const input of ADVERSARIAL_AMOUNTS) {
    let threw = false
    let result
    try {
      result = parseFn(input)
    } catch {
      threw = true
    }
    if (expectReject.has(input) && !threw && result !== null && result !== undefined && result !== false) {
      violations.push({ input, issue: 'invalid amount accepted', result })
    }
    if (input === '0.29' && !threw && typeof result === 'number' && !Number.isInteger(result)) {
      violations.push({ input, issue: 'float precision drift on 0.29', result })
    }
    if (input === '9007199254740993' && !threw && typeof result === 'number' && String(result) !== '9007199254740993') {
      violations.push({ input, issue: 'BigInt precision lost beyond MAX_SAFE_INTEGER', result })
    }
  }
  return { tested: ADVERSARIAL_AMOUNTS.length, violations }
}
