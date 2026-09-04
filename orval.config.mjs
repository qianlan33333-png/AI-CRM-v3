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
      target: './web/v3/generated/hxc-dashboard.ts',
      client: 'fetch',
      // The generated directory also contains the separately generated
      // AI Assistant client. Cleaning the whole directory here makes the two
      // generators delete each other's output.
      clean: false,
      prettier: true,
    },
  },
});
