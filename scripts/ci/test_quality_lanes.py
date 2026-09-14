import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

import quality_lanes


class QualityLaneTests(unittest.TestCase):
    def test_all_ci_lanes_have_one_canonical_command_definition(self):
        for lane in quality_lanes.LANES:
            commands = quality_lanes.commands(lane, Path("/tmp/evidence"))
            self.assertTrue(commands, lane)
            self.assertTrue(all(isinstance(command, list) and command for command in commands), lane)

    def test_preflight_runs_release_acceptance_harness_unit_tests(self):
        commands = quality_lanes.commands("preflight", Path("/tmp/evidence"))
        self.assertIn(
            [sys.executable, "-m", "unittest", "discover", "-s", "scripts/testing", "-p", "test_*.py"],
            commands,
        )

    def test_backend_emits_per_go_test_events_without_changing_required_gates(self):
        commands = quality_lanes.commands("backend", Path("/tmp/evidence"))
        self.assertIn(
            ["bash", "scripts/run-go-with-donor-views.sh", "go", "test", "-json", "-p", "1", "-race", "-count=1", "./..."],
            commands,
        )

    def test_database_lanes_require_reachable_postgresql_16(self):
        with patch.object(quality_lanes, "command_available", return_value=True), patch.object(
                quality_lanes, "exact_version", return_value=True), patch.object(
                quality_lanes, "postgres_16_ready", return_value=False), patch.object(
                quality_lanes, "chromium_font_ready", return_value=True):
            self.assertIn("PostgreSQL 16 reachable through AICRM_DATABASE_URL",
                          quality_lanes.missing_prerequisites("backend"))
            self.assertIn("PostgreSQL 16 reachable through AICRM_DATABASE_URL",
                          quality_lanes.missing_prerequisites("browser"))

    def test_browser_requires_chromium_but_frontend_does_not(self):
        def available(name):
            return name != "google-chrome"
        with patch.object(quality_lanes, "command_available", side_effect=available), patch.object(
                quality_lanes, "exact_version", return_value=True), patch.object(
                quality_lanes, "postgres_16_ready", return_value=True), patch.object(
                quality_lanes, "chromium_font_ready", return_value=True):
            self.assertIn("google-chrome", quality_lanes.missing_prerequisites("browser"))
            self.assertNotIn("google-chrome", quality_lanes.missing_prerequisites("frontend"))

    def test_postgres_check_does_not_print_database_url(self):
        with patch.dict(os.environ, {"AICRM_DATABASE_URL": "postgres://secret@127.0.0.1/aicrm_test_secret"}), patch.object(
                quality_lanes, "command_available", return_value=True), patch("subprocess.run") as run:
            run.return_value.returncode = 1
            self.assertFalse(quality_lanes.postgres_16_ready())
            self.assertNotIn("postgres://secret", run.call_args.args[0])

    def test_postgres_rejects_remote_or_shared_database_without_connecting(self):
        for url in ("postgres://user:pass@10.0.0.2:5432/aicrm_test_isolated", "postgres://user:pass@127.0.0.1:5432/aicrm_shared"):
            with self.subTest(url=url), patch.dict(os.environ, {"AICRM_DATABASE_URL": url}), patch.object(
                    quality_lanes, "command_available", return_value=True), patch("subprocess.run") as run:
                self.assertFalse(quality_lanes.postgres_16_ready())
                run.assert_not_called()

    def test_donors_require_fixed_sha_and_clean_checkout(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            v2, sidebar = root / "v2", root / "sidebar"
            v2.mkdir()
            sidebar.mkdir()
            clean = type("Result", (), {"returncode": 0, "stdout": ""})
            v2_head = type("Result", (), {"returncode": 0, "stdout": quality_lanes.V2_DONOR_SHA + "\n"})
            sidebar_head = type("Result", (), {"returncode": 0, "stdout": quality_lanes.SIDEBAR_DONOR_SHA + "\n"})
            with patch.dict(os.environ, {"PR07_DONOR_DIR": str(v2), "AICRM_SIDEBAR_DONOR_DIR": str(sidebar)}), patch(
                    "subprocess.run", side_effect=[v2_head, clean, sidebar_head, clean]):
                self.assertTrue(quality_lanes.donors_ready())
            wrong = type("Result", (), {"returncode": 0, "stdout": "f" * 40 + "\n"})
            with patch.dict(os.environ, {"PR07_DONOR_DIR": str(v2), "AICRM_SIDEBAR_DONOR_DIR": str(sidebar)}), patch(
                    "subprocess.run", side_effect=[wrong, clean]):
                self.assertFalse(quality_lanes.donors_ready())

    def test_version_match_is_exact_token_not_substring(self):
        with patch.object(quality_lanes, "command_available", return_value=True), patch("subprocess.run") as run:
            run.return_value.returncode = 0
            run.return_value.stdout = "go version go1.26.60 linux/amd64\n"
            self.assertFalse(quality_lanes.exact_version(["go", "version"], "go1.26.6"))

    def test_dispatch_dedup_baseline_falls_back_to_checked_out_head_parent(self):
        head = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=quality_lanes.ROOT, text=True).strip()
        with patch.dict(os.environ, {"AICRM_DEDUP_HEAD_SHA": head, "AICRM_DEDUP_BASE_SHA": ""}, clear=False):
            self.assertTrue(quality_lanes.dedup_base_ready())


if __name__ == "__main__":
    unittest.main()
