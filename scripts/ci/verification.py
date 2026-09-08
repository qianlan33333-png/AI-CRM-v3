#!/usr/bin/env python3
"""Reuse successful PR verification only for an identical, API-verified Git tree.

No code or release artifact from a PR is executed by the main proof lookup.
Missing, expired, invalid or unavailable evidence falls back to full verification.
"""
import argparse
import io
import json
import os
from pathlib import Path
import re
import subprocess
import zipfile

PHASES = ("preflight", "backend", "frontend", "browser", "archive-sdk")
WORKFLOW = ".github/workflows/ci.yml"


def git(ref):
    return subprocess.check_output(["git", "rev-parse", ref], text=True).strip()


def api(path, raw=False):
    result = subprocess.run(["gh", "api", path], check=True, capture_output=True, timeout=30)
    return result.stdout if raw else json.loads(result.stdout)


def eligible_run(run, repo, head):
    return (run.get("event") == "pull_request" and run.get("status") == "completed"
            and run.get("conclusion") == "success" and run.get("head_sha") == head
            and run.get("path") == WORKFLOW
            and (run.get("head_repository") or {}).get("full_name") == repo)


def valid_proof(proof, run, pr, commit, tree, repo):
    return (proof.get("schema") == 1 and proof.get("mode") == "full"
            and proof.get("repository") == repo
            and proof.get("event") == "pull_request"
            and proof.get("run_id") == run["id"]
            and proof.get("run_attempt") == run["run_attempt"]
            and proof.get("pr") == pr["number"]
            and proof.get("head") == pr["head"]["sha"]
            and proof.get("tested_sha") == commit.get("sha")
            and proof.get("tree") == tree == commit.get("commit", {}).get("tree", {}).get("sha")
            and len(commit.get("parents", [])) == 2
            and commit["parents"][1]["sha"] == pr["head"]["sha"]
            and proof.get("phases") == {name: "success" for name in PHASES})


def read_proof(data):
    with zipfile.ZipFile(io.BytesIO(data)) as archive:
        entries = archive.infolist()
        if len(entries) != 1 or entries[0].filename != "verification.json" or entries[0].file_size > 16384:
            raise ValueError("unexpected verification artifact")
        return json.loads(archive.read(entries[0]))


def find_verified_run(repo, sha, tree):
    prefix = f"repos/{repo}"
    for pr in api(f"{prefix}/commits/{sha}/pulls?per_page=100"):
        if (not pr.get("merged_at") or pr.get("merge_commit_sha") != sha
                or pr.get("base", {}).get("ref") != "main"
                or pr.get("base", {}).get("repo", {}).get("full_name") != repo):
            continue
        head = pr["head"]["sha"]
        runs = api(f"{prefix}/actions/workflows/ci.yml/runs?event=pull_request&head_sha={head}&status=success&per_page=20")
        for run in runs["workflow_runs"]:
            if not eligible_run(run, repo, head):
                continue
            expected_name = f"ci-verification-{run['id']}-{run['run_attempt']}"
            artifacts = api(f"{prefix}/actions/runs/{run['id']}/artifacts?per_page=100")
            for artifact in artifacts["artifacts"]:
                if artifact["name"] != expected_name or artifact["expired"] or artifact["size_in_bytes"] > 65536:
                    continue
                proof = read_proof(api(f"{prefix}/actions/artifacts/{artifact['id']}/zip", raw=True))
                tested_sha = proof.get("tested_sha", "")
                if not re.fullmatch(r"[0-9a-f]{40}", tested_sha):
                    continue
                commit = api(f"{prefix}/commits/{tested_sha}")
                if not valid_proof(proof, run, pr, commit, tree, repo):
                    continue
                jobs = api(f"{prefix}/actions/runs/{run['id']}/attempts/{run['run_attempt']}/jobs?per_page=100")
                results = {job["name"]: job["conclusion"] for job in jobs["jobs"]}
                if all(results.get(name) == "success" for name in (*PHASES, "check")):
                    return run["id"]
    return None


def require_results(needs, full, event, ref):
    if needs.get("plan", {}).get("result") != "success":
        raise ValueError("verification plan did not succeed")
    if not full and (event not in {"push", "workflow_dispatch"} or ref != "refs/heads/main"
                     or not needs["plan"].get("outputs", {}).get("verified_run")):
        raise ValueError("only main with a verified PR tree may reuse checks")
    expected = "success" if full else "skipped"
    for name in PHASES:
        if needs.get(name, {}).get("result") != expected:
            raise ValueError(f"{name}: expected {expected}")


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("mode", choices=["plan", "gate"])
    args = parser.parse_args()
    event, ref = os.environ["GITHUB_EVENT_NAME"], os.environ["GITHUB_REF"]
    repo, sha = os.environ["GITHUB_REPOSITORY"], os.environ["GITHUB_SHA"]
    if args.mode == "plan":
        verified_run = None
        if (event in {"push", "workflow_dispatch"} and ref == "refs/heads/main"
                and os.environ.get("FORCE_FULL") != "true"):
            try:
                verified_run = find_verified_run(repo, sha, git("HEAD^{tree}"))
            except (subprocess.SubprocessError, ValueError, KeyError, TypeError, zipfile.BadZipFile):
                # Do not disclose raw API responses; uncertainty means run tests.
                print("PR verification unavailable or invalid; running full checks")
        full = verified_run is None
        with open(os.environ["GITHUB_OUTPUT"], "a") as output:
            output.write(f"full={str(full).lower()}\nverified_run={verified_run or ''}\n")
        message = ("Full PR verification required" if full else
                   f"Reusing PR run {verified_run}: complete Git tree equals {sha}")
        print(message)
        with open(os.environ["GITHUB_STEP_SUMMARY"], "a") as summary:
            summary.write(message + "\n")
    else:
        needs = json.loads(os.environ["CI_NEEDS"])
        full_text = needs.get("plan", {}).get("outputs", {}).get("full")
        if full_text not in {"true", "false"}:
            raise ValueError("missing verification mode")
        full = full_text == "true"
        require_results(needs, full, event, ref)
        if full:
            payload = json.loads(Path(os.environ["GITHUB_EVENT_PATH"]).read_text())
            proof = {
                "schema": 1, "mode": "full", "event": event, "repository": repo,
                "run_id": int(os.environ["GITHUB_RUN_ID"]),
                "run_attempt": int(os.environ["GITHUB_RUN_ATTEMPT"]),
                "pr": payload.get("number"),
                "head": payload.get("pull_request", {}).get("head", {}).get("sha"),
                "tested_sha": git("HEAD"), "tree": git("HEAD^{tree}"),
                "phases": {name: needs[name]["result"] for name in PHASES},
            }
            Path("verification.json").write_text(json.dumps(proof, indent=2) + "\n")
        print("All required verification passed" if full else "Identical merged tree: PR verification reused")


if __name__ == "__main__":
    main()
