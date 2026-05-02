import { defineConfig, externalizeDepsPlugin } from 'electron-vite'
import react from '@vitejs/plugin-react'
import path from 'path'

export default defineConfig({
  main: {
    plugins: [externalizeDepsPlugin()],
    build: {
      rollupOptions: {
        input: {
          index: path.resolve(__dirname, 'src/main/index.ts')
        }
      }
    }
  },
  preload: {
    plugins: [externalizeDepsPlugin()],
    build: {
      rollupOptions: {
        input: {
          index: path.resolve(__dirname, 'src/preload/index.ts')
        }
      }
    }
  },
  renderer: {
    resolve: {
      alias: {
        '@': path.resolve(__dirname, 'src/renderer/src')
      }
    },
    plugins: [react()],
    root: path.resolve(__dirname, 'src/renderer'),
    // The notification sounds in `gui/src/renderer/public/sounds/` are
    // mirrors of the canonical `assets/sounds/` files at the repo root —
    // duplicated rather than symlinked because Vite's `?url` import doesn't
    // play nicely with files outside the renderer's project root, and the
    // WAVs are tiny (~25 KB each) and never change. The README at
    // `assets/sounds/README.md` documents the source-of-truth.
    publicDir: path.resolve(__dirname, 'src/renderer/public'),
    build: {
      outDir: path.resolve(__dirname, 'out/renderer'),
      rollupOptions: {
        input: {
          index: path.resolve(__dirname, 'src/renderer/index.html')
        }
      }
    }
  }
})
