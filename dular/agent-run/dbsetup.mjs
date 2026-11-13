// Find a working Postgres role on 127.0.0.1:5433, create a fresh scratch DB
// named `dular_whitehat`, apply the target's schema.sql, return that DB's DSN.
import { readFileSync } from 'node:fs'
import { pg } from './deps.mjs'

const REPO = '/tmp/ckb-whitehat/dular2/repo'
const DB = 'dular_whitehat'
const PORT = 5433
const candidates = [
  { user: 'dular', password: 'dular' },
  { user: 'postgres', password: 'postgres' },
  { user: 'postgres', password: undefined },
  { user: 'dular', password: undefined },
  { user: 'root', password: 'root' },
  { user: 'root', password: undefined },
]

export async function ensureScratchDb() {
  const { Client } = pg
  let admin = null, auth = null
  for (const creds of candidates) {
    try {
      const c = new Client({ host: '127.0.0.1', port: PORT, user: creds.user, password: creds.password, database: 'postgres' })
      await c.connect()
      admin = c
      auth = creds
      break
    } catch { /* try next */ }
  }
  if (!admin) throw new Error('No Postgres role reachable on 127.0.0.1:' + PORT)
  await admin.query(`DROP DATABASE IF EXISTS ${DB}`)
  await admin.query(`CREATE DATABASE ${DB} OWNER ${admin.connectionParameters.user}`)
  await admin.end()

  const mig = new Client({ host: '127.0.0.1', port: PORT, user: auth.user, password: auth.password, database: DB })
  await mig.connect()
  await mig.query(readFileSync(`${REPO}/server/schema.sql`, 'utf8'))
  await mig.end()
  const password = auth.password ? `:${auth.password}` : ''
  return `postgres://${auth.user}${password}@127.0.0.1:${PORT}/${DB}`
}
