// ckb-whitehat — shared target resolution for the baseline Dular PoCs.
//
// Every PoC in this directory resolves the audited target the same way:
//
//   DULAR_DIR points at the REPOSITORY ROOT (e.g. /tmp/ckb-whitehat/dular2/repo).
//   For backward compatibility DULAR_DIR may also point at a `server/` dir;
//   we detect which by looking for `server/schema.sql` (root) vs `schema.sql`
//   (server dir).
//
// Third-party dependencies (`pg`) are resolved through createRequire() against
// the TARGET's package.json — this directory has no package.json of its own,
// so a bare `import 'pg'` would fail here. Same trick as agent-run/deps.mjs,
// but self-contained so the baseline suite stays runnable on its own.

import { existsSync, readFileSync } from 'node:fs'
import path from 'node:path'
import { createRequire } from 'node:module'
import { pathToFileURL } from 'node:url'

const DEFAULT_TARGET = '/tmp/ckb-whitehat/dular2/repo'

/**
 * Resolve { repoRoot, serverDir } from DULAR_DIR.
 * Accepts either the repo root or a `server/` directory.
 */
export function resolveTarget(env = process.env) {
  const raw = env.DULAR_DIR || DEFAULT_TARGET
  const abs = path.resolve(raw)

  if (existsSync(path.join(abs, 'server', 'schema.sql'))) {
    return { repoRoot: abs, serverDir: path.join(abs, 'server'), source: 'repo-root' }
  }
  if (existsSync(path.join(abs, 'schema.sql')) && existsSync(path.join(abs, 'db.js'))) {
    const parent = path.dirname(abs)
    const repoRoot = existsSync(path.join(parent, 'package.json')) ? parent : abs
    return { repoRoot, serverDir: abs, source: 'server-dir' }
  }
  throw new Error(
    `DULAR_DIR=${abs} is neither a Dular repo root (no server/schema.sql) `
    + 'nor a Dular server/ dir (no schema.sql + db.js).',
  )
}

/** Import a module from the target by absolute path. */
export function targetImport(serverDir, relativePath) {
  return import(pathToFileURL(path.join(serverDir, relativePath)).href)
}

/** require() bound to the target's package.json — resolves the target's node_modules. */
export function targetRequire(repoRoot) {
  return createRequire(path.join(repoRoot, 'package.json'))
}

/** The target's own `pg` module (resolved from the target's node_modules). */
export function targetPg(repoRoot) {
  return targetRequire(repoRoot)('pg')
}

// --------------------------------------------------------------------- scratch DB

const PG_CANDIDATES = [
  { user: 'dular', password: 'dular' },
  { user: 'postgres', password: 'postgres' },
  { user: 'postgres', password: undefined },
  { user: 'dular', password: undefined },
  { user: 'root', password: 'root' },
  { user: 'root', password: undefined },
]

/**
 * Create a throwaway database seeded with the target's own schema.sql and
 * return its handle.
 *
 * The PoCs never touch a production DATABASE_URL: they point the target at an
 * isolated scratch database on our local Postgres, and drop it again on
 * teardown, so no PoC row can survive into any shared database
 * (AGENTS.md rule 6 — strict test-database isolation).
 */
export async function ensureScratchDb(repoRoot, { name = 'dular_poc', port = 5433, host = '127.0.0.1' } = {}) {
  const { Client } = targetPg(repoRoot)
  let admin = null
  let creds = null
  for (const candidate of PG_CANDIDATES) {
    const client = new Client({ host, port, user: candidate.user, password: candidate.password, database: 'postgres' })
    try {
      await client.connect()
      admin = client
      creds = candidate
      break
    } catch {
      await client.end().catch(() => {})
    }
  }
  if (!admin) throw new Error(`No Postgres role reachable on ${host}:${port}`)

  await admin.query(`DROP DATABASE IF EXISTS ${name}`)
  await admin.query(`CREATE DATABASE ${name} OWNER ${admin.connectionParameters.user}`)
  await admin.end()

  const migrate = new Client({ host, port, user: creds.user, password: creds.password, database: name })
  await migrate.connect()
  await migrate.query(readFileSync(path.join(repoRoot, 'server', 'schema.sql'), 'utf8'))
  await migrate.end()

  const password = creds.password ? `:${creds.password}` : ''
  return {
    url: `postgres://${creds.user}${password}@${host}:${port}/${name}`,
    name,
    host,
    port,
    user: creds.user,
    password: creds.password,
    repoRoot,
  }
}

/** Drop the scratch database (best effort) so no PoC row outlives the run. */
export async function dropScratchDb(db) {
  if (!db) return
  const { Client } = targetPg(db.repoRoot || DEFAULT_TARGET)
  const admin = new Client({ host: db.host, port: db.port, user: db.user, password: db.password, database: 'postgres' })
  try {
    await admin.connect()
    await admin.query(`DROP DATABASE IF EXISTS ${db.name}`)
  } catch {
    /* best effort */
  } finally {
    await admin.end().catch(() => {})
  }
}

/** Console line helper shared by the PoCs. */
export function line(t) {
  console.log('\n=== ' + t + ' ===')
}
