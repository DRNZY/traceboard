import { svelte } from '@sveltejs/vite-plugin-svelte'
import { defineConfig } from 'vite'

export default defineConfig({
  plugins: [svelte()],
  build: {
    outDir: '../internal/frontend/dist',
    emptyOutDir: true,
  },
  // Svelte components must resolve their client-side runtime inside Vitest.
  resolve: process.env.VITEST ? { conditions: ['browser'] } : {},
  test: {
    environment: 'node',
    include: ['src/**/*.test.ts'],
    restoreMocks: true,
  },
})
