import { createLibp2p } from 'libp2p'
import { tcp } from '@libp2p/tcp'
import { circuitRelayTransport } from '@libp2p/circuit-relay-v2'
import { noise } from '@chainsafe/libp2p-noise'
import { yamux } from '@chainsafe/libp2p-yamux'
import { identify } from '@libp2p/identify'
import { multiaddr } from '@multiformats/multiaddr'

const [discoveryURL, accountA, accountB] = process.argv.slice(2)
const encoder = new TextEncoder()
const decoder = new TextDecoder()

function frame(object) {
  const body = encoder.encode(JSON.stringify(object))
  if (body.length > 127) throw new Error('test frame unexpectedly large')
  return Uint8Array.from([body.length, ...body])
}

function bytesOf(value) { return value?.subarray?.() ?? value }

async function response(stream) {
  return new Promise((resolve, reject) => {
    const chunks = []
    stream.addEventListener('message', event => {
      chunks.push(...bytesOf(event.data))
      if (chunks.length > 0 && chunks.length === chunks[0] + 1) {
        try { resolve(JSON.parse(decoder.decode(Uint8Array.from(chunks.slice(1))))) }
        catch (error) { reject(error) }
      }
    })
    stream.addEventListener('error', reject)
  })
}

async function createClient() {
  return createLibp2p({
    addresses: { listen: ['/ip4/127.0.0.1/tcp/0'] },
    transports: [tcp(), circuitRelayTransport()],
    connectionEncrypters: [noise()],
    streamMuxers: [yamux()],
    services: { identify: identify() }
  })
}

async function connectWithToken(node, token) {
  const http = await fetch(discoveryURL, { headers: { Authorization: `Bearer ${token}` }, redirect: 'manual' })
  if (http.status !== 200 || http.headers.get('cache-control') !== 'no-store') throw new Error(`discovery: ${http.status}`)
  const doc = await http.json()
  if (doc.version !== 1 || !Array.isArray(doc.relay.addresses) || doc.relay.addresses.length === 0) throw new Error('bad discovery document')
  const connection = await node.dial(multiaddr(doc.relay.addresses[0]), { signal: AbortSignal.timeout(10000) })
  // Noise has already authenticated this exact connection before the token is sent.
  if (connection.remotePeer.toString() !== doc.relay.peerId) throw new Error('Noise Peer ID mismatch')
  const stream = await connection.newStream('/clipp/relay-auth/1.0.0', { signal: AbortSignal.timeout(10000) })
  const reply = response(stream)
  stream.send(frame({ accessToken: token }))
  await stream.close()
  const auth = await reply
  if (auth.ok !== true || typeof auth.sessionExpiresAt !== 'string') throw new Error(`auth: ${JSON.stringify(auth)}`)
  return doc.relay.addresses[0]
}

const a = await createClient()
const b = await createClient()
try {
  const relayAddr = await connectWithToken(a, accountA)
  await connectWithToken(b, accountB)
  const received = new Promise((resolve, reject) => {
    a.handle('/clipp/interop/1.0.0', async data => {
      try {
        const stream = data.stream ?? data
        stream.addEventListener('message', event => resolve(decoder.decode(bytesOf(event.data))))
        stream.addEventListener('error', reject)
      } catch (error) { reject(error) }
    }, { runOnLimitedConnection: true })
  })
  await a.components.transportManager.listen([multiaddr(`${relayAddr}/p2p-circuit`)])
  const target = multiaddr(`${relayAddr}/p2p-circuit/p2p/${a.peerId.toString()}`)
  const stream = await b.dialProtocol(target, '/clipp/interop/1.0.0', { signal: AbortSignal.timeout(10000), runOnLimitedConnection: true })
  stream.send(encoder.encode('JS cross-account opaque bytes'))
  await stream.close()
  let timer
  const value = await Promise.race([received, new Promise((_, reject) => { timer = setTimeout(() => reject(new Error('relayed bytes timed out')), 10000) })])
  clearTimeout(timer)
  if (value !== 'JS cross-account opaque bytes') throw new Error(`wrong relayed bytes: ${value}`)
  console.log(JSON.stringify({ ok: true, relayPeer: relayAddr.split('/p2p/').at(-1), bytes: value.length }))
} finally {
  await b.stop()
  await a.stop()
}
