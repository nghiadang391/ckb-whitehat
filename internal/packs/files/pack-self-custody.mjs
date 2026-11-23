// Reusable CKB/Fiber attack pack: Browser & Server Self-Custody Security Checks (BSS).
// Inspects target source/config for predictable default secrets, weak PIN/KDF
// parameters, and plaintext private-key / mnemonic exposure in web storage or logs.
import fs from 'node:fs'
import path from 'node:path'

const PATTERNS = [
  { id: 'predictable-default-secret', re: /(?:SESSION_SECRET|JWT_SECRET|HMAC_SECRET)\s*\|\|\s*['"`][^'"`]{3,}['"`]/g },
  { id: 'plaintext-key-storage', re: /localStorage\.setItem\([^)]*(?:priv|key|seed|mnemonic|secret)/gi },
  { id: 'weak-pin-kdf', re: /pbkdf2(?:Sync)?\([^)]*,\s*(?:[1-9]\d{0,3})\s*,/g },
  { id: 'hardcoded-privkey-hex', re: /(?:private_?key|privkey|secret_?key)\s*[:=]\s*['"`](?:0x)?[0-9a-fA-F]{64}['"`]/gi },
]

export function scanSelfCustodySignals(targetDir, relFiles) {
  const hits = []
  for (const rel of relFiles) {
    const full = path.join(targetDir, rel)
    if (!fs.existsSync(full)) continue
    const text = fs.readFileSync(full, 'utf8')
    for (const { id, re } of PATTERNS) {
      re.lastIndex = 0
      let m
      while ((m = re.exec(text)) !== null) {
        const line = text.slice(0, m.index).split('\n').length
        hits.push({ rule: id, file: rel, line, snippet: m[0].slice(0, 120) })
      }
    }
  }
  return hits
}
