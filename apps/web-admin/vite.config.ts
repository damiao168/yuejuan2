import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

const backendProxy = {
  "/api": "http://127.0.0.1:8080",
  "/ready": "http://127.0.0.1:8080"
};

export default defineConfig({
  plugins: [react()],
  build: {
    manifest: true,
    rollupOptions: {
      output: {
        manualChunks: {
          antd: ["antd"],
          charts: ["recharts"],
          motion: ["framer-motion"]
        }
      }
    }
  },
  server: {
    port: 5173,
    proxy: backendProxy
  },
  preview: {
    port: 4173,
    proxy: backendProxy
  }
});
