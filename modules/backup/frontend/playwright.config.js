import { defineConfig } from "@playwright/test";
import os from "node:os";
import path from "node:path";

export default defineConfig({
  testDir: "./e2e",
  fullyParallel: true,
  outputDir:
    process.env.BACKUP_E2E_ARTIFACTS ||
    path.join(os.tmpdir(), "fnos-backup-center-e2e"),
  use: {
    baseURL: "http://127.0.0.1:18766",
    viewport: { width: 1536, height: 1024 },
    headless: true,
  },
  webServer: {
    command: "npm run dev -- --port 18766 --strictPort",
    url: "http://127.0.0.1:18766",
    reuseExistingServer: !process.env.CI,
  },
});
