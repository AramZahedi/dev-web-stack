import { defineConfig } from 'vitest/config';
import { svelte } from '@sveltejs/vite-plugin-svelte';

export default defineConfig({
  plugins: [svelte()],
  resolve: {
    conditions: ['browser'],
  },
  server: {
    port: 5173,
    proxy: {
      '/api': {
        target: process.env.DEVSTACK_API_ORIGIN ?? 'http://127.0.0.1:7070',
        changeOrigin: false,
      },
    },
  },
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    sourcemap: false,
  },
  test: {
    environment: 'jsdom',
    include: ['src/**/*.{test,spec}.{ts,js}'],
    setupFiles: ['./src/test/setup.ts'],
    restoreMocks: true,
  },
});
