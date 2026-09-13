import unittest
from unittest.mock import patch
import os
from pathlib import Path
import tempfile

import quality_report


REPO = "owner/repo"
PR = 7
OLD = "a" * 40
FRESH = "b" * 40


def run(run_id, head, when, *, pr=PR, attempt=1, repository=REPO, event="pull_request"):
    return {"id": run_id, "head_sha": head, "path": quality_report.WORKFLOW, "event": event,
            "repository": {"full_name": repository}, "pull_requests": [{"number": pr}],
            "created_at": when, "run_attempt": attempt}


def pages(*runs):
    return [{"total_count": len(runs), "workflow_runs": list(runs)}]


class QualityReportTests(unittest.TestCase):
    def test_receipt_preserves_multiple_observations(self):
        data = quality_report.receipt("backend", ["checkout=success", "setup=success"], "failure", "cancelled")
        self.assertEqual(data["observations"], ["assertion_or_verification_failure", "cancelled"])

    def test_setup_failure_is_not_relabelled_as_assertion_failure(self):
        data = quality_report.receipt("frontend", ["checkout=success", "setup=failure"], "skipped", "failure")
        self.assertEqual(data["observations"], ["environment_setup_failure", "pending_or_incomplete"])

    def test_completed_failure_without_receipt_is_unknown(self):
        needs = {lane: {"result": "success", "outputs": {}} for lane in quality_report.LANES}
        needs["browser"] = {"result": "failure", "outputs": {}}
        self.assertEqual(quality_report.lane_summary(needs)["browser"]["observations"], ["unknown_failure"])

    def test_lane_summary_preserves_full_receipt_observations_when_available(self):
        needs = {lane: {"result": "success", "outputs": {}} for lane in quality_report.LANES}
        needs["backend"] = {"result": "failure", "outputs": {"classification": "environment_setup_failure", "observations": '["environment_setup_failure","assertion_or_verification_failure","cancelled"]'}}
        self.assertEqual(quality_report.lane_summary(needs)["backend"]["observations"], ["environment_setup_failure", "assertion_or_verification_failure", "cancelled"])

    def test_receipt_exports_primary_and_full_compact_observations(self):
        with tempfile.TemporaryDirectory() as directory:
            output = Path(directory) / "github-output"
            with patch.dict(os.environ, {"GITHUB_OUTPUT": str(output)}, clear=False):
                args = type("Args", (), {"lane": "backend", "setup": ["setup=failure"], "verification": "failure", "job": "failure", "out": Path(directory) / "receipt.json"})
                quality_report.emit_receipt(args)
            self.assertEqual(output.read_text().splitlines(), [
                "classification=assertion_or_verification_failure",
                'observations=["environment_setup_failure","assertion_or_verification_failure"]',
            ])

    def test_current_self_run_stays_in_progress_when_check_is_success(self):
        data = quality_report.current_attempt(REPO, 1, 2, OLD, {"check": {"result": "success"}})
        self.assertEqual(data["verification"], "success")
        self.assertEqual(data["workflow_lifecycle"], "in_progress")
        self.assertEqual(data["workflow_conclusion"], "not_available")

    def test_first_pr_attempt_uses_old_force_pushed_head_and_attempt_one(self):
        old_run = run(4, OLD, "2026-01-01T00:00:00Z", attempt=3)
        with patch.object(quality_report, "gh_pages", return_value=pages(old_run, run(5, FRESH, "2026-01-02T00:00:00Z"))), patch.object(
                quality_report, "attempt_record", return_value={"head_sha": OLD, "verification": "cancelled"}) as attempt:
            first = quality_report.first_pr_attempt(REPO, PR, {"created_at": "2026-01-01T00:00:00Z"})
        self.assertEqual(first["head_sha"], OLD)
        attempt.assert_called_once_with(REPO, 4, 1, pr=PR, head=OLD)

    def test_same_sha_different_pr_is_not_history_evidence(self):
        foreign = run(1, FRESH, "2026-01-01T00:00:00Z", pr=8)
        own = run(2, FRESH, "2026-01-02T00:00:00Z")
        with patch.object(quality_report, "gh_pages", return_value=pages(foreign, own)):
            found = quality_report.pr_runs(REPO, PR, "2026-01-01T00:00:00Z", FRESH)
        self.assertEqual(found, [own])

    def test_wrong_repository_or_event_is_not_pr_history_evidence(self):
        wrong_repo = run(1, OLD, "2026-01-01T00:00:00Z", repository="other/repo")
        wrong_event = run(2, OLD, "2026-01-01T00:00:01Z", event="push")
        with patch.object(quality_report, "gh_pages", return_value=pages(wrong_repo, wrong_event)):
            self.assertEqual(quality_report.pr_runs(REPO, PR, "2026-01-01T00:00:00Z"), [])

    def test_empty_actions_pr_list_requires_unique_commit_to_pr_association(self):
        candidate = run(1, OLD, "2026-01-01T00:00:00Z")
        candidate["pull_requests"] = []
        with patch.object(quality_report, "gh_pages", return_value=[[{"number": PR}, {"number": 8}]]):
            with self.assertRaisesRegex(ValueError, "ambiguously associated"):
                quality_report.is_pr_run(candidate, PR, REPO)

    def test_incomplete_history_pagination_makes_first_unknown(self):
        incomplete = [{"total_count": 2, "workflow_runs": [run(1, OLD, "2026-01-01T00:00:00Z")]}]
        with patch.object(quality_report, "gh_pages", return_value=incomplete):
            with self.assertRaisesRegex(ValueError, "pagination is incomplete"):
                quality_report.first_pr_attempt(REPO, PR, {"created_at": "2026-01-01T00:00:00Z"})

    def test_history_at_github_truncation_boundary_is_rejected(self):
        boundary = [{"total_count": 1000, "workflow_runs": [run(index, OLD, f"2026-01-01T00:00:{index:02d}Z") for index in range(1000)]}]
        with self.assertRaisesRegex(ValueError, "truncation boundary"):
            quality_report._history_page_runs(boundary)

    def test_final_uses_current_head_latest_run_not_old_success(self):
        pull = {"created_at": "2026-01-01T00:00:00Z", "head": {"sha": FRESH}}
        old_success = run(2, OLD, "2026-01-03T00:00:00Z", attempt=4)
        fresh_pending = run(9, FRESH, "2026-01-04T00:00:00Z", attempt=2)
        with patch.object(quality_report, "gh_pages", return_value=pages(old_success, fresh_pending)), patch.object(
                quality_report, "attempt_record", return_value={"head_sha": FRESH, "verification": "pending_or_incomplete"}) as attempt:
            head, final = quality_report.final_pr_attempt(REPO, PR, pull)
        self.assertEqual(head, FRESH)
        self.assertEqual(final["verification"], "pending_or_incomplete")
        attempt.assert_called_once_with(REPO, 9, 2, pr=PR, head=FRESH)

    def test_final_uses_highest_visible_rerun_of_latest_run(self):
        pull = {"created_at": "2026-01-01T00:00:00Z", "head": {"sha": FRESH}}
        attempts = [run(9, FRESH, "2026-01-04T00:00:00Z", attempt=1), run(9, FRESH, "2026-01-04T00:00:00Z", attempt=3)]
        with patch.object(quality_report, "gh_pages", return_value=pages(*attempts)), patch.object(
                quality_report, "attempt_record", return_value={"verification": "success"}) as record:
            quality_report.final_pr_attempt(REPO, PR, pull)
        record.assert_called_once_with(REPO, 9, 3, pr=PR, head=FRESH)

    def test_attempt_record_keeps_completed_lifecycle_and_conclusion_separate(self):
        with patch.object(quality_report, "gh_api", side_effect=[
                {**run(4, OLD, "2026-01-01T00:00:00Z"), "status": "completed", "conclusion": "timed_out"},
                {"total_count": 1, "jobs": [{"name": "check", "status": "completed", "conclusion": "timed_out"}]},
        ]):
            record = quality_report.attempt_record(REPO, 4, 1, pr=PR, head=OLD)
        self.assertEqual(record["workflow_lifecycle"], "completed")
        self.assertEqual(record["workflow_conclusion"], "timed_out")
        self.assertEqual(record["verification"], "failure")

    def test_cancelled_run_with_failed_check_but_no_failed_lane_is_cancelled(self):
        jobs = [{"name": "backend", "status": "completed", "conclusion": "cancelled"},
                {"name": "check", "status": "completed", "conclusion": "failure"}]
        self.assertEqual(quality_report.attempt_verification("completed", "cancelled", jobs), "cancelled")

    def test_cancelled_run_keeps_actual_lane_failure(self):
        jobs = [{"name": "backend", "status": "completed", "conclusion": "failure"},
                {"name": "check", "status": "completed", "conclusion": "failure"}]
        self.assertEqual(quality_report.attempt_verification("completed", "cancelled", jobs), "failure")

    def test_batch_rejects_duplicate_pr_snapshots(self):
        item = {"repository": REPO, "pull_request": PR, "first_attempt": {"verification": "success"}, "final_attempt": {"verification": "success"}}
        with self.assertRaisesRegex(ValueError, "duplicate PR"):
            quality_report.batch_counts([item, item])

    def test_batch_terminal_denominator_excludes_cancelled_pending_and_unknown(self):
        items = [
            {"repository": REPO, "pull_request": 1, "first_attempt": {"verification": "success"}, "final_attempt": {"verification": "failure"}},
            {"repository": REPO, "pull_request": 2, "first_attempt": {"verification": "cancelled"}, "final_attempt": {"verification": "pending_or_incomplete"}},
            {"repository": REPO, "pull_request": 3, "first_attempt": {"verification": "unknown"}, "final_attempt": {"verification": "success"}},
        ]
        counts = quality_report.batch_counts(items)
        self.assertEqual(counts["prs_observed"], 3)
        self.assertEqual(counts["first_terminal_observed"], 1)
        self.assertEqual(counts["final_terminal_observed"], 2)
        self.assertEqual(counts["first_cancelled"], 1)
        self.assertEqual(counts["final_pending_or_incomplete"], 1)


if __name__ == "__main__":
    unittest.main()
