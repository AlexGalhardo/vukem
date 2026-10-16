// Entry point bundled by Vite ({{vite "resources/js/app.ts"}} in the layout).
// htmx is vendored in resources/public/js and loaded by the layout, so this
// file only holds your own scripts.

// Upload progress for forms marked data-upload
// (resources/views/components/upload.html). htmx 4 sends requests with
// fetch, which cannot report upload progress, so those requests go through
// XMLHttpRequest instead; everything else about the request is unchanged.
type HtmxRequest = RequestInit & { headers?: Record<string, string> };

type HtmxContext = {
  sourceElement: Element;
  fetch?: (url: string, init: HtmxRequest) => Promise<Response>;
};

type HtmxEvent = CustomEvent<{ ctx: HtmxContext }>;

function uploadProgress(event: Event): HTMLProgressElement | null {
  const { ctx } = (event as HtmxEvent).detail;
  return ctx.sourceElement.closest("form[data-upload]")?.querySelector("progress") ?? null;
}

function parseHeaders(raw: string): Headers {
  const headers = new Headers();
  for (const line of raw.trim().split(/[\r\n]+/)) {
    const i = line.indexOf(":");
    if (i > 0) headers.append(line.slice(0, i).trim(), line.slice(i + 1).trim());
  }
  return headers;
}

function fetchWithProgress(progress: HTMLProgressElement) {
  return (url: string, init: HtmxRequest): Promise<Response> =>
    new Promise((resolve, reject) => {
      const xhr = new XMLHttpRequest();
      xhr.open(init.method ?? "POST", url);
      for (const [name, value] of Object.entries(init.headers ?? {})) {
        xhr.setRequestHeader(name, value);
      }

      xhr.upload.addEventListener("progress", (e) => {
        if (e.lengthComputable) progress.value = (e.loaded / e.total) * 100;
      });
      xhr.addEventListener("load", () => {
        const empty = [101, 204, 205, 304].includes(xhr.status);
        resolve(
          new Response(empty ? null : xhr.responseText, {
            status: xhr.status,
            statusText: xhr.statusText,
            headers: parseHeaders(xhr.getAllResponseHeaders()),
          }),
        );
      });
      xhr.addEventListener("error", () => reject(new TypeError("Network request failed")));
      xhr.addEventListener("abort", () => reject(new DOMException("Upload aborted", "AbortError")));
      init.signal?.addEventListener("abort", () => xhr.abort());

      xhr.send((init.body as XMLHttpRequestBodyInit | null | undefined) ?? null);
    });
}

document.addEventListener("htmx:before:request", (event) => {
  const progress = uploadProgress(event);
  if (!progress) return;

  progress.value = 0;
  progress.hidden = false;
  (event as HtmxEvent).detail.ctx.fetch = fetchWithProgress(progress);
});

document.addEventListener("htmx:finally:request", (event) => {
  const progress = uploadProgress(event);
  if (progress) progress.hidden = true;
});

export {};
