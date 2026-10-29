import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

// Saat pengembangan, Vite menyajikan frontend dan meneruskan /api ke server Go di :8080.
// Saat build, hasilnya ditulis ke dist/ lalu disisipkan ke binary Go (web/embed.go).
export default defineConfig({
  plugins: [react(), tailwindcss()],
  server: {
    port: 5173,
    // Server Go menolak permintaan dari origin lain (CSRF); di dev, origin diganti ke server Go.
    proxy: {
      '/api': {
        target: 'http://localhost:8080',
        changeOrigin: true,
        headers: { origin: 'http://localhost:8080' },
      },
      '/healthz': 'http://localhost:8080',
    },
  },
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    // Font selalu jadi file terpisah: CSP server hanya mengizinkan font-src 'self', bukan data:.
    assetsInlineLimit: (file) => (/\.(woff2?|ttf|otf)$/.test(file) ? false : undefined),
  },
})
