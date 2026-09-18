#!/usr/bin/env python3
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location("release_cleanup", Path(__file__).with_name("cleanup-releases.py"))
cleanup = importlib.util.module_from_spec(spec)
spec.loader.exec_module(cleanup)


class CleanupTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name).resolve()
        (self.root / "releases").mkdir()
        self.names = [f"{n:040x}" for n in range(1, 5)]
        self.migration = b"CREATE TABLE example(id bigint);\n"
        for i, name in enumerate(self.names):
            self.package(name, i)
        (self.root / "current").symlink_to(self.root / "releases" / self.names[-1])
        self.schema = self.root / "schema.json"
        self.schema.write_text(json.dumps({"current_sha": self.names[-1], "captured_at": cleanup.utcnow().isoformat(),
            "migrations": [{"version": "0001", "name": "0001_example.sql", "checksum": hashlib.sha256(self.migration).hexdigest()}]}))
        self.ready = patch.object(cleanup, "check_ready")
        self.refs = patch.object(cleanup, "live_references", return_value=set())
        self.ready.start()
        self.refs.start()
        self.addCleanup(self.ready.stop)
        self.addCleanup(self.refs.stop)

    def package(self, name, sequence):
        path = self.root / "releases" / name
        (path / "bin").mkdir(parents=True)
        (path / "migrations").mkdir()
        (path / "bin/aicrm").write_bytes(b"verified release executable")
        (path / "migrations/0001_example.sql").write_bytes(self.migration)
        files = ["bin/aicrm", "migrations/0001_example.sql"]
        (path / "release-files.sha256").write_text("".join(cleanup.digest_file(path / f) + "  ./" + f + "\n" for f in files))
        (path / "release.env").write_text(f"AICRM_RELEASE_SHA={name}\n")
        for item in path.rglob("*"):
            os.utime(item, (1000000 + sequence, 1000000 + sequence))
        return path

    def plan(self):
        return cleanup.inventory(self.root, self.schema, [])

    def test_only_verified_unreferenced_releases_delete_and_replay_is_noop(self):
        plan = self.plan()
        self.assertEqual(plan["candidate_count"], 1)
        self.assertEqual(set(plan["rollback_releases"]), set(self.names[1:3]))
        before = {n: cleanup.verified_package(self.root / "releases" / n) for n in self.names[1:]}
        result = cleanup.apply_plan(self.root, self.schema, plan, [])
        self.assertEqual([v["name"] for v in result["deleted"]], self.names[:1])
        self.assertFalse((self.root / "releases" / self.names[0]).exists())
        for name, value in before.items():
            self.assertEqual(cleanup.verified_package(self.root / "releases" / name), value)
        replay = cleanup.apply_plan(self.root, self.schema, plan, [])
        self.assertEqual(replay["deleted"], [])
        self.assertEqual(replay["already_absent"], self.names[:1])
        audit = [json.loads(line) for line in (self.root / "release-cleanup-audit.jsonl").read_text().splitlines()]
        self.assertEqual([item["event"] for item in audit], ["delete_started", "delete_completed"])
        self.assertEqual({item["name"] for item in audit}, {self.names[0]})

    def test_interrupted_cleanup_preserves_completed_receipt_and_reports_partial(self):
        other = f"{5:040x}"
        self.package(other, -1)
        plan = self.plan()
        self.assertEqual(plan["candidate_count"], 2)
        original = cleanup.shutil.rmtree
        def remove(path):
            if path.name == other:
                raise OSError("simulated write failure")
            original(path)
        remove.avoids_symlink_attacks = True
        with patch.object(cleanup.shutil, "rmtree", side_effect=remove) as deletion:
            deletion.avoids_symlink_attacks = True
            with self.assertRaises(cleanup.Refuse) as failure:
                cleanup.apply_plan(self.root, self.schema, plan, [])
        self.assertEqual([item["name"] for item in failure.exception.deleted], self.names[:1])
        audit = [json.loads(line) for line in (self.root / "release-cleanup-audit.jsonl").read_text().splitlines()]
        self.assertEqual([item["event"] for item in audit], ["delete_started", "delete_completed", "delete_started"])
        self.assertTrue((self.root / "releases" / other).exists())

    def test_new_reference_or_file_change_refuses_before_any_delete(self):
        plan = self.plan()
        with patch.object(cleanup, "live_references", return_value={self.names[0]}):
            with self.assertRaisesRegex(cleanup.Refuse, "protected"):
                cleanup.apply_plan(self.root, self.schema, plan, [])
        (self.root / "releases" / self.names[0] / "bin/aicrm").write_bytes(b"changed")
        with self.assertRaises(cleanup.Refuse):
            cleanup.apply_plan(self.root, self.schema, plan, [])
        self.assertTrue(all((self.root / "releases" / n).exists() for n in self.names))

    def test_unknown_backup_secret_and_symlink_are_protected(self):
        bad = self.root / "releases" / self.names[0]
        (bad / "customer-export.csv").write_bytes(b"business data")
        (self.root / "releases" / "manual-backup").mkdir()
        plan = self.plan()
        self.assertEqual(plan["candidate_count"], 0)
        self.assertTrue(all(item["action"] == "protect" for item in plan["entries"]))
        (bad / "customer-export.csv").unlink()
        (bad / "bin/secret.pem").write_bytes(b"never inspect this content")
        with self.assertRaises(cleanup.Refuse):
            cleanup.verified_package(bad)
        (bad / "bin/secret.pem").unlink()
        (bad / "bin/external").symlink_to(self.root / "schema.json")
        with self.assertRaises(cleanup.Refuse):
            cleanup.verified_package(bad)

    def test_missing_two_compatible_rollbacks_blocks_all_cleanup(self):
        (self.root / "releases" / self.names[1] / "bin/aicrm").write_bytes(b"corrupt")
        (self.root / "releases" / self.names[0] / "migrations/0001_example.sql").write_bytes(b"old schema")
        plan = self.plan()
        self.assertTrue(plan["blockers"])
        self.assertEqual(plan["candidate_count"], 0)
        with self.assertRaises(cleanup.Refuse):
            cleanup.apply_plan(self.root, self.schema, plan, [])

    def test_schema_evidence_and_current_must_match(self):
        value = json.loads(self.schema.read_text())
        value["migrations"][0]["checksum"] = "0" * 64
        self.schema.write_text(json.dumps(value))
        with self.assertRaisesRegex(cleanup.Refuse, "schema"):
            self.plan()

    def test_installer_lock_is_shared_and_nonblocking(self):
        with cleanup.release_lock(self.root):
            with self.assertRaisesRegex(cleanup.Refuse, "in_progress"):
                with cleanup.release_lock(self.root):
                    pass

    def test_each_proc_reference_kind_protects_release(self):
        self.refs.stop()
        proc = self.root / "proc"
        proc.mkdir()
        for kind in ("exe", "cwd", "maps", "fd", "cmdline"):
            with self.subTest(kind=kind), tempfile.TemporaryDirectory(dir=proc) as ignored:
                pid = proc / "1001"
                pid.mkdir(exist_ok=True)
                (pid / "fd").mkdir(exist_ok=True)
                target = str(self.root / "releases" / self.names[0] / "bin/aicrm")
                path = pid / ("fd/1" if kind == "fd" else kind)
                if kind in ("maps", "cmdline"):
                    path.write_text(target)
                else:
                    path.symlink_to(target)
                self.assertEqual(cleanup.process_references(self.root, set(self.names), proc), {self.names[0]})
                path.unlink()

    def test_disabled_service_external_symlink_and_transient_service_are_protected(self):
        alias = self.root / "external-service-binary"
        alias.symlink_to(self.root / "releases" / self.names[0] / "bin/aicrm")
        def systemctl(args):
            if args[0] == "list-unit-files":
                return "disabled.service disabled\n"
            if args[0] == "list-units":
                return "transient.service loaded inactive dead\n"
            self.assertIn("transient.service", args)
            return f"ExecStart={{ path={alias} ; argv[]={alias} ; }}\n"
        with patch.object(cleanup, "run_systemctl", side_effect=systemctl):
            self.assertEqual(cleanup.service_references(self.root, set(self.names)), {self.names[0]})

    def test_uninstantiated_template_and_dropin_are_read_not_shown_or_skipped(self):
        alias = self.root / "disabled-template-alias"
        alias.symlink_to(self.root / "releases" / self.names[2] / "bin/aicrm")
        calls = []
        def systemctl(args):
            calls.append(args)
            if args[0] == "list-unit-files":
                return "disabled@.service disabled\nconcrete.service disabled\n"
            if args[0] == "list-units":
                return "transient@one.service loaded inactive dead\n"
            if args[0] == "cat":
                self.assertEqual(args, ["cat", "disabled@.service", "--no-pager"])
                return (f"# /etc/systemd/system/disabled@.service\n[Service]\nExecStart={self.root}/releases/{self.names[0]}/bin/aicrm\n"
                        f"# /etc/systemd/system/disabled@.service.d/override.conf\nExecStartPost={self.root}/releases/{self.names[1]}/bin/aicrm\n"
                        f"EnvironmentFile={alias}\n")
            self.assertEqual(args[0], "show")
            self.assertNotIn("disabled@.service", args)
            self.assertIn("concrete.service", args)
            self.assertIn("transient@one.service", args)
            return "ExecStart=/usr/bin/true\n"
        with patch.object(cleanup, "run_systemctl", side_effect=systemctl):
            self.assertEqual(cleanup.service_references(self.root, set(self.names)), set(self.names[:3]))
        self.assertTrue(any(args[0] == "cat" for args in calls))

    def test_unreadable_template_or_dropin_refuses_the_entire_inventory(self):
        def systemctl(args):
            if args[0] == "list-unit-files":
                return "disabled@.service disabled\n"
            if args[0] == "list-units":
                return ""
            self.assertEqual(args[0], "cat")
            raise cleanup.Refuse("service_reference_inventory_failed")
        with patch.object(cleanup, "run_systemctl", side_effect=systemctl):
            with self.assertRaisesRegex(cleanup.Refuse, "service_reference_inventory_failed"):
                cleanup.service_references(self.root, set(self.names))

    def test_dynamic_release_template_protects_all_possible_packages(self):
        def systemctl(args):
            if args[0] == "list-unit-files":
                return "release@.service disabled\n"
            if args[0] == "list-units":
                return ""
            return f"[Service]\nExecStart={self.root}/releases/%i/bin/aicrm\n"
        with patch.object(cleanup, "run_systemctl", side_effect=systemctl):
            self.assertEqual(cleanup.service_references(self.root, set(self.names)), set(self.names))


if __name__ == "__main__":
    unittest.main()
