import { defineConfig } from 'orval';

export default defineConfig({
  hxcDashboard: {
    input: {
      target: './api/openapi.yaml',
      filters: {
        mode: 'include',
        tags: ['HXCDashboard'],
        schemas: [/^HXC/, /^PositiveID$/, /^ErrorResponse$/, /^UnavailableResponse$/],
      },
    },
    output: {
      target: './web/src/api/generated/hxc-dashboard/hxc-dashboard.ts',
      client: 'fetch',
      clean: true,
      prettier: true,
    },
  },
});
