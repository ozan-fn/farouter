import zlib from 'node:zlib';
import { defineConfig } from '@rsbuild/core';
import { pluginReact } from '@rsbuild/plugin-react';
import { pluginTailwindcss } from '@rsbuild/plugin-tailwindcss';
import CompressionPlugin from 'compression-webpack-plugin';

export default defineConfig({
  plugins: [
    pluginReact({ reactCompiler: true }),
    pluginTailwindcss(),
  ],
  html: {
    title: 'farouter - AI Router Dashboard',
    meta: {
      description: 'High-performance AI router for Kiro with intelligent account rotation, quota tracking, and built-in monitoring dashboard',
    },
  },
  server: {
    proxy: {
      '/api': { target: 'http://localhost:20180', changeOrigin: true },
    },
  },
  tools: {
    rspack: {
      plugins: [
        new CompressionPlugin({
          algorithm: 'brotliCompress',
          compressionOptions: {
            params: {
              [zlib.constants.BROTLI_PARAM_QUALITY]: 11,
            },
          } as zlib.BrotliOptions,
          threshold: 0,
        }),
      ],
    },
  },
});
