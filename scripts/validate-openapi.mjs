#!/usr/bin/env node
import SwaggerParser from '@apidevtools/swagger-parser';

try {
  const specification = await SwaggerParser.validate('api/openapi.yaml');
  console.log(`validated OpenAPI ${specification.openapi}: ${Object.keys(specification.paths).length} paths`);
} catch (error) {
  console.error(`OpenAPI validation failed: ${error instanceof Error ? error.message : String(error)}`);
  process.exit(1);
}
