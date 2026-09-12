#!/usr/bin/env node
import SwaggerParser from '@apidevtools/swagger-parser';
import { assertOpenAPIRouteParity } from './check-openapi-route-parity.mjs';

try {
  const specification = await SwaggerParser.validate('api/openapi.yaml');
  assertOpenAPIRouteParity(specification);
  console.log(`validated OpenAPI ${specification.openapi}: ${Object.keys(specification.paths).length} paths`);
} catch (error) {
  console.error(`OpenAPI validation failed: ${error instanceof Error ? error.message : String(error)}`);
  process.exit(1);
}
