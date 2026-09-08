#!/usr/bin/env python3
"""Contracts for automatic inclusion and truthful browser execution evidence."""
import json
from pathlib import Path
import tempfile
import unittest
import contextlib
import io
import sys

from dev_preflight import Preflight, REQUIRED_JOURNEYS, SHELL_JOURNEYS, select_journeys, verify_journey_results


class BrowserCoverage(unittest.TestCase):
    def listing(self, extra=()):
        return "\n".join(sorted(REQUIRED_JOURNEYS | set(extra))) + "\nok example/cmd/aicrm 0.05s\n"

    def test_new_journey_joins_without_workflow_edit(self):
        name = "TestPostgreSQLSurveyCompletionChromiumJourney"
        self.assertIn(name, select_journeys(self.listing([name]), "business"))

    def test_groups_are_disjoint_and_exhaustive(self):
        listing = self.listing(["TestPostgreSQLGroupOpsStandardHostChromiumJourney"])
        early = set(select_journeys(listing, "shell"))
        later = set(select_journeys(listing, "business"))
        self.assertEqual(early, SHELL_JOURNEYS)
        self.assertFalse(early & later)
        self.assertEqual(early | later, set(select_journeys(listing, "all")))

    def test_missing_existing_test_fails(self):
        for name in REQUIRED_JOURNEYS:
            with self.subTest(name=name), self.assertRaises(ValueError):
                select_journeys(self.listing().replace(name, ""), "all")

    def test_no_tests_to_run_is_not_success(self):
        with self.assertRaises(ValueError):
            select_journeys("testing: warning: no tests to run\nPASS\n", "all")

    def verify(self, events):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "events.jsonl"
            path.write_text("\n".join(json.dumps(event) for event in events))
            return verify_journey_results(path, ["TestExampleChromiumJourney"])

    def test_actual_pass_is_accepted(self):
        name = "TestExampleChromiumJourney"
        self.assertEqual(self.verify([{"Test": name, "Action": "pass"}, {"Action": "pass"}]), {name: "pass"})

    def test_skipped_journey_fails_even_when_package_passes(self):
        with self.assertRaises(ValueError):
            self.verify([{"Test": "TestExampleChromiumJourney", "Action": "skip"}, {"Action": "pass"}])

    def test_skipped_subtest_fails_even_when_parent_passes(self):
        name = "TestExampleChromiumJourney"
        with self.assertRaises(ValueError):
            self.verify([{"Test": name + "/browser", "Action": "skip"}, {"Test": name, "Action": "pass"}, {"Action": "pass"}])

    def test_missing_failed_or_truncated_results_fail(self):
        name = "TestExampleChromiumJourney"
        for events in [[], [{"Action": "pass"}], [{"Test": name, "Action": "pass"}], [{"Test": name, "Action": "fail"}, {"Action": "fail"}]]:
            with self.subTest(events=events), self.assertRaises(ValueError):
                self.verify(events)

    def test_command_failure_retains_exit_code_and_diagnostic(self):
        with tempfile.TemporaryDirectory() as directory, contextlib.redirect_stdout(io.StringIO()):
            check = Preflight(Path(directory))
            with self.assertRaises(RuntimeError):
                check.run("failure", [sys.executable, "-c", "print('fixture failure'); raise SystemExit(7)"])
            summary = json.loads((Path(directory) / "summary.json").read_text())
            self.assertEqual(summary["steps"][0]["exit_code"], 7)
            self.assertIn("fixture failure", (Path(directory) / "failure.log").read_text())

    def test_command_success_retains_reproduction_command(self):
        with tempfile.TemporaryDirectory() as directory, contextlib.redirect_stdout(io.StringIO()):
            check = Preflight(Path(directory))
            command = [sys.executable, "-c", "print('fixture passed')"]
            check.run("success", command)
            self.assertEqual(check.report["steps"][0]["command"], command)
            self.assertEqual(check.report["steps"][0]["exit_code"], 0)


if __name__ == "__main__":
    unittest.main()
