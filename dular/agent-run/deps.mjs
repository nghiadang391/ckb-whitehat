// Load the target's preinstalled vendored deps (pg) via createRequire so PoC
// scripts can run from the scratch dir without a local node_modules. This loads
// a third-party library, not the target's own source code.
import { createRequire } from 'node:module'

const require = createRequire(
  `${process.env.CKB_WHITEHAT_TARGET || '/tmp/ckb-whitehat/dular2/repo'}/package.json`,
)

export const pg = require('pg')
export const { Client } = pg
