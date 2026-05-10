import { defineConfig } from 'vite'
import { svelte } from '@sveltejs/vite-plugin-svelte'
import tailwindcss from '@tailwindcss/vite'
import path from 'node:path'

export default defineConfig({
  plugins: [svelte(), tailwindcss()],
  resolve: {
    alias: {
      $lib: path.resolve(__dirname, './src/lib'),
    },
  },
  server: {
    proxy: {
      '/api': {
        target: 'http://localhost:8080',
        changeOrigin: true,
        ws: true,
        configure: (proxy) => {
          const isBenign = (err: NodeJS.ErrnoException) =>
            err.code === 'ECONNRESET' || err.code === 'EPIPE'
          proxy.on('error', (err) => {
            if (isBenign(err)) return
            console.error('[vite proxy]', err)
          })
          proxy.on('proxyReqWs', (_proxyReq, _req, socket) => {
            socket.on('error', (err: NodeJS.ErrnoException) => {
              if (isBenign(err)) return
              console.error('[vite proxy ws]', err)
            })
          })
        },
      },
    },
  },
})
