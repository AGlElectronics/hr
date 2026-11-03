import { defineConfig } from '@rsbuild/core';
import { pluginReact } from '@rsbuild/plugin-react';

const viteEnv = {
  VITE_API_BASE_URL: process.env.VITE_API_BASE_URL ?? '',
  VITE_DEV_USER: process.env.VITE_DEV_USER ?? 'dev-hr-admin',
  VITE_AUTH_MODE: process.env.VITE_AUTH_MODE ?? 'dev',
  VITE_OIDC_AUTHORITY: process.env.VITE_OIDC_AUTHORITY ?? '',
  VITE_OIDC_CLIENT_ID: process.env.VITE_OIDC_CLIENT_ID ?? '',
  VITE_OIDC_SCOPE: process.env.VITE_OIDC_SCOPE ?? 'openid profile email',
} as const;

export default defineConfig({
  plugins: [pluginReact()],
  source: {
    entry: { index: './src/main.tsx' },
    define: {
      'process.env.HR_VERSION': JSON.stringify(process.env.HR_VERSION ?? 'dev'),
      'import.meta.env.VITE_API_BASE_URL': JSON.stringify(viteEnv.VITE_API_BASE_URL),
      'import.meta.env.VITE_DEV_USER': JSON.stringify(viteEnv.VITE_DEV_USER),
      'import.meta.env.VITE_AUTH_MODE': JSON.stringify(viteEnv.VITE_AUTH_MODE),
      'import.meta.env.VITE_OIDC_AUTHORITY': JSON.stringify(viteEnv.VITE_OIDC_AUTHORITY),
      'import.meta.env.VITE_OIDC_CLIENT_ID': JSON.stringify(viteEnv.VITE_OIDC_CLIENT_ID),
      'import.meta.env.VITE_OIDC_SCOPE': JSON.stringify(viteEnv.VITE_OIDC_SCOPE),
    },
  },
  resolve: {
    alias: {
      '@': './src',
    },
  },
  html: {
    template: './public/index.html',
    title: 'HR',
    meta: {
      description: 'HR: volunteer onboarding and access',
      'theme-color': '#18181b',
    },
  },
  server: {
    port: 3003,
    proxy: {
      '/api': {
        target: process.env.API_PROXY_TARGET ?? 'http://localhost:8110',
        changeOrigin: true,
      },
    },
  },
  output: {
    distPath: { root: 'dist' },
    cleanDistPath: true,
  },
});
