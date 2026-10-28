// Local transport/identity probe only. No application or Provider calls.
import assert from "node:assert/strict";
import http from "node:http";
import { pathToFileURL } from "node:url";
import { resolve } from "node:path";
import { observeSSEReader } from "../../../../../scripts/lib/sse-reader-observer.mjs";
const { chromium } = await import(
  pathToFileURL(
    resolve("backend/bin/go076-tools/node_modules/playwright-core/index.mjs"),
  ).href
);
const results = [],
  closed = new Set(),
  identities = [];
const server = http.createServer(async (req, res) => {
  if (req.url === "/") {
    res.end("<!doctype html><title>Local SSE transport probe</title>");
    return;
  }
  if (
    req.url !== "/api/ai/conversations/probe/messages" ||
    req.method !== "POST"
  ) {
    res.writeHead(404).end();
    return;
  }
  let raw = "";
  for await (const chunk of req) raw += chunk;
  const { mode } = JSON.parse(raw);
  res.on("close", () => {
    if (!res.writableFinished) closed.add(mode);
  });
  res.writeHead(200, {
    "Content-Type": "text/event-stream",
    "Cache-Control": "no-cache",
  });
  res.write("event: meta\ndata: {}\n\n");
  if (mode === "cancel") return;
  const finish = () => {
    res.write(
      'event: answer\ndata: {"text":"probe"}\n\nevent: done\ndata: {}\n\n',
    );
    if (mode === "fast") res.end();
    else setTimeout(() => res.end(), 300);
  };
  if (mode === "fast") finish();
  else setTimeout(finish, 30);
});
await new Promise((resolveReady) =>
  server.listen(0, "127.0.0.1", resolveReady),
);
const browser = await chromium.launch({
  executablePath: "C:/Program Files/Google/Chrome/Application/chrome.exe",
  headless: true,
});
try {
  const context = await browser.newContext(),
    page = await context.newPage();
  // The inner spy records native return references before the outer observer
  // attaches. Assertions below compare the exact objects visible to the caller.
  await page.addInitScript(() => {
    const nativeFetch = window.fetch;
    window.__nativeRefs = {};
    window.fetch = function (...args) {
      const refs = (window.__nativeRefs = {});
      const pending = Reflect.apply(nativeFetch, this, args);
      refs.fetchPromise = pending;
      void pending.then(
        (response) => {
          refs.response = response;
          const getReader = response.body.getReader;
          response.body.getReader = function (...readerArgs) {
            const reader = Reflect.apply(getReader, this, readerArgs);
            refs.reader = reader;
            const read = reader.read;
            reader.read = function (...readArgs) {
              const p = Reflect.apply(read, this, readArgs);
              refs.readPromise = p;
              void p.then(
                (result) => {
                  refs.readResult = result;
                },
                () => {},
              );
              return p;
            };
            refs.cancel = reader.cancel;
            return reader;
          };
        },
        () => {},
      );
      return pending;
    };
  });
  await observeSSEReader(page, {
    onStart(meta) {
      return { mode: JSON.parse(meta.postData).mode };
    },
    onFinish(value, text, state) {
      results.push({
        ...value,
        events: [...text.matchAll(/^event: (.+)$/gm)].map((m) => m[1]),
        ...state,
      });
    },
  });
  await page.goto(`http://127.0.0.1:${server.address().port}/`);
  for (const mode of ["fast", "done-cancel", "cancel"]) {
    const identity = await page.evaluate(async (mode) => {
      const same = {
        fetchPromise: true,
        response: true,
        reader: true,
        readPromise: true,
        readResult: true,
        cancelMethod: true,
        abortError: mode !== "cancel",
      };
      const abort = new AbortController();
      try {
        const p = fetch("/api/ai/conversations/probe/messages", {
          method: "POST",
          body: JSON.stringify({ mode }),
          signal: abort.signal,
        });
        same.fetchPromise = p === window.__nativeRefs.fetchPromise;
        const response = await p;
        same.response = response === window.__nativeRefs.response;
        const reader = response.body.getReader();
        same.reader = reader === window.__nativeRefs.reader;
        same.cancelMethod = reader.cancel === window.__nativeRefs.cancel;
        let text = "";
        while (true) {
          const reading = reader.read();
          same.readPromise &&= reading === window.__nativeRefs.readPromise;
          const result = await reading;
          same.readResult &&= result === window.__nativeRefs.readResult;
          if (result.done) break;
          text += new TextDecoder().decode(result.value);
          if (mode === "cancel") {
            abort.abort();
            try {
              await reader.read();
            } catch (error) {
              same.abortError = error.name === "AbortError";
            }
            break;
          }
          if (text.includes("event: done")) {
            await reader.cancel();
            break;
          }
        }
      } catch (error) {
        throw new Error(error.name);
      }
      return { mode, ...same };
    }, mode);
    identities.push(identity);
    assert.ok(
      Object.entries(identity).every(
        ([key, value]) => key === "mode" || value === true,
      ),
      "native identity or abort changed",
    );
    const deadline = Date.now() + 3000;
    while (
      (results.length < identities.length ||
        (mode === "cancel" && !closed.has(mode))) &&
      Date.now() < deadline
    )
      await new Promise((r) => setTimeout(r, 10));
    const result = results.find((r) => r.mode === mode);
    assert.ok(
      result && !result.captureError,
      `reader observation failed for ${mode}`,
    );
    assert.deepEqual(
      result.events,
      mode === "cancel" ? ["meta"] : ["meta", "answer", "done"],
    );
  }
  assert.ok(
    closed.has("cancel"),
    "observer must not keep aborted HTTP stream alive",
  );
  console.log(
    JSON.stringify({
      passed: true,
      paidCalls: 0,
      cases: results,
      identities,
      serverClosedOnAbort: closed.has("cancel"),
    }),
  );
} finally {
  await browser.close();
  await new Promise((resolveClose) => server.close(resolveClose));
}
