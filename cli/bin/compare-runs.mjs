#!/usr/bin/env node
// ckb-whitehat — compare a fresh automated run against the manual baseline.
//
//   node cli/bin/compare-runs.mjs <new-report.md> <baseline-report.md>
//
// Scores the new report against the baseline on:
//   - coverage: which baseline findings the new run rediscovered
//   - novelty:  findings in the new run that the baseline missed
//   - format:   presence of the required report sections
//
// Exit 0 always (this is a report, not a gate); prints a summary.

import fs from 'node:fs'

const [newPath, basePath] = process.argv.slice(2)
if (!newPath || !basePath) {
  console.error('usage: compare-runs.mjs <new-report.md> <baseline-report.md>')
  process.exit(2)
}
const read = (p) => (fs.existsSync(p) ? fs.readFileSync(p, 'utf8') : '')

// A finding id looks like "## F3 —" / "## S1 —" / "## C4.2 —"
const ids = (md) => {
  const out = []
  for (const m of md.matchAll(/^#{2,3}\s+((?:F|S|C4)\d+(?:\.\d+)?)\s*[—-]\s*(.+)$/gm)) {
    out.push({ id: m[1], title: m[2].trim().replace(/\s*\(.*\)$/, '') })
  }
  return out
}
const kw = (s) => s.toLowerCase().replace(/[^a-z0-9]+/g, ' ').split(' ').filter((w) => w.length > 3)
const overlap = (a, b) => {
  const A = new Set(kw(a)); const B = new Set(kw(b))
  if (!A.size || !B.size) return 0
  let hit = 0
  for (const w of A) if (B.has(w)) hit++
  return hit / Math.min(A.size, B.size)
}
// Match by CONTENT, never by id: two reports both number a finding "F4" but
// may mean completely different things, and id-matching produces false
// coverage claims. An id match is only trusted when the titles also agree.
const sameFinding = (a, b) => {
  const t = overlap(a.title, b.title)
  if (t >= 0.45) return true
  if (a.id === b.id && t >= 0.25) return true
  return false
}

const newMd = read(newPath)
const newF = ids(newMd)
const baseF = ids(read(basePath))

console.log(`baseline findings: ${baseF.length}   new-run findings: ${newF.length}\n`)

const covered = []
const missed = []
for (const b of baseF) {
  const hit = newF.find((n) => sameFinding(n, b))
  if (hit) { covered.push({ b, n: hit }) } else { missed.push(b) }
}
const novel = newF.filter((n) => !baseF.some((b) => sameFinding(n, b)))

console.log(`REDISCOVERED (${covered.length}/${baseF.length}):`)
for (const { b, n } of covered) console.log(`  ${b.id.padEnd(5)} ${b.title.slice(0, 58).padEnd(58)} <- ${n.id}`)
console.log(`\nMISSED by the new run (${missed.length}):`)
for (const b of missed) console.log(`  ${b.id.padEnd(5)} ${b.title.slice(0, 70)}`)
console.log(`\nNEW in the new run, absent from baseline (${novel.length}):`)
for (const n of novel) console.log(`  ${n.id.padEnd(5)} ${n.title.slice(0, 70)}`)

const need = ['## At a glance', 'On-chain evidence', 'Layer boundary', 'Honest limitations']
console.log('\nreport sections:')
for (const s of need) console.log(`  ${newMd.includes(s) ? 'ok  ' : 'MISS'} ${s}`)

console.log(`\ncoverage: ${covered.length}/${baseF.length} (${Math.round((covered.length / Math.max(baseF.length, 1)) * 100)}%)`)
