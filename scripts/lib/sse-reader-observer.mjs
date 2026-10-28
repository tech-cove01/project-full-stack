// Test-only observation of bytes already read by the Vue application. This adds
// small callbacks; it is not a wire capture. No tee, clone, extra reader or read.
export async function observeSSEReader(
  page,
  { onStart, onFinish, maxBytes = 65536 },
) {
  const streams = new Map();
  await page.exposeBinding("__go074ObserveSSE", (_source, event) => {
    if (event.kind === "start") {
      const value = onStart(event.meta);
      if (value)
        streams.set(event.id, { value, text: "", bytes: 0, error: false });
      return;
    }
    const stream = streams.get(event.id);
    if (!stream) return;
    if (event.kind === "headers") stream.value.httpStatus = event.status;
    if (event.kind === "chunk") {
      stream.bytes += Buffer.byteLength(event.text);
      if (stream.bytes <= maxBytes) stream.text += event.text;
      else stream.error = true;
    }
    // Vue cancels its reader at protocol done, before HTTP EOF. Finish from the
    // terminal event it actually consumed, or the original read's EOF/error.
    if (
      event.kind === "finish" ||
      /(?:^|\n)event: (?:done|error)\r?\ndata: [^\n]*\r?\n\r?\n/.test(
        stream.text,
      )
    ) {
      streams.delete(event.id);
      onFinish(stream.value, stream.text, {
        loadingFailed: !!event.failed,
        captureError: stream.error,
      });
    }
  });
  await page.addInitScript(() => {
    const originalFetch = window.fetch;
    let serial = 0;
    const notify = (event) => {
      try {
        void window.__go074ObserveSSE(event).catch(() => {});
      } catch {}
    };
    window.fetch = function (...args) {
      let meta;
      try {
        const [input, init] = args;
        const url = new URL(
          typeof input === "string" || input instanceof URL ? input : input.url,
          location.href,
        );
        const method = (init?.method || input?.method || "GET").toUpperCase();
        if (
          url.origin === location.origin &&
          method === "POST" &&
          /^\/api\/ai\/conversations\/[^/]+\/messages$/.test(url.pathname)
        ) {
          meta = {
            method,
            pathname: url.pathname,
            postData: init?.body,
            authorizationPresent: new Headers(
              init?.headers || input?.headers,
            ).has("authorization"),
          };
        }
      } catch {}
      const id = ++serial;
      if (meta) notify({ kind: "start", id, meta });
      const pending = Reflect.apply(originalFetch, this, args);
      if (!meta) return pending;
      void pending
        .then(
          (response) => {
            try {
              notify({ kind: "headers", id, status: response.status });
              const body = response.body;
              if (!body) {
                notify({ kind: "finish", id });
                return;
              }
              const originalGetReader = body.getReader;
              body.getReader = function (...readerArgs) {
                const reader = Reflect.apply(
                  originalGetReader,
                  this,
                  readerArgs,
                );
                const originalRead = reader.read;
                const decoder = new TextDecoder();
                reader.read = function (...readArgs) {
                  const reading = Reflect.apply(originalRead, this, readArgs);
                  void reading
                    .then(
                      (result) => {
                        try {
                          const text = result.done
                            ? decoder.decode()
                            : decoder.decode(result.value, { stream: true });
                          if (text) notify({ kind: "chunk", id, text });
                          if (result.done) notify({ kind: "finish", id });
                        } catch {
                          notify({ kind: "finish", id, failed: true });
                        }
                      },
                      () => notify({ kind: "finish", id, failed: true }),
                    )
                    .catch(() => {});
                  return reading;
                };
                return reader;
              };
            } catch {
              notify({ kind: "finish", id, failed: true });
            }
          },
          () => notify({ kind: "finish", id, failed: true }),
        )
        .catch(() => {});
      return pending;
    };
  });
}
