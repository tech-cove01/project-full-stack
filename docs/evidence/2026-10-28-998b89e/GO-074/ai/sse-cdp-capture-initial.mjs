// Passive Chromium CDP observation: no interception, response substitution,
// request replay, tee(), or extra body reader changes the application stream.
// Full event payloads stay in caller memory; callers must persist a safe subset.
export async function passiveSSECapture(context, page, { onStart, onFinish, maxBytes = 65536 }) {
  const session = await context.newCDPSession(page)
  const streams = new Map()
  function finish(stream) {
    if (!stream.terminal || stream.enabling || stream.finished) return
    stream.finished = true
    const bytes = Buffer.concat([stream.buffered, ...stream.chunks])
    onFinish(stream.value, bytes.toString('utf8'), {
      loadingFailed: stream.failed,
      captureError: stream.error || bytes.length > maxBytes,
    })
    streams.delete(stream.id)
  }
  function enable(stream) {
    if (stream.ready || stream.enabling) return
    stream.enabling = true
    const afterResponse = stream.responseSeen
    void session.send('Network.streamResourceContent', { requestId: stream.id }).then(result => {
      const bytes = Buffer.from(result.bufferedData || '', 'base64')
      if (bytes.length > maxBytes) stream.error = true
      stream.buffered = bytes.subarray(0, maxBytes)
      stream.ready = true
    }).catch(() => {
      // Some Chromium versions only expose the resource after response headers.
      // A second attempt at responseReceived is still passive and sends no HTTP.
      if (afterResponse) stream.error = true
    }).finally(() => {
      stream.enabling = false
      if (stream.responseSeen && !stream.ready && !stream.error) enable(stream)
      finish(stream)
    })
  }
  session.on('Network.requestWillBeSent', event => {
    const url = new URL(event.request.url)
    const value = onStart({
      method: event.request.method,
      pathname: url.pathname,
      postData: event.request.postData,
      authorizationPresent: Object.entries(event.request.headers).some(([name, value]) => name.toLowerCase() === 'authorization' && !!value),
    })
    if (!value) return
    const stream = { id: event.requestId, value, buffered: Buffer.alloc(0), chunks: [], bytes: 0, responseSeen: false, ready: false, enabling: false, terminal: false, failed: false, error: false, finished: false }
    streams.set(stream.id, stream)
    enable(stream)
  })
  session.on('Network.responseReceived', event => {
    const stream = streams.get(event.requestId)
    if (!stream) return
    stream.responseSeen = true
    stream.value.httpStatus = event.response.status
    enable(stream)
  })
  session.on('Network.dataReceived', event => {
    const stream = streams.get(event.requestId)
    if (!stream || !event.data) return
    const bytes = Buffer.from(event.data, 'base64')
    stream.bytes += bytes.length
    if (stream.bytes <= maxBytes) stream.chunks.push(bytes)
    else stream.error = true
  })
  for (const [name, failed] of [['Network.loadingFinished', false], ['Network.loadingFailed', true]]) {
    session.on(name, event => {
      const stream = streams.get(event.requestId)
      if (!stream) return
      stream.terminal = true
      stream.failed = failed
      finish(stream)
    })
  }
  await session.send('Network.enable', { maxResourceBufferSize: maxBytes, maxTotalBufferSize: maxBytes * 8 })
  return { close: () => session.detach() }
}
