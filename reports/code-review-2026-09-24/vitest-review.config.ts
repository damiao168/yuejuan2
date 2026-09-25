import { defineConfig } from 'vitest/config';
export default defineConfig({
  esbuild: { jsx: 'automatic' },
  test: { environment: 'jsdom', include: ['reports/code-review-2026-09-24/repro-preparation-race.test.tsx'], maxWorkers: 1 },
});
