import { defineConfig, devices } from "@playwright/test";

// Smoke tests against a running remora: the e2e harness starts the image, so there is no webServer here.
// REMORA_URL points at it; PLAYWRIGHT_CHROMIUM_EXECUTABLE optionally selects a Chromium that is already installed.
export default defineConfig({
  testDir: "e2e",
  forbidOnly: !!process.env.CI,
  // The tests share one admin session store and one key list; keep them in order.
  workers: 1,
  reporter: "list",
  use: {
    baseURL: process.env.REMORA_URL ?? "http://localhost:8080",
    trace: "retain-on-failure",
  },
  projects: [
    {
      name: "chromium",
      use: {
        ...devices["Desktop Chrome"],
        launchOptions: { executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE || undefined },
      },
    },
  ],
});
