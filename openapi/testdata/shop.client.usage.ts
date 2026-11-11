// Written by hand, not generated: the executable specification of what the
// client promises. tsc --strict must compile it, which proves error narrows
// by status and data narrows once error is checked.
import { ApiError, createClient, type CallOptions, type Problem } from "./shop.client";

async function narrow(): Promise<void> {
  const api = createClient();
  const { data, error } = await api.productsStore({ params: { shop: 1 }, body: { name: "x" } });

  if (error) {
    if (error.status === 422) {
      const problem: Problem = error.value; // fails to compile if not narrowed
      void problem;
    }
    if (error.status === 401) {
      const problem: Problem = error.value;
      void problem;
    }
    if (error.status === 500) {
      // Any other status carries the generic problem, or nothing.
      const detail: string | undefined = error.value?.detail;
      void detail;
    }
    return;
  }

  const id: number | undefined = data.id; // data is Product here, not null
  void id;
}

async function noContent(): Promise<void> {
  const api = createClient();
  const result = await api.productsDestroy({ params: { id: "1" } });
  if (result.error === null) {
    const nothing: void = result.data;
    void nothing;
  }
  const status: number = result.status;
  const headers: Headers = result.headers;
  const response: Response = result.response;
  void [status, headers, response];
}

async function perCall(): Promise<void> {
  const api = createClient({
    baseUrl: "https://api.example.com",
    headers: async () => ({ Authorization: "Bearer token" }),
    credentials: "include",
    onRequest: (_path, init) => ({ ...init, cache: "no-store" }),
    onResponse: async (response) => {
      void response.status;
    },
  });
  const controller = new AbortController();
  const options: CallOptions = { signal: controller.signal, headers: { "X-Trace": "1" } };
  const { data } = await api.productsIndex({ query: { q: "lamp" } }, options);
  void data;
  await api.productsIndex(); // the arguments are optional when nothing is required
}

async function throwing(): Promise<void> {
  const api = createClient({ throwHttpError: true });
  try {
    await api.productsStore({ params: { shop: 1 }, body: { name: "x" } });
  } catch (err) {
    if (err instanceof ApiError) {
      const errors: Record<string, string[]> = err.errors;
      void errors;
    }
  }
}

void [narrow, noContent, perCall, throwing];
