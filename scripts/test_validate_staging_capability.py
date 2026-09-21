import json
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
SCRIPT = ROOT / "scripts" / "validate-staging-capability.py"


class CapabilityTests(unittest.TestCase):
    def run_check(self, capability, readback):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            cap = root / "capability.json"
            obs = root / "readback.json"
            cap.write_text(json.dumps(capability))
            obs.write_text(json.dumps(readback))
            return subprocess.run([sys.executable, str(SCRIPT), str(cap), str(obs)], text=True, capture_output=True)

    def test_unrelated_distribution_is_classified_without_blocking(self):
        result = self.run_check(
            {"required_routes": [{"route": "/api/public/payment/checkout", "required": True}], "required_provider_dependencies": []},
            {"observations": [{"route": "/api/v1/distribution/products", "status": 503, "error": "distribution_unavailable"}, {"route": "/api/public/payment/checkout", "status": 200, "required": True}]},
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn('"classification": "external_config_unavailable"', result.stdout)

    def test_required_distribution_still_blocks(self):
        result = self.run_check(
            {"required_routes": [], "required_provider_dependencies": ["distribution"]},
            {"observations": [{"route": "/api/v1/distribution/products", "status": 503, "error": "distribution_unavailable", "provider": "distribution"}]},
        )
        self.assertNotEqual(result.returncode, 0)

    def test_required_route_failure_blocks(self):
        result = self.run_check(
            {"required_routes": [], "required_provider_dependencies": []},
            {"observations": [{"route": "/api/public/payment/checkout", "status": 503, "error": "service_unavailable", "required": True}]},
        )
        self.assertNotEqual(result.returncode, 0)


if __name__ == "__main__":
    unittest.main()
