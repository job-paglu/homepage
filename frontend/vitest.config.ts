/**
 * Vitest configuration.
 *
 * Only the pure modules under `src/lib` are covered here — `format.ts`, `seo.ts`
 * and (indirectly) `copy.ts`. They are deliberately free of Astro imports and of
 * `document`, which is what lets them run in the plain `node` environment with
 * no jsdom and no Astro plugin: the fastest tests are the ones that need no
 * runtime at all.
 *
 * `.astro` component tests would need `@astrojs/test-utils` or a container API
 * setup and a DOM. They are worth adding, but they are a separate concern from
 * these and should not slow this suite down.
 *
 * The aliases must stay in agreement with `tsconfig.json`. Vitest does not read
 * tsconfig `paths`, so a new alias has to be added in both places.
 */

import { defineConfig } from 'vitest/config';
import { fileURLToPath } from 'node:url';

const src = fileURLToPath(new URL('./src', import.meta.url));

export default defineConfig({
  resolve: {
    alias: {
      '@': src,
      '@components': `${src}/components`,
      '@layouts': `${src}/layouts`,
      '@lib': `${src}/lib`,
      '@styles': `${src}/styles`,
    },
  },
  test: {
    environment: 'node',
    include: ['src/**/__tests__/**/*.test.ts'],
    // `import.meta.env.APP_ENV` is read by urls.ts. Pinning it here means a test
    // never accidentally exercises the development-only `http:` allowance.
    env: {
      APP_ENV: 'production',
      SITE_URL: 'https://jobpaglu.com',
      PUBLIC_API_BASE_URL: 'https://api.jobpaglu.com',
    },
    restoreMocks: true,
  },
});
