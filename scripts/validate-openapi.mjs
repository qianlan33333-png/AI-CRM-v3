#!/usr/bin/env node
import assert from 'node:assert/strict';
import path from 'node:path';
import { createRequire } from 'node:module';
import SwaggerParser from '@apidevtools/swagger-parser';
import { assertOpenAPIRouteParity } from './check-openapi-route-parity.mjs';

const require = createRequire(import.meta.url);

function assertGroupOpsPlanListItemExamples(specification) {
  // Ajv is Swagger Parser's existing transitive validator. Resolve it from the
  // Parser package so this assertion adds no second schema runtime.
  const parserPath = require.resolve('@apidevtools/swagger-parser');
  const Ajv = require(require.resolve('ajv', { paths: [path.dirname(parserPath)] }));
  const validate = new Ajv({ allErrors: true, strict: false }).compile(specification.components.schemas.GroupOpsPlanListItem);
  const item = {
    plan_id: '41', name: '列表计划', status: 'draft', revision: 7,
    created_by: 7, updated_by: 7,
    created_at: '2026-09-15T09:00:00Z', updated_at: '2026-09-15T09:00:00Z',
    owner: { staff_id: 7, sender_userid: 'fixture-owner', display_name: '列表负责人', name_source: 'wecom_profile', profile_read_state: 'ready' },
    queue_count: 0, bound_group_count: 0,
  };
  assert.equal(validate(item), true, `GroupOpsPlanListItem rejected valid zero counters: ${JSON.stringify(validate.errors)}`);
  assert.equal(validate({ ...item, accidental_field: true }), false, 'GroupOpsPlanListItem must reject an undeclared field');
  assert.equal(validate({ ...item, bound_group_count: -1 }), false, 'GroupOpsPlanListItem must reject a negative bound count');
}

try {
  // Keep the repository's normal OpenAPI structural validation.  The
  // dereferenced copy below is only for compiling the local DTO examples.
  const specification = await SwaggerParser.validate('api/openapi.yaml');
  assertOpenAPIRouteParity(specification);
  const dereferenced = await SwaggerParser.dereference('api/openapi.yaml');
  assertGroupOpsPlanListItemExamples(dereferenced);
  console.log(`validated OpenAPI ${specification.openapi}: ${Object.keys(specification.paths).length} paths`);
} catch (error) {
  console.error(`OpenAPI validation failed: ${error instanceof Error ? error.message : String(error)}`);
  process.exit(1);
}
