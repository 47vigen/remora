import { defineConfig } from "@hey-api/openapi-ts";

export default defineConfig({
  input: "../api/openapi.yaml",
  output: "src/client",
  // The live-log WebSocket is opened by hand; it has no generated client.
  parser: { filters: { operations: { exclude: ["GET /api/logs/live"] }, orphans: true } },
  plugins: ["@hey-api/client-fetch", "@tanstack/react-query"],
});
