// The slice of the "bun:test" API that the unit tests use. It is declared here
// so that the type check covers the tests without a types package; `bun test`
// provides the real module at run time.
declare module "bun:test" {
  interface Matchers {
    toBe(expected: unknown): void;
    toEqual(expected: unknown): void;
    toBeLessThan(expected: number): void;
    toBeLessThanOrEqual(expected: number): void;
    toBeGreaterThan(expected: number): void;
    toBeGreaterThanOrEqual(expected: number): void;
    toBeNull(): void;
    toBeUndefined(): void;
    toContain(expected: string): void;
    toHaveLength(expected: number): void;
    toBeInstanceOf(expected: unknown): void;
    toThrow(expected?: unknown): void;
    not: Matchers;
    rejects: {
      toThrow(expected?: unknown): Promise<void>;
      toMatchObject(expected: unknown): Promise<void>;
      toBeInstanceOf(expected: unknown): Promise<void>;
    };
  }

  export function describe(name: string, fn: () => void): void;
  export function test(name: string, fn: () => void | Promise<void>): void;
  export function expect(actual: unknown): Matchers;
  export function afterEach(fn: () => void | Promise<void>): void;
}
