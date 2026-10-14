/// <reference types="vitest/config" />
import tailwindcss from "@tailwindcss/vite";
import { tanstackRouter } from "@tanstack/router-plugin/vite";
import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";

export default defineConfig({
  plugins: [tanstackRouter({ target: "react", autoCodeSplitting: true }), react(), tailwindcss()],
  build: { outDir: "../internal/web/dist", emptyOutDir: true, assetsDir: "assets" },
  server: {
    proxy: { "/api": { target: "http://localhost:8080", ws: true } },
  },
  test: {
    environment: "jsdom",
    globals: true,
    setupFiles: ["./src/test/setup.ts"],
    include: ["src/**/*.test.{ts,tsx}"],
    // User-event tests run several seconds on a loaded CI runner; 5 s flakes.
    testTimeout: 20_000,
  },
});
