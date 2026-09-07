import hashlib
import json
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import check_source_authority_changes as gate


class SourceAuthorityGateTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.repo = Path(self.tmp.name) / "repo"
        self.repo.mkdir()
        self.g("init", "-q")
        self.g("config", "user.name", "audit-test")
        self.g("config", "user.email", "audit@example.invalid")

    def tearDown(self):
        self.tmp.cleanup()

    def g(self, *args):
        return subprocess.check_output(["git", "-C", str(self.repo), *args], stderr=subprocess.STDOUT).decode().strip()

    @staticmethod
    def blob_sha(data: bytes) -> str:
        return hashlib.sha1(f"blob {len(data)}\0".encode() + data).hexdigest()

    def write(self, relative: str, data: bytes):
        path = self.repo / relative
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_bytes(data)

    def commit(self, label: str) -> str:
        self.g("add", "-A")
        self.g("commit", "-qm", label)
        return self.g("rev-parse", "HEAD")

    def write_snapshot(self, payload: bytes, *, binding_gate: str = "scripts/check-fixture.sh"):
        canonical = "web/donor-sources/frozen/payload.ts"
        source_blob = self.blob_sha(payload)
        digest = hashlib.sha256(payload).hexdigest()
        self.write(canonical, payload)
        library = {
            "id": "frozen",
            "immutable": True,
            "authority_kind": "frozen_donor",
            "source_repository": "https://example.invalid/frozen.git",
            "source_commit": "a" * 40,
            "root": "web/donor-sources/frozen",
        }
        content = {
            "id": "payload",
            "library_id": "frozen",
            "canonical_path": canonical,
            "source_path": "legacy/payload.ts",
            "source_git_blob_sha": source_blob,
            "content_sha256": digest,
            "bytes": len(payload),
            "mode": "100644",
        }
        index = {
            "schema_version": 1,
            "lock_path": gate.LOCK_PATH,
            "libraries": [library],
            "contents": [content],
            "bindings": [{
                "module": "fixture",
                "logical_path": "web/src/payload.ts",
                "content_id": "payload",
                "source_repository": library["source_repository"],
                "source_commit": library["source_commit"],
                "source_path": content["source_path"],
                "source_git_blob_sha": source_blob,
                "mode": "100644",
                "usage": "frozen_donor_compatibility_view",
                "freeze_gate": binding_gate,
                "freeze_ledger": "docs/fixture-ledger.txt",
                "current_path_state": "untracked_post_p4",
            }],
            "views": [{"target_path": "web/src/payload.ts", "content_id": "payload", "enabled": True}],
        }
        lock = {"schema_version": 1, "entries": [gate.canonical_lock_entry(content, library)]}
        self.write(gate.INDEX_PATH, (json.dumps(index, sort_keys=True) + "\n").encode())
        self.write(gate.LOCK_PATH, (json.dumps(lock, sort_keys=True) + "\n").encode())

    def write_approvals(self, approvals):
        self.write(gate.APPROVALS_PATH, (json.dumps({"schema_version": 1, "approvals": approvals}, sort_keys=True) + "\n").encode())

    def approval(self, base: str, changes):
        return {
            "base_commit": base,
            "changes": changes,
            "reason": "reviewed authority source update",
            "review_reference": "PR #999",
        }

    def test_non_authority_change_passes_with_empty_ledger(self):
        self.write_snapshot(b"alpha\n")
        self.write_approvals([])
        base = self.commit("base")
        self.write("docs/ordinary.md", b"ordinary non-authority change\n")
        head = self.commit("ordinary")
        self.assertEqual(gate.check(self.repo, base, head)["authority_changes"], [])

    def test_coordinated_index_lock_and_canonical_change_needs_exact_base_bound_approval(self):
        self.write_snapshot(b"alpha\n")
        self.write_approvals([])
        base = self.commit("base")
        self.write_snapshot(b"beta\n")
        changed = self.commit("coordinated source change")
        with self.assertRaisesRegex(gate.AuthorityGateError, "exactly one approval"):
            gate.check(self.repo, base, changed)

        changes = gate.authority_changes(self.repo, base, changed)
        self.assertEqual([change["path"] for change in changes], ["web/donor-sources/frozen/payload.ts", gate.INDEX_PATH, gate.LOCK_PATH])
        self.write_approvals([self.approval(base, changes)])
        approved = self.commit("approved source change")
        result = gate.check(self.repo, base, approved)
        self.assertEqual(result["approval"]["review_reference"], "PR #999")
        self.assertEqual(result["authority_changes"], changes)

    def test_partial_or_stale_approval_cannot_cover_a_coordinated_change(self):
        self.write_snapshot(b"alpha\n")
        self.write_approvals([])
        base = self.commit("base")
        self.write_snapshot(b"beta\n")
        changed = self.commit("coordinated source change")
        changes = gate.authority_changes(self.repo, base, changed)
        self.write_approvals([self.approval("b" * 40, changes[:-1])])
        head = self.commit("bad approval")
        with self.assertRaisesRegex(gate.AuthorityGateError, "exactly one approval"):
            gate.check(self.repo, base, head)

    def test_index_only_change_requires_review(self):
        self.write_snapshot(b"alpha\n")
        self.write_approvals([])
        base = self.commit("base")
        self.write_snapshot(b"alpha\n", binding_gate="scripts/other-fixture.sh")
        head = self.commit("index only")
        with self.assertRaisesRegex(gate.AuthorityGateError, "exactly one approval"):
            gate.check(self.repo, base, head)

    def test_invalid_lock_cannot_be_approved(self):
        self.write_snapshot(b"alpha\n")
        self.write_approvals([])
        base = self.commit("base")
        self.write_snapshot(b"beta\n")
        lock = json.loads((self.repo / gate.LOCK_PATH).read_text())
        lock["entries"][0]["content_sha256"] = "0" * 64
        self.write(gate.LOCK_PATH, (json.dumps(lock, sort_keys=True) + "\n").encode())
        self.write_approvals([self.approval(base, [{"path": gate.INDEX_PATH, "base_sha256": "0" * 64, "head_sha256": "1" * 64}])])
        head = self.commit("invalid coordinated change")
        with self.assertRaisesRegex(gate.AuthorityGateError, "source lock differs"):
            gate.check(self.repo, base, head)


if __name__ == "__main__":
    unittest.main()
