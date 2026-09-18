import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

export default defineConfig({
  plugins: [react()],
  base: "./",
  server: { proxy: { "/app/backup_center/api": "http://127.0.0.1:18765" } },
  test: {
    environment: "jsdom",
    setupFiles: "./src/test-setup.js",
    include: ["src/**/*.test.{js,jsx}"],
    coverage: {
      provider: "v8",
      reporter: ["text", "lcov"],
      allowExternal: true,
      include: ["src/**/*.{js,jsx}", "**/web/{api,shell,overview}.js"],
      exclude: ["src/main.jsx", "src/test-setup.js", "src/**/*.test.{js,jsx}"],
      thresholds: { statements: 80, branches: 80, functions: 80, lines: 80 },
    },
  },
});
