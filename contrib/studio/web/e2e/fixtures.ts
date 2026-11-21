import { execFileSync, spawn } from "node:child_process";
import type { ChildProcess } from "node:child_process";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { test as base } from "@playwright/test";

const moduleDir = resolve(import.meta.dirname, "..", "..");

export interface Studio {
  /** The start URL with the one-time token. */
  startUrl: string;
  /** The origin, for example http://127.0.0.1:41234. */
  origin: string;
}

interface Built {
  dir: string;
  binary: string;
  database: string;
}

function build(): Built {
  const dir = mkdtempSync(join(tmpdir(), "studio-e2e-"));
  const binary = join(dir, `vukem-studio${process.platform === "win32" ? ".exe" : ""}`);
  const database = join(dir, "fixture.sqlite");

  execFileSync("go", ["build", "-o", binary, "./cmd/vukem-studio"], { cwd: moduleDir, stdio: "inherit" });
  execFileSync("go", ["run", "./web/e2e/fixture/main.go", database], { cwd: moduleDir, stdio: "inherit" });

  return { dir, binary, database };
}

function start(built: Built, args: string[]): Promise<{ studio: Studio; child: ChildProcess }> {
  const child = spawn(
    built.binary,
    ["--no-open", "--port", "0", "--dir", built.dir, "--database-url", `file:${built.database}`, ...args],
    { env: { ...process.env, APP_ENV: "development" }, stdio: ["ignore", "pipe", "pipe"] },
  );

  return new Promise((resolveStart, reject) => {
    let out = "";
    const timer = setTimeout(() => reject(new Error(`vukem-studio printed no start URL:\n${out}`)), 30_000);

    child.stdout?.on("data", (chunk: Buffer) => {
      out += chunk.toString();
      const match = /^\s+open\s+(http:\/\/\S+)$/m.exec(out);
      if (match?.[1]) {
        clearTimeout(timer);
        resolveStart({ studio: { startUrl: match[1], origin: new URL(match[1]).origin }, child });
      }
    });
    child.on("exit", (code) => {
      clearTimeout(timer);
      reject(new Error(`vukem-studio exited with ${code}:\n${out}`));
    });
  });
}

// The binary is built once per worker. The start token is single-use, so every
// test starts its own server on a free port and stops it afterwards.
export const test = base.extend<{ studio: Studio; writeStudio: Studio }, { built: Built }>({
  built: [
    // Playwright requires an object pattern here.
    // eslint-disable-next-line no-empty-pattern
    async ({}, use) => {
      const built = build();
      await use(built);
      rmSync(built.dir, { recursive: true, force: true, maxRetries: 5 });
    },
    { scope: "worker" },
  ],
  studio: async ({ built }, use) => {
    const { studio, child } = await start(built, []);
    await use(studio);
    child.removeAllListeners("exit");
    child.kill();
  },
  writeStudio: async ({ built }, use) => {
    const { studio, child } = await start(built, ["--write"]);
    await use(studio);
    child.removeAllListeners("exit");
    child.kill();
  },
});

export { expect } from "@playwright/test";
