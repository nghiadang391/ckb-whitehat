// Discover the Postgres role/DB and the two FNN nodes so the PoC can target them.
import { pg } from './deps.mjs'

const candidates = [
  { user: 'dular', password: 'dular', db: 'dular' },
  { user: 'postgres', password: 'postgres', db: 'postgres' },
  { user: 'postgres', password: undefined, db: 'postgres' },
  { user: 'dular', password: undefined, db: 'dular' },
  { user: 'root', password: 'root', db: 'postgres' },
]
console.log('--- Postgres discovery (127.0.0.1:5433) ---')
for (const c of candidates) {
  try {
    const client = new pg.Client({ host: '127.0.0.1', port: 5433, user: c.user, password: c.password, database: c.db })
    await client.connect()
    const dbs = await client.query('SELECT datname FROM pg_database WHERE datistemplate = false')
    console.log(`OK  role=${c.user} db=${c.db} | databases=${dbs.rows.map(r => r.datname).join(',')}`)
    await client.end()
    break
  } catch (e) {
    console.log(`FAIL role=${c.user} db=${c.db} :: ${e.message.split('\n')[0]}`)
  }
}

console.log('--- FNN discovery ---')
for (const port of [8227, 8327]) {
  try {
    const r = await fetch(`http://127.0.0.1:${port}`, { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ jsonrpc: '2.0', id: 1, method: 'node_info', params: [] }) })
    const p = await r.json()
    console.log(`FNN ${port} pubkey=${p.result?.pubkey} channels=${p.result?.channel_count} pending=${p.result?.pending_channel_count} funding_lock=${p.result?.default_funding_lock_script?.args || 'none'}`)
  } catch (e) {
    console.log(`FNN ${port} :: ${e.message}`)
  }
}
