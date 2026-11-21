import type { StorageLike } from "./store";

class MemoryStorage implements StorageLike {
  private readonly data = new Map<string, string>();

  get length(): number {
    return this.data.size;
  }
  key(index: number): string | null {
    return [...this.data.keys()][index] ?? null;
  }
  getItem(key: string): string | null {
    return this.data.get(key) ?? null;
  }
  setItem(key: string, value: string): void {
    this.data.set(key, value);
  }
  removeItem(key: string): void {
    this.data.delete(key);
  }
}

let fallback: StorageLike | null = null;

/** localStore is the browser's localStorage, or memory for this page when the browser blocks it. */
export function localStore(): StorageLike {
  try {
    const s = window.localStorage;
    s.getItem("vukem.studio.probe");
    return s;
  } catch {
    fallback ??= new MemoryStorage();
    return fallback;
  }
}
