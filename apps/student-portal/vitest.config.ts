import { mergeConfig } from "vite";
import { defineConfig } from "vitest/config";
import viteConfig from "./vite.config";

export default defineConfig((environment) => mergeConfig(viteConfig(environment), {
  test: {
    environment: "jsdom",
    setupFiles: ["./src/test/setup.ts"]
  }
}));
