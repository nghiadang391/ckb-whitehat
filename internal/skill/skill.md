---
name: ckb-whitehat
description: Pre-push defensive security verification for a security-sensitive project — AI triage of a local repository, named security invariant checks, deterministic local regression tests (PoCs) that verify observable state (local scratch DB, local devnet chain tx, local HTTP, exit code), and per-finding patch guidance. Primarily aimed at CKB / Fiber (FNN) money products, but the method generalizes to any project with money, state, auth, or chain interaction. Use when a developer asks to run pre-push defensive security verification / invariant audits on a CKB or Fiber project, or invokes /ckb-whitehat.
user-invocable: true
---

# ckb-whitehat

Pre-push **defensive security verification** for a project that holds **money,
state, keys, or auth**, ending in **deterministic local regression evidence +
patch guidance** so developers can catch and fix security defects before
pushing or deploying. CKB / Fiber (FNN) money products are the anchor lane, but
the method is general: the invariant failures are the same classes everywhere
(unauthenticated webhook handling, concurrent balance race, unsafe default
config, weak secret, unvalidated amount, PII exposure) — only the *evidence
substrate* (local devnet cells vs. a local scratch DB) changes.

**Standing rule: "AI finds, the verification test proves."** An AI reading a
repo produces a threat model (findings are hypotheses). No finding ships as
`PROVEN` until it terminates in one deterministic, replayable **local defensive
verification test** in `poc/`: a local test against mocked vendors and local
disposable Docker infrastructure proving that a required safety invariant fails
on unmodified source (e.g., an unauthenticated webhook accepted with mocked
vendor, a concurrent debit violating `balance >= 0` in scratch Postgres, an
unsafe config default, or an unintended settlement on the local devnet) —
reproducible with a single command against *unmodified source*. Never report an
AI opinion as a proven result, and never report a finding whose test was never
executed — an unrun test is not evidence.

---

## 1. Scope and consent — read before touching anything

Strictly **defensive, local pre-push verification** (developer safety gate).
Three hard lines, for **any** target:

1. **No live-service probing.** Never send requests to any deployed endpoints
   or live third-party vendors. All verification scripts run against a **local
   checkout/clone + local disposable instance** with external vendor boundaries
   (payment provider, external APIs) mocked/stubbed. Say so explicitly in the
   report.
2. **Local devnet / owned test infrastructure only.** When on-chain
   verification is enabled, use only the local Docker CKB devnet (`127.0.0.1:8114`),
   local 2-node Fiber (FNN) devnet (`127.0.0.1:8227` / `8327`), and isolated
   scratch Postgres (`127.0.0.1:5439`). Never target mainnet or any third-party
   node. (If a public testnet is ever explicitly configured instead of local
   devnet, word it as "impact confined to our own addresses and local
   services," **never** "isolated network.")
3. **Private developer workspace & responsible disclosure.** Verification scripts
   and `FINDINGS.md` stay local in the developer's scratch workspace so bugs
   can be patched before pushing. If used on a third-party/grant-funded repo,
   follow private report → patch window → publish only with the maintainer's
   written consent.

**Layer boundary:** we verify *how the project uses the chain and its own
code*, not that the underlying CKB/Fiber protocol or lock scripts have bugs.
Those are shared protocol assets. If a verification test trips an upstream
chain/FNN issue, link it and stop — don't claim it as a target finding. A RED
on-chain row means an **app** bug caused an unintended local devnet transfer
(the chain executed it faithfully); it is not a chain bug.

## 2. Cost (state up front — who pays what)

- **Local devnet + scratch DB:** free, zero external faucets required (disposable
  local Docker CKB devnet + 2-node FNN + scratch Postgres).
- **Mainnet:** strictly out of scope and hard-refused by the CLI.
- **AI/LLM analysis:** local authenticated agent CLI (`agy`, `claude`, or `codex`).

## 3. Method (per target)

```
0.  Inspect target in its local dir / neutral clone. With the CLI, the scratch
    workspace is `~/.ckb-whitehat/<slug>/` (verification scripts in `poc/`,
    report as `FINDINGS.md`, `run.log` alongside). Verification scripts are
    NEVER run from inside the target tree (untrusted-content rule: an
    interpreter loads code from its own cwd).
1.  Triage (AI-assisted): one pass over every security-relevant file. Map
    keygen/storage, amount parsing (BigInt vs Number/float), signing, the
    network/vendor trust boundary, balance/ledger, secrets-in-config, and any
    on-chain interaction. BEFORE touching the chain, grep the source for which
    chain/asset/RPC conventions the target actually uses.
2.  Threat model & invariants: define the core safety and money invariants the
    code must hold before shipping (no double-credit, balance >= 0 under
    concurrency, webhook authenticity, fail-closed production config, no
    unauthorized state transition).
3.  Security invariant checks, ranked. Each names: code region, local offline
    verification strategy (unauthenticated-webhook test / concurrency race
    test / boundary fuzz / config-matrix / secret-hygiene check). Keep to ~10;
    cut to ~5 for concise delivery.
4.  Defensive verification test (PoC): one deterministic script in `poc/` per
    accepted invariant hypothesis. Each runs locally against unmodified code
    with mocked external vendors, asserts the invariant, and prints a
    before/after or fail/pass line + `FINDING <id> FIRED` when the invariant
    fails, reproducible with one command. **Write it, then RUN it.** A test is
    only evidence once it has been executed against the target and its claimed
    output has been observed. Nothing reaches the report as PROVEN that has
    not been run (see "The verification test must actually run").
5.  State evidence (MANDATORY section, uniform across all targets). If the
    target holds on-chain or persistent state that a bug can move, verify it
    on the local disposable devnet — baseline-green control first, then the
    app-invariant trace (local devnet tx/payment hash recorded via
    `recordChainEvidence()`).
    If the target has NO such state (pure logic library): write exactly one
    line — "N/A — target has no on-chain/persistent-asset interaction;
    findings terminate in code state, not txs." Never omit the section.
6.  Patch guidance: per finding, the minimal defensive fix. For concurrency or
    config invariants, include the *working fixed version* when practical and
    re-run the same test to show it now passes (regression test: fail before,
    pass after).
7.  Report: FINDINGS.md (template below).
8.  Verify: every verification script reproduces (re-run it); mark any finding
    whose test cannot run as UNPROVEN — an unrun script is not a PROVEN
    finding. Also confirm no real secret material (provider keys, OTPs, phone
    PII, real private keys) leaked into `poc/` or `FINDINGS.md`.
```

### Token-efficiency rules (learned from real runs — these prevent the dead ends)
- **Bound every wait with a decision point.** A pending/confirming tx, a
  settling claim, or a stuck channel gets a *max* wait (e.g. 10–15 min), then
  a decision: confirm the chain is advancing (tip delta over ~40s), and choose
  wait / re-open / ask the user. Do NOT sit on an unbounded poll for an hour.
- **Never mask a query error as an empty result.** `.catch(() => [])` turned a
  gateway "Invalid request" into "0 cells" and we wrongly concluded the asset
  didn't exist. Distinguish *error* from *empty*; on error, switch the read
  path (e.g. `get_transaction`/`get_cells` over a flaky indexer).
- **Confirm the target's exact chain/testnet before funding anything.** Funding
  an asset on a testnet the target's nodes don't watch wastes the whole path.
  One source grep (the target's RPC URL / explorer link) is cheaper than a
  funding detour.
- **Reuse settled state; record by hash.** A `ChannelReady` channel or a
  settled payment already on-chain is reused, not re-fired; re-verify by its
  recorded hash. (FNN: a duplicate `send_payment` for the same invoice is
  rejected — use a unique invoice per run.)
- **One clean, parameterized script over many disposable probes.** A handful
  of throwaway `.mjs` files is where tokens leak; fold them into one.
- **Read the source for the answer before hitting the chain.** The target's
  config usually says which chain/asset/explorer it uses — cheaper than probing.

### PoC quality bar (borrowed from no-mistakes)
- **Assert observable state, not string-greps of your own source.** A finding's
  PoC must *execute* the interface and assert a machine-checkable result: a DB
  balance, a ledger entry, an HTTP body/status, an exit code, a tx hash, a
  channel state. Matching source text is not proof — matching text can be dead
  or commented out. (This is "AI finds, the PoC proves," made concrete.)
- **For a fix, the regression fails before and passes after** the patch, in
  the same run.
- **Split findings by who decides the fix.** Mechanical/low-risk findings you
  patch yourself ("auto-fix"); findings that change product behavior or assume
  intent you escalate to the user ("ask-user"). Severity maps onto this.
- **Quoted output is captured, not composed.** The PoC's own quotes must come
  from a captured run (the real stdout/stderr you saw), never paraphrased or
  predicted. The harness must end with one machine-readable verdict line —
  `FINDING <id> FIRED`, `FINDING <id> UNPROVEN`, or
  `FINDING <id> STRUCTURAL` — so a reader (or the CLI) can check the claim
  without reading the script.

### The verification test must actually run
- A verification script that was written but never executed is **not a PROVEN
  finding.** "AI finds, the verification test proves" means the script has to
  have run.
- If a script errors (syntax error, bad import, wrong method), the defect is in
  **your** test harness — fix the script until it runs. Do not reinterpret a
  crash or a 500 as "the target held the invariant."
- Never write a quoted "run output" block you did not observe from an actual
  run. If you cannot get it to run, report the finding as **UNPROVEN** with
  the exact error you saw.
- The report must distinguish: **PROVEN** (executed, output observed) vs
  **UNPROVEN** (reasoned from code but not executed) vs **STRUCTURAL** (static
  code-shape issue). Only PROVEN findings may claim verified state movement.
- A verification script that needs the target's internal state set up (a
  specific DB row in local scratch Postgres, a local devnet invoice) is fine —
  set that state up explicitly inside the script and say so in the report.

### Domain cheatsheet: CKB + Fiber (only what actually costs tokens to rediscover)
- **Mainnet vs testnet (do not mix these up):** `ckb`/`lina`
  (explorer.nervos.org) is **production mainnet — never fund it, never open
  channels against it, and treat any PoC that would move real CKB as out of
  scope**. **Pudge is the testnet** (explorer `pudge.explorer.nervos.org`,
  faucets via `testnet.ckbapp.dev` / `testnet.ckb.dev`); Meepo is the
  current protocol edition shared by both networks. A target configured
  with `chain: ckb` / `lina` is on mainnet — a Pudge claim never appears
  where a node watching mainnet looks.
- **Outpoint:** `funding_tx_hash (64 hex) ‖ index (8 hex)`. A channel's
  `channel_outpoint` first-64-hex is its funding tx. Derive the funding tx from
  the outpoint (FNN 0.9.1 `list_channels` does **not** return
  `funding_tx_hash`).
- **UDT amount:** in a standard CKB sUDT cell, `data` is the **first 16 bytes
  (32 hex chars) as a little-endian u128** — `0x00ca9a3b0000…` is 1,000,000,000
  base units (the byte order flips the value). 1 RUSD = 100 000 000 base units.
  Note **Fiber channels do not read this field**: Fiber tracks a channel's
  RUSD balance over RPC (`list_channels` → `local_balance`/`remote_balance`)
  and sends amounts as `amount: hex(baseUnits)` + `udt_type_script`. Reach for
  cell `data` only when reading an on-chain sUDT cell directly.
- **Channel state machine:** NegotiatingFunding → AwaitingTxSignatures (funding
  tx signed, awaiting the peer's signature + broadcast) → AwaitingChannelReady
  (tx broadcast, awaiting on-chain confirmation) → ChannelReady (confirmed).
  Both peers sit in AwaitingChannelReady while the funding tx confirms;
  AwaitingTxSignatures means the funding tx is not yet broadcast.
- **UDT channel funding:** RUSD must come from cells under the node's
  secp256k1-blake160 ckb-account lock; CKB capacity alone cannot fund a UDT
  channel.
- **Payment idempotency:** FNN keeps one payment session per invoice; a
  concurrent duplicate `send_payment` for the same invoice → "Payment session
  already exists."
- **Gateway RPC reality:** `get_transaction` works; `get_transaction_status` →
  "Method not found"; `send_transaction` → 403 (read-only gateways). Use
  `get_transaction` + `get_cells` as the reliable read path; the REST indexer
  is flaky. When scanning for a UDT cell, **filter by the RUSD type script** —
  any cell with non-empty `output_data` is not your UDT.

**Deeper reference (fetch only when a specific question needs it, don't ingest
upfront):** CKB — https://docs.nervos.org/ · Fiber — https://www.fiber.world/docs

### Offline defensive invariant-test recipes (fast, deterministic)
Each recipe below is a local verification strategy, not a deliverable: **every
recipe you use must be executed and must end in a `FINDING <id> ...` verdict
line**, or the finding is UNPROVEN. A static grep alone has not run.
- Boot the target's app or import its handlers locally (local scratch Postgres,
  its own `schema.sql`/`migrate.js`), with all external vendors mocked/stubbed
  at `node:https`/`fetch`. Zero live network calls.
- **Webhook-authentication invariant class:** send a local unauthenticated or
  unverified callback fixture to the local Express app; assert that the handler
  rejects it rather than crediting a balance with zero mocked vendor calls.
- **Concurrency / non-negative balance invariant class:** run N parallel debits
  = balance/N against local scratch Postgres (`$DATABASE_URL`); assert final
  balance ≥ 0 and Σentries == balance. Real local Postgres is needed because
  the fix hinges on exact `FOR UPDATE` / advisory-lock / `CHECK` semantics.
  Note when the repo already has fix primitives (`withTransaction`,
  `withAdvisoryLock`) unused on the money path.
- **Config-matrix invariant class:** enumerate dangerous flag combinations (e.g.
  demo-mode × production, missing `SESSION_SECRET`); assert the app fails
  closed when an unsafe combination is configured.
- **Input-validation fuzz class:** small deterministic corpus (`0`, `-1`,
  `1e999`, `"abc"`, `"0x…"`, `"1.5"`); assert rejection *before* any outbound
  call. Credit any clean path (`BigInt` throughout) so the report stays honest.
- **Secret-strength & PII-hygiene invariant class:** verify locally whether
  defaults fall back to hardcoded strings, whether low-entropy OTP/PIN hashes
  lack salts/KDFs, or whether public endpoints expose unmasked PII.

### Common mistakes to avoid (hard-won from live runs — read before running tools)
These exact pitfalls burned 20–30 wasted turns in real runs. Avoid every one:

1. **Never hunt the host filesystem for DB passwords or credentials.**
   - Use **only** the `$DATABASE_URL` already provided in your environment
     (auto-provisioned isolated Docker scratch Postgres).
   - Never `grep`, `find`, or run Node scripts over `$HOME`, `/etc`, `/var`,
     `/opt`, `/root`, or shell profiles looking for Postgres passwords,
     `.pgpass`, or `.env` files, and never spawn ad-hoc host `initdb`/`postgres`
     clusters.
   - If `$DATABASE_URL` is absent or unreachable after 1 check, mark DB-backed
     findings `UNPROVEN` ("DB unavailable"), note it in `Honest limitations`,
     and move on to provable findings.
2. **Hard cap of 2 diagnostic probes — stop writing `_probe_*.mjs` chains.**
   - Do not create a series of throwaway `_probe_*.mjs` scripts to debug the
     environment. If an import, DB, or RPC probe fails twice, treat it as a
     real blocker and move directly to writing verification tests that *can* run.
3. **Never retry denied Bash commands with workarounds.**
   - The CLI enforces a strict high-risk denylist (`rm -rf`, `git push`,
     `git reset --hard`, `sudo`, `curl`, `wget`, `ssh`, `psql`, `env`,
     `printenv`, live web fetches, MCP, and writes inside the target repo).
   - Never try to bypass a denied command; use `node` (with `pg` or `fetch` to
     `127.0.0.1` only) and built-in file tools instead.
   - Set custom env vars inside `.mjs` test scripts (`process.env.KEY = ...; await import(...)`)
     instead of prefixing `KEY=VAL node ...` on the shell line.
4. **Node ESM imports from `<workspace>/poc/`.**
   - Node ESM ignores `NODE_PATH` for bare specifiers imported directly by a
     PoC inside `<workspace>/poc/`.
   - Modules imported *from inside the target repo* (`await import('/abs/target/server/config.js')`)
     resolve `<target>/node_modules` automatically.
   - If your PoC script itself needs a package installed in `<target>/node_modules`
     (such as `pg`), load it cleanly via `createRequire('<target>/package.json')('pg')`
     from `node:module` — do not guess `NODE_PATH` or `cd` tricks.
5. **CKB / Fiber devnet RPC conventions (for `--chain` runs).**
   - Import `../packs/chain-client.mjs` and read `$CKB_WHITEHAT_CHAIN_CONFIG`
     instead of guessing RPC method names (`get_tip_block_number` exists on CKB,
     not `get_tip_block_hash`; Fiber v0.10 uses `pubkey` in `node_info` and
     `open_channel`, and PascalCase `ChannelReady`).
   - Always record settled hashes via `recordChainEvidence(...)` and cite the
     exact 32-byte `0x...` tx/payment hashes in `## On-chain evidence` so
     strict post-run RPC verification passes.

## 4. What to dodge (from the committee's rejection record)

Don't let a finding read as any of these — the exact reasons proposals were
rejected: **duplicates an existing primitive / protocol-core work /
custody-adjacent third-party / deprecated stack / vague deliverable
("analysis without a shipped artifact") / prior under-delivery.** Frame every
finding as *community-owned verification/tooling that sits beside the core
repo*, with a concrete, dated, verifiable artifact. Pick targets you can
actually finish in 4–8 weeks.

## 5. FINDINGS.md template

```
# ckb-whitehat — <target> v<ver>
Target: <url> (<license>, <kind>). PoCs run against unmodified source:
`<one command>`. No network, no live service; <if on-chain: impact confined to
our own addresses on a public testnet>.

Severity = impact on a <merchant/user/holder> weighted by how a real attacker
reaches it.

## At a glance
ONE overview table per layer up top, single-line rows; deep analysis goes in
the detail sections that follow, in the same order.

### Offline findings
| # | Finding (one clause) | Sev | Status (PROVEN/UNPROVEN/STRUCTURAL) | Money-path? |
|---|---|---|---|---|
| **F1** | <one clause> | <sev> | PROVEN (fired) | yes/no |

Status states, exactly:
- **PROVEN** — the PoC ran and its claimed output was observed. Only a PROVEN
  row may say "fired" and only a PROVEN row may claim money movement.
- **UNPROVEN** — reasoned from code but the PoC did not run (or errored). Not
  a finding yet; say what blocked it.
- **STRUCTURAL** — a static/code-shape observation (no executable test),
  e.g. a dangerous default or a missing check.

### On-chain / state evidence
| Trace | App finding proven | Result (GREEN/RED/SAFE) | payment/tx hash |
|---|---|---|---|
| **C1** | control | GREEN | `0x…` |
A RED row requires a trace that was actually executed and observed on our own
testnet addresses. A finding whose on-chain trace never ran is UNPROVEN — it
cannot produce a RED row; write "not executed" in the trace column instead.

## F1 — <name> (<SEV>) — <PROVEN|UNPROVEN|STRUCTURAL>
<code region + why the default is the foot-gun>
PoC (run output, captured from an actual run):
```
<before -> after, or fail/pass lines, quoted — only if it actually ran>
FINDING F1 FIRED
```
For an UNPROVEN finding, replace the block with:
```
PoC not executed — error: <exact error observed>
FINDING F1 UNPROVEN
```
Impact: <one concrete, non-exaggerated line. Say if it needs a hostile/sloppy
caller — don't overstate "remotely exploitable" for a library default.>
Patch: <minimal fix> (for races/config: "fix applied, harness now passes")

## F2 — …

## On-chain evidence (mandatory section; N/A with one line if no such state)

**What this section is:** the on-chain *state* produced by the app findings —
NOT a hunt for bugs in the chain. Label every row with the finding it proves
so a RED row is never read as a chain bug:
- **GREEN** = control; the honest path works.
- **RED** = an app finding produced this on-chain money movement; the chain
  faithfully executed the app's bug.
- **SAFE** = the chain face held (e.g. duplicate payment rejected), so the
  defect stays in the app's own ledger/state layer. Say so explicitly.

One table (do NOT repeat the overview table here) + the funding chain
(transfer → channel → block, balances reconcile):
| Trace | App finding proven | Outcome | Payment/tx hash |
|---|---|---|---|
| C1 | control | GREEN — `0.1 RUSD` settled | `0x…` |
Reproduce: `<one command>`. Every hash is in `<record-file>`.

## Layer boundary
we tested how the target uses the chain, not the chain protocol itself; any
upstream chain/FNN issue tripped was linked, not claimed.

## Honest limitations
<1–5 bullets: mocked boundaries, default-path-not-remote-exploit,
probabilistic vs deterministic PoC, "our addresses on a shared testnet"
wording. **If any finding is UNPROVEN, list it here by id and say what
blocked it** — an unlisted UNPROVEN finding reads as PROVEN.>
```

## 6. Delivery & disclosure

- Nothing is published without the maintainer's OK; PoCs stay in the scratch
  dir. The *private report → patch window → consent-then-public* flow **is**
  the consent protocol being demonstrated.
- A second target proves the method scales case-by-case; pick a *money
  product* (stronger impact than a pure library).
