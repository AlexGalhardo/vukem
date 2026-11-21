import { defineConfig, devices } from "@playwright/test";

// The specs start the real vukem-studio binary (see e2e/fixtures.ts), so the
// strict CSP, the session token and the embedded build are all under test.
export default defineConfig({
  testDir: "./e2e",
  fullyParallel: false,
  workers: 1,
  retries: 0,
  reporter: [["list"]],
  outputDir: "./test-results",
  projects: [{ name: "chromium", use: { ...devices["Desktop Chrome"] } }],
});
