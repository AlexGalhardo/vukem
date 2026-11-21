import { execFileSync, spawn } from "node:child_process";
import { randomBytes } from "node:crypto";
import { connect } from "node:net";
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

/**
 * The MongoDB and Redis servers the specs browse: the services of the
 * integration tests (task services:up), or the ones named by
 * STUDIO_E2E_MONGO_URL and STUDIO_E2E_REDIS_URL. The specs seed a database and
 * a key prefix of their own and remove them afterwards.
 */
export interface Services {
  available: boolean;
  /** Why the servers are not available. */
  reason: string;
  mongoUrl: string;
  mongoDb: string;
  redisUrl: string;
  /** Every Redis key the specs use starts with this. */
  prefix: string;
}

const mongoUrl = process.env.STUDIO_E2E_MONGO_URL ?? "mongodb://localhost:57017";
const redisUrl = process.env.STUDIO_E2E_REDIS_URL ?? "redis://localhost:56379/0";

function reachable(url: string): Promise<boolean> {
  const u = new URL(url);
  return new Promise((resolveProbe) => {
    const socket = connect({ host: u.hostname, port: Number(u.port), timeout: 1500 });
    socket.on("connect", () => {
      socket.destroy();
      resolveProbe(true);
    });
    socket.on("error", () => resolveProbe(false));
    socket.on("timeout", () => {
      socket.destroy();
      resolveProbe(false);
    });
  });
}

async function seed(): Promise<Services> {
  const id = randomBytes(4).toString("hex");
  const services: Services = { available: false, reason: "", mongoUrl, mongoDb: `studio_e2e_${id}`, redisUrl, prefix: `e2e:${id}:` };
  const down = (await Promise.all([reachable(mongoUrl), reachable(redisUrl)])).map((up, i) => (up ? "" : ["MongoDB", "Redis"][i])).filter(Boolean);
  if (down.length > 0) {
    services.reason = `${down.join(" and ")} not reachable (start them with task services:up, or set STUDIO_E2E_MONGO_URL and STUDIO_E2E_REDIS_URL)`;
    return services;
  }
  execFileSync("go", ["run", "./web/e2e/fixture/services.go", "seed", mongoUrl, services.mongoDb, redisUrl, services.prefix], { cwd: moduleDir, stdio: "inherit" });
  services.available = true;
  return services;
}

function unseed(s: Services): void {
  if (s.available) {
    execFileSync("go", ["run", "./web/e2e/fixture/services.go", "clean", s.mongoUrl, s.mongoDb, s.redisUrl, s.prefix], { cwd: moduleDir, stdio: "inherit" });
  }
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
interface Fixtures {
  studio: Studio;
  writeStudio: Studio;
  liveStudio: Studio;
  /** Studio over the SQLite fixture plus MongoDB and Redis (read-only). */
  serviceStudio: Studio;
  /** The same with --write. */
  serviceWriteStudio: Studio;
  services: Services;
}

// A spec that needs the services is skipped, with the reason, when they are not
// reachable on a developer machine; in CI (CI is set) it fails instead, so a
// missing service can never pass silently.
function requireServices(services: Services): void {
  if (services.available) {
    return;
  }
  if (process.env.CI) {
    throw new Error(`the MongoDB and Redis specs need the services: ${services.reason}`);
  }
  base.skip(true, services.reason);
}

function serviceArgs(s: Services): string[] {
  return ["--mongo-url", s.mongoUrl, "--mongo-database", s.mongoDb, "--redis-url", s.redisUrl];
}

export const test = base.extend<Fixtures, { built: Built; seeded: Services }>({
  seeded: [
    // Playwright requires an object pattern here.
    // eslint-disable-next-line no-empty-pattern
    async ({}, use) => {
      const seeded = await seed();
      await use(seeded);
      unseed(seeded);
    },
    { scope: "worker" },
  ],
  services: async ({ seeded }, use) => {
    requireServices(seeded);
    await use(seeded);
  },
  serviceStudio: async ({ built, services }, use) => {
    const { studio, child } = await start(built, serviceArgs(services));
    await use(studio);
    child.removeAllListeners("exit");
    child.kill();
  },
  serviceWriteStudio: async ({ built, services }, use) => {
    const { studio, child } = await start(built, ["--write", ...serviceArgs(services)]);
    await use(studio);
    child.removeAllListeners("exit");
    child.kill();
  },
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
  // Writes are on so a spec can change the data behind the grid, and the live interval is short.
  liveStudio: async ({ built }, use) => {
    const { studio, child } = await start(built, ["--write", "--refresh", "500ms"]);
    await use(studio);
    child.removeAllListeners("exit");
    child.kill();
  },
});

export { expect } from "@playwright/test";
