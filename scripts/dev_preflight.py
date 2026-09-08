#!/usr/bin/env python3
"""Local/CI checks using existing gates, with exact execution evidence."""
from __future__ import annotations

import argparse
import json
import os
from pathlib import Path
import re
import shlex
import subprocess
import sys
import tempfile
import time

ROOT = Path(__file__).resolve().parents[1]
SHELL_JOURNEYS = {
    "TestPostgreSQLSidebarThumbnailChromiumJourney",
    "TestPostgreSQLAdminShellLayoutChromiumJourney",
}
# Coverage floor, not an allowlist: new compiled journeys join automatically.
REQUIRED_JOURNEYS = SHELL_JOURNEYS | {
    "TestPostgreSQLCustomerTagCommandChromiumJourney",
    "TestPostgreSQLOpenPlatformV1ChromiumJourney",
    "TestPostgreSQLProductExternalPushChromiumJourney",
    "TestPostgreSQLOwnerHandoffChromiumJourney",
    "TestPostgreSQLRuntimeReleaseChromiumJourney",
}


def select_journeys(listing: str, group: str) -> list[str]:
    names = set(re.findall(r"^Test\w*ChromiumJourney$", listing, re.MULTILINE))
    missing = REQUIRED_JOURNEYS - names
    if missing:
        raise ValueError("required Chromium tests missing: " + ", ".join(sorted(missing)))
    selected = names if group == "all" else names & SHELL_JOURNEYS if group == "shell" else names - SHELL_JOURNEYS
    if not selected:
        raise ValueError("empty Chromium selection")
    return sorted(selected)


def verify_journey_results(path: Path, expected: list[str]) -> dict:
    terminal, problems = {}, []
    package_passed = False
    for line in path.read_text().splitlines():
        try:
            event = json.loads(line)
        except json.JSONDecodeError:
            continue  # Compiler diagnostics; subprocess status is also checked.
        name, action = event.get("Test", ""), event.get("Action")
        if not name:
            if action == "pass":
                package_passed = True
            elif action in {"fail", "skip"}:
                problems.append("package: " + action)
        elif name.split("/")[0] in expected and action in {"pass", "fail", "skip"}:
            terminal[name] = action
            if action != "pass":
                problems.append(name + ": " + action)
    for name in expected:
        if terminal.get(name) != "pass":
            problems.append(name + ": no successful terminal event")
    if not package_passed:
        problems.append("no successful package terminal event")
    if problems:
        raise ValueError("Chromium execution incomplete: " + "; ".join(problems))
    return terminal


class Preflight:
    def __init__(self, report_dir: Path):
        self.report_dir = report_dir
        report_dir.mkdir(parents=True, exist_ok=True)
        self.report = {
            "head": subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=ROOT, text=True).strip(),
            "working_tree": subprocess.check_output(["git", "status", "--porcelain", "--untracked-files=normal"], cwd=ROOT, text=True).splitlines(),
            "steps": [], "result": "running",
        }

    def save(self):
        (self.report_dir / "summary.json").write_text(json.dumps(self.report, ensure_ascii=False, indent=2) + "\n")

    def run(self, name: str, command: list[str], env: dict | None = None) -> Path:
        print(f"\n[{name}] {shlex.join(command)}", flush=True)
        log = self.report_dir / (name + ".log")
        start = time.monotonic()
        with log.open("w") as output:
            process = subprocess.Popen(command, cwd=ROOT, env=env, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True)
            assert process.stdout is not None
            with process.stdout:
                for line in process.stdout:
                    output.write(line)
                    print(line, end="", flush=True)
            code = process.wait()
        self.report["steps"].append({"name": name, "command": command, "exit_code": code, "seconds": round(time.monotonic() - start, 2), "log": str(log)})
        self.save()
        if code:
            raise RuntimeError(f"{name} failed ({code}); reproduce: {shlex.join(command)}")
        return log

    def fast(self):
        # No npm install, database, browser or external donor checkout needed.
        self.run("format", ["make", "fmt-check"])
        self.run("boundaries", ["bash", "scripts/check-hxc-identity-boundaries.sh"])
        self.run("whitespace", ["git", "diff", "--check", "HEAD"])
        self.run("source-views", ["make", "prepare-donor-views"])
        self.run("frozen-frontend", ["bash", "scripts/check-pr01-donor-manifest.sh"])
        self.run("preflight-tests", [sys.executable, "scripts/test_dev_preflight.py"])

    def compile(self):
        self.run("compile-all-tests", ["bash", "scripts/run-go-with-donor-views.sh", "go", "test", "-p", "1", "-run", "^$", "./..."])

    def browser(self, group: str):
        if not os.environ.get("AICRM_DATABASE_URL"):
            raise ValueError("AICRM_DATABASE_URL is required; use an isolated PostgreSQL 16 test database")
        env = dict(os.environ, AICRM_REQUIRE_CHROMIUM_JOURNEY="1")
        listing = self.run("browser-discovery", ["bash", "scripts/run-go-with-donor-views.sh", "go", "test", "-p", "1", "-list", "ChromiumJourney$", "./cmd/aicrm"], env)
        names = select_journeys(listing.read_text(), group)
        self.report["required_browser_tests"] = names
        self.save()
        pattern = "^(" + "|".join(names) + ")$"
        log = self.run("browser-execution", ["bash", "scripts/run-go-with-donor-views.sh", "go", "test", "-json", "-p", "1", "-count=1", "-run", pattern, "./cmd/aicrm"], env)
        self.report["browser_results"] = verify_journey_results(log, names)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("phase", choices=["fast", "compile", "browser"])
    parser.add_argument("--group", choices=["all", "shell", "business"], default="all")
    parser.add_argument("--report-dir", type=Path)
    args = parser.parse_args()
    report_dir = args.report_dir or Path(tempfile.mkdtemp(prefix="aicrm-preflight-"))
    check = Preflight(report_dir.resolve())
    check.report["phase"] = args.phase
    print("Evidence: " + str(check.report_dir), flush=True)
    try:
        if args.phase == "browser":
            check.browser(args.group)
        else:
            getattr(check, args.phase)()
        check.report["result"] = "passed"
    except (OSError, RuntimeError, ValueError) as error:
        check.report.update(result="failed", error=str(error))
        print(str(error), file=sys.stderr)
    finally:
        check.save()
    return 0 if check.report["result"] == "passed" else 1


if __name__ == "__main__":
    sys.exit(main())
