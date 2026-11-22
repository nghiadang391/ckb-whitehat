---
name: ckb-whitehat
description: Authorized whitehat security challenge for a security-sensitive project — AI triage of a target repo, named attack hypotheses, deterministic replayable PoCs that terminate in observable state (DB, chain tx, HTTP, exit code), and per-finding patch guidance. Primarily aimed at CKB / Fiber (FNN) money products, but the method generalizes to any project with money, state, auth, or chain interaction. Use when the user asks to whitehat/audit/hack-test a CKB or Fiber project, run a security challenge against a grant-funded repo, or invokes /ckb-whitehat.
user-invocable: true
---

# ckb-whitehat

Authorized security challenge of a project that holds **money, state, keys, or
auth**, ending in **deterministic, replayable evidence + patch guidance**. CKB /
Fiber (FNN) money products are the anchor lane, but the method is general:
the findings are the same classes everywhere (forged callback, race, config
foot-gun, weak secret, unvalidated amount, PII leak) — only the *evidence
substrate* (on-chain cells vs. a local DB) changes.

**Standing rule: "AI finds, the PoC proves."** An AI reading a repo produces a
threat model (findings are hypotheses). No finding is deliverable until it
terminates in one deterministic, replayable artifact: an exploit transaction,
a forged input that lands a bad state, a race that drains a balance —
reproducible with a single command against *unmodified source*. Never report an
AI opinion as a result, and never report a finding whose PoC was never
executed — an unrun PoC is not evidence.

---

## 1. Scope and consent — read before touching anything

Authorized, defensive/educational work. Three hard lines, for **any** target:

1. **No live-service probing.** Never send requests to the target's deployed
   endpoints. All PoCs run against a **local clone + local instance** with
   vendor boundaries (payment provider, chain RPC, third-party API) stubbed.
   Say so explicitly in the report.
2. **On-chain evidence is ours only, on a public testnet.** If the target
   touches the chain, use our own 2-node local FNN stack, our addresses, our
   local Postgres, faucet currency. A testnet is a *shared public chain* —
   word it as "impact confined to our own addresses and local services,"
   **never** "isolated network."
3. **The target is a live project (often grant-funded).** Findings stay private
   in our scratch dir. Disclosure = private report → patch window → publish
   only with the maintainer's written consent.

**Layer boundary:** we prove *how the project uses the chain / its own code*,
not that the chain protocol or its lock scripts have bugs. Those are shared
protocol assets. If a PoC trips an upstream chain/FNN issue, link it and stop —
don't claim it as our finding. A RED on-chain row means an **app** bug moved
money on-chain (the chain executed it faithfully); it is not a chain bug.

## 2. Cost (state up front — who pays what)

- **Testnet asset + capacity:** free, from public faucets. Faucets go down —
  plan a fallback asset with the same mechanics.
- **Mainnet:** out of scope unless explicitly requested; declare it a
  follow-on with the real cost, not in this run.
- **AI/LLM analysis:** an allowed explicit line item on the AI-assisted track;
  budget it honestly and name who pays.

## 3. Method (per target)

```
0.  Clone target to a neutral dir (e.g. /tmp/ckb-whitehat/<name>/ - its own
    new empty dir). With the CLI, the workspace is `~/.ckb-whitehat/<slug>/`
    (PoC scripts in `poc/`, report as `FINDINGS.md`, `run.log` alongside).
    Either way: PoC scripts are NEVER run from inside the cloned tree
    (untrusted-content rule: an interpreter loads code from its own cwd).
1.  Triage (AI-assisted): one pass over every security-relevant file. Map
    keygen/storage, amount parsing (BigInt vs Number/float), signing, the
    network/vendor trust boundary, balance/ledger, secrets-in-config, and any
    on-chain interaction. BEFORE touching the chain, grep the source for which
    testnet/chain/asset the target actually uses — that one read prevents the
    most expensive dead end (funding a chain the target never watches).
2.  Threat model: attacker classes + the money invariants the code must hold
    (no double-credit, no negative balance, callback authenticity, no free
    state transition).
3.  Attack hypotheses, ranked. Each names: code region, offline test strategy
    (forged input / race / fuzz / config-matrix / static assert). Keep to ~10;
    cut to ~5 for solo delivery.
4.  PoC: one deterministic block per accepted hypothesis. Each prints a
    before/after or a fail/pass line, reproducible with one command. **Write
    it, then RUN it.** A PoC is only a PoC once it has been executed against the
    target and its claimed output has been observed. Nothing reaches the
    report that has not been run (see "The PoC must actually run").
5.  State evidence (MANDATORY section, uniform across all targets). If the
    target holds on-chain or persistent state that a finding can move, prove
    it there by running the PoC — baseline-green first, then the trace that
    makes the finding concrete (forged credit → real testnet asset moves, tx
    hash recorded).
    If the target has NO such state (pure logic library): write exactly one
    line — "N/A — target has no on-chain/persistent-asset interaction;
    findings terminate in code state, not txs." Never omit the section.
6.  Patch guidance: per finding, the minimal fix. For races/config, include
    the *working fixed version* and re-run the same PoC to show it now passes
    (the regression pack: fail before, pass after).
7.  Report: FINDINGS.md (template below).
8.  Verify: every PoC reproduces (re-run it); drop any finding whose PoC you
    cannot make run — an unrun PoC is not a finding. Also a `grep -rn` sweep
    confirms no real secret
    material (provider keys, OTPs, phone PII, chain hex keys) leaked into the
    PoCs or report — only structure.
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

### The PoC must actually run
- A PoC that was written but never executed is **not a finding. Do not report
  it.** "AI finds, the PoC proves" means the PoC has to have run.
- If a PoC errors (syntax error, bad import, wrong method), the defect is in
  **your** harness — fix the PoC until it runs. Do not reinterpret a crash
  or a 500 as "the target rejected the exploit."
- Never write a quoted "run output" block you did not observe from an actual
  run. If you cannot get it to run, report the finding as **UNPROVEN** with
  the exact error you saw.
- The report must distinguish: **PROVEN** (executed, output observed) vs
  **UNPROVEN** (reasoned from code but not executed). Only PROVEN findings
  may claim money movement.
- A PoC that needs the target's internal state set up (a specific DB row, a
  funded address) is fine — set that state up explicitly inside the PoC and
  say so in the report.

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

### Offline PoC recipes (fast, committee-grade)
Each recipe below is a strategy, not a deliverable: **every recipe you use
must be executed and must end in a `FINDING <id> ...` verdict line**, or the
finding is UNPROVEN. A Static-class "PoC" that is only a grep has not run.
- Boot the target's app locally (its own `DEMO_MODE`/local Postgres, its own
  `migrate.js`), vendors stubbed at `node:https`/`fetch`. No live calls.
- **Forged-callback class:** POST a forged provider success; assert a ledger
  credit lands **with zero vendor calls made**.
- **Race class:** N parallel debits = balance/N; assert final balance ≥ 0 and
  Σentries == balance. Use real local Postgres (the fix hinges on exact
  `FOR UPDATE` / advisory-lock semantics, not pg-mem). Note when the repo
  already has the fix primitives (`withTransaction`, `withAdvisoryLock`) unused
  on the money path.
- **Config-matrix class:** enumerate the dangerous flag pairs (e.g.
  demo-mode × production); assert the app refuses to start/credit when a bad
  pair coexists.
- **Fuzz class:** small corpus (0, -1, 1e999, "abc", "0x…", "1.5"); assert
  rejection *before* any outbound call. Credit any clean path (BigInt
  throughout) so the report isn't all-red.
- **Static class:** grep + offline brute-force demo (e.g. a known default
  secret → a captured hash is trivially recovered).

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
- **STRUCTURAL** — a static/code-shape observation (no executable exploit),
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
