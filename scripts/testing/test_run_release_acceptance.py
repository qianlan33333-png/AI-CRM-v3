#!/usr/bin/env python3
import importlib.util
import json
import os
import signal
from pathlib import Path
import subprocess
import sys
import tempfile
import time
import unittest
from unittest.mock import patch


MODULE_PATH = Path(__file__).with_name("run_release_acceptance.py")
SPEC = importlib.util.spec_from_file_location("run_release_acceptance", MODULE_PATH)
assert SPEC and SPEC.loader
runner = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(runner)


class ReleaseAcceptanceHarnessTests(unittest.TestCase):
    def test_database_url_rejects_authority_overrides_and_invalid_names(self):
        rejected = (
            "postgresql://aicrm_test@127.0.0.1:5432/aicrm_test_ok?host=remote.invalid",
            "postgresql://aicrm_test@127.0.0.1:5432/aicrm_test_ok?hostaddr=10.0.0.2",
            "postgresql://aicrm_test@127.0.0.1:5432/aicrm_test_ok?service=production",
            "postgresql://aicrm_test@127.0.0.1:5432/aicrm_test_ok?dbname=production",
            "postgresql://aicrm_test@127.0.0.1:5432/aicrm_test_ok/other",
            "postgresql://aicrm_test@127.0.0.1:0/aicrm_test_ok",
            "postgresql://aicrm_test@127.0.0.1:65536/aicrm_test_ok",
            "postgresql://aicrm_test@127.0.0.1:5432/aicrm_test-not-valid",
        )
        for url in rejected:
            with self.subTest(url=url):
                with self.assertRaises(ValueError):
                    runner.validated_database(url)
        _, database = runner.validated_database("postgresql://aicrm_test@127.0.0.1:5432/aicrm_test_ok?sslmode=disable")
        self.assertEqual(database["database"], "aicrm_test_ok")

    def test_isolated_environment_has_no_postgres_or_donor_leaks(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            with patch.dict(os.environ, {"PATH": "/safe/path", "HOME": "/original/home", "PGSERVICE": "production", "PGHOSTADDR": "10.0.0.2", "PGPASSFILE": "/secret", "PGOPTIONS": "-c role=admin", "DATABASE_URL": "postgres://production", "PR08_DONOR_DIR": "/production", "PR09_DONOR_ROOT": "/production", "AICRM_WECOM_ENABLED": "true"}, clear=True):
                env = runner.isolated_env("postgresql://aicrm_test@127.0.0.1:5432/aicrm_test_ok?sslmode=disable", "a" * 40, "b" * 40, root / "run", root / "v2", root / "sidebar")
            for key in ("PGSERVICE", "PGHOSTADDR", "PGOPTIONS", "DATABASE_URL"):
                self.assertNotIn(key, env)
            self.assertEqual(env["PGPASSFILE"], os.devnull)
            self.assertEqual(env["HOME"], "/original/home")
            self.assertEqual(env["NPM_CONFIG_USERCONFIG"], os.devnull)
            self.assertEqual(env["PIP_CONFIG_FILE"], os.devnull)
            self.assertEqual(env["GIT_CONFIG_GLOBAL"], os.devnull)
            self.assertEqual(env["PR08_DONOR_DIR"], str((root / "v2").resolve()))
            self.assertEqual(env["PR09_DONOR_ROOT"], str((root / "v2").resolve()))
            self.assertEqual(env["AICRM_SIDEBAR_DONOR_DIR"], str((root / "sidebar").resolve()))
            self.assertEqual(env["AICRM_ADMIN_LAYOUT_SCREENSHOT_DIR"], str((root / "run" / "artifacts" / "admin-shell-layout").resolve()))
            self.assertEqual(env["AICRM_SIDEBAR_SCREENSHOT_DIR"], str((root / "run" / "artifacts" / "sidebar-standard").resolve()))
            self.assertEqual(env["AICRM_WECOM_ENABLED"], "false")

    def test_integrity_detects_source_head_change_and_dirty_harness(self):
        clean = {"head": "a" * 40, "tree": "b" * 40, "dirty": False}
        changed = {"head": "c" * 40, "tree": "b" * 40, "dirty": False}
        self.assertEqual(runner.integrity_violations(clean, changed, "source"), ["source_head_changed"])
        with self.assertRaisesRegex(RuntimeError, "harness is dirty"):
            runner.require_clean("harness", {"head": "a", "tree": "b", "dirty": True})

    def test_prerequisite_only_and_environment_blocks_are_not_business_success(self):
        self.assertEqual(runner.lane_outcome(False, 0, []), ("ready", 0))
        self.assertEqual(runner.lane_outcome(True, 2, []), ("blocked_environment", 2))

    def test_exception_writes_immutable_failure_receipt(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            harness, source, reports = root / "harness", root / "source", root / "reports"
            for directory in (harness, source):
                directory.mkdir(); (directory / ".git").write_text("gitdir: test\n")
            state = {"head": "a" * 40, "tree": "b" * 40, "dirty": False}
            args = ["preflight", "--test-database-url", "postgresql://aicrm_test@127.0.0.1:5432/aicrm_test_ok?sslmode=disable", "--report-dir", str(reports), "--run-id", "failure01", "--source-root", str(source), "--candidate-sha", "a" * 40]
            with patch.object(runner, "HARNESS_ROOT", harness), patch.object(runner, "git_state", side_effect=[state, state, state, state]), patch.object(runner, "git", return_value="b" * 40), patch.object(runner, "run_command", side_effect=RuntimeError("token=top-secret")):
                self.assertEqual(runner.main(args), 2)
            receipt = json.loads((reports / "failure01" / "environment-receipt.json").read_text())
            self.assertEqual(receipt["status"], "failure")
            self.assertIn("started_at_utc", receipt); self.assertIn("ended_at_utc", receipt)
            self.assertEqual(receipt["run_id"], "failure01")
            self.assertNotIn("top-secret", receipt["error"])
            with self.assertRaisesRegex(RuntimeError, "completed receipt"):
                runner.write_receipt(reports / "failure01", receipt)

    def test_real_short_process_timeout_returns_single_complete_output(self):
        command = [sys.executable, "-c", "import time; print('once', flush=True); time.sleep(5)"]
        code, stdout, stderr, timed_out = runner.run_command(command, cwd=Path.cwd(), env=os.environ.copy(), timeout_seconds=1)
        self.assertEqual(code, 124)
        self.assertTrue(timed_out)
        self.assertEqual(stdout.count("once"), 1)
        self.assertEqual(stderr, "")

    def test_sigterm_reaps_child_and_writes_interrupted_receipt(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            harness, source, reports, pid_file = root / "harness", root / "source", root / "reports", root / "child.pid"
            for directory in (harness, source):
                directory.mkdir()
                subprocess.run(["git", "init", "-q"], cwd=directory, check=True)
                subprocess.run(["git", "config", "user.email", "test@example.invalid"], cwd=directory, check=True)
                subprocess.run(["git", "config", "user.name", "test"], cwd=directory, check=True)
                (directory / "tracked").write_text("x\n")
                subprocess.run(["git", "add", "tracked"], cwd=directory, check=True)
                subprocess.run(["git", "commit", "-qm", "initial"], cwd=directory, check=True)
            lane = source / "scripts" / "ci" / "quality_lanes.py"
            lane.parent.mkdir(parents=True)
            lane.write_text("import os, pathlib, time\npathlib.Path(" + repr(str(pid_file)) + ").write_text(str(os.getpid()))\ntime.sleep(30)\n")
            subprocess.run(["git", "add", "scripts/ci/quality_lanes.py"], cwd=source, check=True)
            subprocess.run(["git", "commit", "-qm", "lane"], cwd=source, check=True)
            code = """import importlib.util, sys
from pathlib import Path
spec = importlib.util.spec_from_file_location('runner', sys.argv[1]); runner = importlib.util.module_from_spec(spec); spec.loader.exec_module(runner)
runner.HARNESS_ROOT = Path(sys.argv[2])
raise SystemExit(runner.main(['preflight', '--execute', '--test-database-url', 'postgresql://aicrm_test@127.0.0.1:5432/aicrm_test_ok?sslmode=disable', '--report-dir', sys.argv[3], '--run-id', 'interrupted01', '--source-root', sys.argv[4], '--candidate-sha', sys.argv[5], '--timeout-seconds', '120']))
"""
            source_sha = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=source, text=True).strip()
            process = subprocess.Popen([sys.executable, "-c", code, str(MODULE_PATH), str(harness), str(reports), str(source), source_sha])
            deadline = time.monotonic() + 10
            while not pid_file.exists() and time.monotonic() < deadline: time.sleep(0.05)
            self.assertTrue(pid_file.exists(), "lane child did not start")
            child_pid = int(pid_file.read_text())
            os.kill(process.pid, signal.SIGTERM)
            self.assertEqual(process.wait(timeout=10), 130)
            with self.assertRaises(ProcessLookupError): os.kill(child_pid, 0)
            receipt = json.loads((reports / "interrupted01" / "environment-receipt.json").read_text())
            self.assertEqual(receipt["status"], "interrupted")
            self.assertEqual(receipt["exit_code"], 130)
            self.assertTrue((reports / "interrupted01" / "stdout.log").exists())
            self.assertTrue((reports / "interrupted01" / "stderr.log").exists())

    def test_exception_still_captures_post_run_git_states(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            harness, source, reports = root / "harness", root / "source", root / "reports"
            for directory in (harness, source):
                directory.mkdir(); (directory / ".git").write_text("gitdir: test\n")
            before = {"head": "a" * 40, "tree": "b" * 40, "dirty": False}
            after = {"head": "a" * 40, "tree": "b" * 40, "dirty": True}
            args = ["preflight", "--test-database-url", "postgresql://aicrm_test@127.0.0.1:5432/aicrm_test_ok?sslmode=disable", "--report-dir", str(reports), "--run-id", "capture01", "--source-root", str(source), "--candidate-sha", "a" * 40]
            with patch.object(runner, "HARNESS_ROOT", harness), patch.object(runner, "git_state", side_effect=[before, before, after, after]), patch.object(runner, "git", return_value="b" * 40), patch.object(runner, "run_command", side_effect=RuntimeError("unexpected failure")):
                self.assertEqual(runner.main(args), 3)
            receipt = json.loads((reports / "capture01" / "environment-receipt.json").read_text())
            self.assertEqual(receipt["status"], "failure")
            self.assertIn("harness_after", receipt)
            self.assertIn("source_after", receipt)
            self.assertIn("harness_dirty_after_run", receipt["integrity_violations"])


if __name__ == "__main__":
    unittest.main()
