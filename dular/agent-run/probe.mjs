const rpc = async (url, method, params = []) => {
  const r = await fetch(url, { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ jsonrpc: '2.0', id: 1, method, params }) })
  return await r.text()
}
for (const url of ['http://127.0.0.1:8227', 'http://127.0.0.1:8327']) {
  console.log('==', url)
  console.log((await rpc(url, 'node_info')).slice(0, 1400))
  console.log('-- channels')
  console.log((await rpc(url, 'list_channels', [{}])).slice(0, 900))
  console.log('-- peers')
  console.log((await rpc(url, 'list_peers', [])).slice(0, 900))
}
