import assert from 'node:assert/strict';
import test from 'node:test';
import { assertExplicitRoutesDocumented, assertSecurityRequirementGroups } from './check-openapi-route-parity.mjs';

test('rejects a newly registered explicit route absent from OpenAPI', () => {
  assert.throws(
    () => assertExplicitRoutesDocumented({ paths: {} }, [{ method: 'post', route: '/api/admin/new-route', source: 'internal/example/http.go' }]),
    /explicit ServeMux route POST \/api\/admin\/new-route.*missing from OpenAPI/,
  );
});

test('rejects OR security where admin session and CSRF must be one requirement', () => {
  assert.throws(
    () => assertSecurityRequirementGroups({ security: [{ adminSession: [] }, { csrfHeader: [] }] }, [['adminSession', 'csrfHeader']], 'POST /api/admin/example'),
    /security must be/,
  );
});
