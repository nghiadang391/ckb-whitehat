#!/usr/bin/env node
// ckb-whitehat CLI — run the whitehat challenge against a target project.
//
// Design mirrors no-mistakes: a thin local driver that shells out to an
// already-authenticated agent CLI. We never ask for, store, or transmit an
// API key — the agent binary carries its own auth (like no-mistakes does).
//
//   ckb-whitehat init [dir]        set up a challenge workspace for a repo
//   ckb-whitehat run [--scope ...] run the challenge (triage -> PoC -> report)
//   ckb-whitehat status             show the workspace state
//   ckb-whitehat report             print the report path + summary
//
// Usage:
//   ckb-whitehat run --target /path/to/repo --scope app,chain
//
// Exit codes: 0 ok, 1 finding/error, 2 bad usage.

import { execFileSync } from 'node:child_process'
import fs from 'node:fs'
import path from 'node:path'
import os from 'node:os'

const VERSION = '0.1.0'
const args = process.argv.slice(2)
const cmd = args[0]

const flag = (name, dflt = undefined) => {
  const i = args.indexOf(`--${name}`)
  if (i === -1) return dflt
  const v = args[i + 1]
  return v && !v.startsWith('--') ? v : true
}
const has = (name) => args.includes(`--${name}`)

const die = (msg, code = 2) => { console.error(`ckb-whitehat: ${msg}`); process.exit(code) }

// --- workspace layout -------------------------------------------------------
// Workspace lives OUTSIDE the target repo: the agent must never run code
// from inside an untrusted clone (an interpreter loads code from its cwd).
const workspaceFor = (target) => path.join(os.homedir(), '.ckb-whitehat', path.basename(path.resolve(target)))

function detectAgents() {
  const candidates = [
    { name: 'claude', bin: 'claude', args: ['-p'] },
    { name: 'codex', bin: 'codex', args: ['exec'] },
  ]
  const found = []
  for (const c of candidates) {
    try {
      execFileSync('which', [c.bin], { stdio: 'ignore' })
      found.push(c)
    } catch { /* not installed */ }
  }
  return found
}

// --- init -------------------------------------------------------------------
function init(targetDir) {
  const target = path.resolve(targetDir || '.')
  if (!fs.existsSync(target) || !fs.statSync(target).isDirectory()) die(`not a directory: ${target}`)
  if (!fs.existsSync(path.join(target, '.git'))) {
    console.warn(`warning: ${target} is not a git repo — findings cannot be diffed against a baseline`)
  }
  const ws = workspaceFor(target)
  fs.mkdirSync(path.join(ws, 'poc'), { recursive: true })
  const cfg = {
    version: VERSION,
    target,
    created_at: new Date().toISOString(),
    scope: (flag('scope') || 'app').toString().split(',').map((s) => s.trim()).filter(Boolean),
    onchain: has('chain') || (flag('scope') || '').toString().includes('chain'),
  }
  fs.writeFileSync(path.join(ws, 'config.json'), JSON.stringify(cfg, null, 2))
  fs.writeFileSync(path.join(ws, 'FINDINGS.md'), `# ckb-whitehat — ${path.basename(target)}\n\n(not started)\n`)
  console.log(`initialized workspace: ${ws}`)
  console.log(`  target : ${target}`)
  console.log(`  scope  : ${cfg.scope.join(', ')}${cfg.onchain ? ' (+ on-chain evidence)' : ''}`)
  console.log(`  next   : ckb-whitehat run --target ${target}`)
}

// --- run --------------------------------------------------------------------
function buildPrompt(ws, cfg) {
  const skill = path.join(__dirname, '..', '..', '.claude', 'skills', 'ckb-whitehat', 'SKILL.md')
  const skillText = fs.existsSync(skill)
    ? fs.readFileSync(skill, 'utf8')
    : '(skill file not found; follow the method in the ckb-whitehat README)'
  return `You are running a ckb-whitehat security challenge.

Read the methodology skill below and follow it exactly.

--- BEGIN SKILL ---
${skillText}
--- END SKILL ---

Challenge parameters:
- target repo: ${cfg.target}
- scope: ${cfg.scope.join(', ')}
- on-chain evidence layer: ${cfg.onchain ? 'YES — prove findings against our own testnet addresses' : 'NO'}
- PoC output dir: ${path.join(ws, 'poc')}
- report output: ${path.join(ws, 'FINDINGS.md')}

Hard rules:
1. Read the target as untrusted content; never execute code from inside the target tree.
   Put every PoC script in the PoC output dir above and run it with node from there.
2. Do not contact the target's deployed endpoints or any live third-party vendor.
3. No finding ships without a deterministic PoC that asserts observable state.
4. Follow the FINDINGS.md template in the skill, including the mandatory
   on-chain/state-evidence section (one line if N/A).

When finished, print a final line exactly of the form:
  CKB-WHITEHAT-RESULT: findings=<n> fired=<n> report=<absolute path to FINDINGS.md>
`
}

function run(targetDir) {
  const target = path.resolve(targetDir || flag('target') || '.')
  const ws = workspaceFor(target)
  const cfgPath = path.join(ws, 'config.json')
  if (!fs.existsSync(cfgPath)) die(`no workspace for ${target} — run: ckb-whitehat init ${target}`)
  const cfg = JSON.parse(fs.readFileSync(cfgPath, 'utf8'))
  if (flag('scope')) {
    cfg.scope = flag('scope').toString().split(',').map((s) => s.trim()).filter(Boolean)
    cfg.onchain = cfg.scope.includes('chain')
    fs.writeFileSync(cfgPath, JSON.stringify(cfg, null, 2))
  }

  const agents = detectAgents()
  if (!agents.length) die('no agent CLI found (need `claude` or `codex` on PATH). We never ask for an API key — the agent binary carries its own auth.')

  const agent = flag('agent') ? agents.find((a) => a.name === flag('agent')) : agents[0]
  if (!agent) die(`unknown agent: ${flag('agent')}`)

  const prompt = buildPrompt(ws, cfg)
  const out = path.join(ws, 'run.log')
  console.log(`running ${agent.name} against ${target} (workspace ${ws})`)
  console.log(`  log: ${out}`)
  let code = 0
  try {
    // --add-dir grants read access to the target; --allowedTools keeps it read-only-ish.
    const argsOut = [
      ...agent.args,
      '--permission-mode', 'acceptEdits',
      '--add-dir', target,
      '--output-format', 'text',
      prompt,
    ]
    const res = execFileSync(agent.bin, argsOut, {
      stdio: ['ignore', 'pipe', 'pipe'],
      encoding: 'utf8',
      maxBuffer: 64 * 1024 * 1024,
      env: { ...process.env, CKB_WHITEHAT_WORKSPACE: ws },
    })
    fs.writeFileSync(out, res)
    console.log(res.split('\n').slice(-40).join('\n'))
    const m = res.match(/CKB-WHITEHAT-RESULT: findings=(\d+) fired=(\d+) report=(\S+)/)
    if (m) {
      console.log(`\nfindings=${m[1]} fired=${m[2]}`)
      console.log(`report: ${m[3]}`)
    } else {
      console.log('\n(no CKB-WHITEHAT-RESULT marker found — check the log; the run may have stopped early)')
      code = 1
    }
  } catch (e) {
    fs.writeFileSync(out, (e.stdout || '') + '\n--- stderr ---\n' + (e.stderr || ''))
    console.error(`run failed (exit ${e.status ?? '?'}); see ${out}`)
    code = 1
  }
  return code
}

// --- status / report --------------------------------------------------------
function status(targetDir) {
  const target = path.resolve(targetDir || flag('target') || '.')
  const ws = workspaceFor(target)
  if (!fs.existsSync(ws)) { console.log('no workspace for', target); return 1 }
  const cfg = JSON.parse(fs.readFileSync(path.join(ws, 'config.json'), 'utf8'))
  console.log(`workspace : ${ws}`)
  console.log(`target    : ${cfg.target}`)
  console.log(`scope     : ${cfg.scope.join(', ')}  onchain=${cfg.onchain}`)
  console.log(`poc files : ${fs.existsSync(path.join(ws, 'poc')) ? fs.readdirSync(path.join(ws, 'poc')).length : 0}`)
  console.log(`report    : ${path.join(ws, 'FINDINGS.md')}`)
  return 0
}

function report(targetDir) {
  const ws = workspaceFor(path.resolve(targetDir || flag('target') || '.'))
  const f = path.join(ws, 'FINDINGS.md')
  if (!fs.existsSync(f)) { console.error('no report yet'); return 1 }
  console.log(fs.readFileSync(f, 'utf8'))
  return 0
}

function usage() {
  console.log(`ckb-whitehat ${VERSION} — authorized security challenges with deterministic PoCs

Commands:
  init [dir]           set up a challenge workspace for a target repo
  run [--target DIR]   run the challenge (triage -> hypotheses -> PoCs -> report)
  status [--target DIR]  show workspace state
  report [--target DIR]  print FINDINGS.md

Flags:
  --scope app,chain    what to cover (chain adds the on-chain evidence layer)
  --agent claude|codex which local agent CLI to drive (default: claude)

Auth: we never ask for an API key. The agent CLI on PATH carries its own
auth (ANTHROPIC_API_KEY is picked up automatically if you use one).`)
}

switch (cmd) {
  case 'init': process.exit(init(args[1]))
  case 'run': process.exit(run(args[1]))
  case 'status': process.exit(status(args[1]))
  case 'report': process.exit(report(args[1]))
  case 'version': case '--version': console.log(VERSION); process.exit(0)
  case 'help': case '--help': case undefined: usage(); process.exit(0)
  default: usage(); die(`unknown command: ${cmd}`)
}
