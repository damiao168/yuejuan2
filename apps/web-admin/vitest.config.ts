import { defineConfig, mergeConfig } from "vitest/config";
import viteConfig from "./vite.config";

// Node 门禁由 test:production-contracts 执行，避免 Vitest 把 node:test 用例当作空测试文件。
export default mergeConfig(viteConfig, defineConfig({
  test: { include: ["src/**/*.test.{ts,tsx}"] }
}));
