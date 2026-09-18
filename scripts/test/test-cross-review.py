#!/usr/bin/env python3
"""Exercise immutable review inputs, rejected results, and read-only CLI settings."""
import importlib.util
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest import mock

sys.dont_write_bytecode = True
ROOT = Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location("cross_review", ROOT / "scripts/review/cross-review.py")
review = importlib.util.module_from_spec(spec)
spec.loader.exec_module(review)


class CrossReviewTests(unittest.TestCase):
    def test_snapshot_uses_commit_not_working_tree_and_omits_symlinks(self):
        with tempfile.TemporaryDirectory() as tmp:
            repo = Path(tmp) / "repo"
            repo.mkdir()
            def git(*args):
                return subprocess.check_output(["git", "-C", str(repo), *args], stderr=subprocess.DEVNULL).decode().strip()
            git("init")
            git("config", "user.email", "test@example.invalid")
            git("config", "user.name", "Test")
            file = repo / "code.txt"
            file.write_text("before\n")
            git("add", ".")
            git("commit", "-m", "base")
            base = git("rev-parse", "HEAD")
            file.write_text("committed\n")
            (repo / "escape").symlink_to("/etc/passwd")
            git("add", ".")
            git("commit", "-m", "change")
            file.write_text("uncommitted\n")
            (repo / ".env").write_text("PRIVATE_VALUE=not-for-review\n")
            snapshot = Path(tmp) / "snapshot"
            snapshot.mkdir()
            metadata, patch = review.prepare(repo, base, "HEAD", snapshot)
            self.assertEqual((snapshot / "code.txt").read_text(), "committed\n")
            self.assertFalse((snapshot / "escape").exists())
            self.assertFalse((snapshot / ".env").exists())
            self.assertEqual(metadata["head"], git("rev-parse", "HEAD"))
            self.assertIn(b"+committed", patch)
            with self.assertRaises(ValueError):
                review.prepare(repo, "HEAD", "HEAD", snapshot)

    def test_read_only_cli_policies(self):
        claude = review.command("claude", "/bin/claude", Path("/tmp/snapshot"), Path("/tmp/report"))
        self.assertIn("--restricted", claude)
        self.assertEqual(claude[claude.index("--tools") + 1], "Read,Glob,Grep")
        self.assertIn("--strict-mcp-config", claude)
        self.assertIn('{"disableAllHooks":true}', claude)
        codex = review.command("codex", "/bin/codex", Path("/tmp/snapshot"), Path("/tmp/report"))
        self.assertEqual(codex[codex.index("--sandbox") + 1], "read-only")
        self.assertIn('approval_policy="never"', codex)
        self.assertIn("--ignore-user-config", codex)
        self.assertNotIn("--dangerously-bypass-approvals-and-sandbox", codex)

    def test_failed_or_empty_claude_result_is_not_a_pass(self):
        for result in ({"is_error": True, "subtype": "success", "result": "oops"},
                       {"subtype": "error_max_turns", "result": "partial"},
                       {"subtype": "success", "result": ""}):
            with self.subTest(result=result), self.assertRaises(RuntimeError):
                review.claude_report(json.dumps(result))
        self.assertEqual(review.claude_report(json.dumps({"subtype": "success", "result": "No findings."})), "No findings.")

    def test_publication_checks_scope_and_posts_body_file(self):
        metadata = {"status": "completed_advisory_review", "reviewer": "claude",
                    "head": "h", "base": "b", "merge_base": "m"}
        state = {"state": "open", "head": "h", "base": "b", "url": "https://example.invalid/pr/1"}
        with tempfile.TemporaryDirectory() as tmp:
            output = Path(tmp)
            (output / "review.md").write_text("Finding at source.py:4. @person")
            with mock.patch.object(review, "pr_state", return_value=state), mock.patch.object(review, "gh", return_value="") as gh:
                review.publish(output, 1, output, metadata)
                self.assertEqual(metadata["publication_status"], "posted")
                self.assertIn("--body-file", gh.call_args.args)
                body = (output / "comment.md").read_text()
                self.assertIn("Reviewed commit: `h`", body)
                self.assertIn("Finding at source.py:4", body)
                self.assertNotIn("@person", body)
            marker = "<!-- battlefield-cross-review:claude:h:b -->"
            with mock.patch.object(review, "pr_state", return_value=state), mock.patch.object(review, "gh", return_value=marker) as gh:
                review.publish(output, 1, output, metadata)
                self.assertEqual(metadata["publication_status"], "already_posted")
                self.assertEqual(gh.call_count, 1)

    def test_publication_rejects_stale_failed_or_closed_review(self):
        metadata = {"status": "completed_advisory_review", "reviewer": "claude",
                    "head": "h", "base": "b", "merge_base": "m"}
        state = {"state": "open", "head": "h", "base": "b", "url": "url"}
        for key, value in (("head", "new"), ("base", "new"), ("state", "closed")):
            with self.subTest(key=key), mock.patch.object(review, "pr_state", return_value={**state, key: value}), mock.patch.object(review, "gh") as gh:
                with self.assertRaises(RuntimeError):
                    review.publish(Path("."), 1, Path("."), metadata)
                gh.assert_not_called()
        for status in ("failed", "dry_run_not_reviewed"):
            with self.subTest(status=status), self.assertRaises(RuntimeError):
                review.publish(Path("."), 1, Path("."), {**metadata, "status": status})

    def test_publication_failure_retains_completed_review_for_retry(self):
        metadata = {"status": "completed_advisory_review"}
        with tempfile.TemporaryDirectory() as tmp:
            output = Path(tmp)
            with mock.patch.object(review, "publish", side_effect=RuntimeError("network")):
                with self.assertRaises(RuntimeError):
                    review.publish_and_record(output, 1, output, metadata)
            saved = json.loads((output / "metadata.json").read_text())
            self.assertEqual(saved["status"], "completed_advisory_review")
            self.assertEqual(saved["publication_status"], "failed")

    def test_timeout_and_failure_are_reported(self):
        with tempfile.TemporaryDirectory() as tmp:
            directory = Path(tmp)
            with self.assertRaisesRegex(RuntimeError, "timed out"):
                review.run_process([sys.executable, "-c", "import time;time.sleep(30)"], "", directory, 0.05,
                                   directory / "out", directory / "err")
            with self.assertRaisesRegex(RuntimeError, "exited 7"):
                review.run_process([sys.executable, "-c", "raise SystemExit(7)"], "", directory, 5,
                                   directory / "out", directory / "err")


if __name__ == "__main__":
    unittest.main()
