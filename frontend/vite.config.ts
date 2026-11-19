import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

export default defineConfig({
  plugins: [react()],
  build: {
    outDir: '../backend/web',
    emptyOutDir: true,
    chunkSizeWarningLimit: 1500,
    rollupOptions: {
      output: {
        manualChunks: {
          xterm: ['@xterm/xterm', '@xterm/addon-fit', '@xterm/addon-web-links', '@xterm/addon-search'],
          codemirror: ['@uiw/react-codemirror', '@codemirror/lang-json', '@codemirror/lang-javascript', '@codemirror/lang-python', '@codemirror/lang-yaml', '@codemirror/theme-one-dark'],
        },
      },
    },
  },
  server: { proxy: { '/api': { target: 'http://127.0.0.1:8099', ws: true } } },
});
