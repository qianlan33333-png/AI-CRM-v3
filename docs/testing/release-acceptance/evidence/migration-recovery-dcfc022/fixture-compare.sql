SELECT id || ':' || payload AS fixture_row
FROM release_acceptance_fixture
ORDER BY id;
