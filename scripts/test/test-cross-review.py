#!/usr/bin/env python3
"""Exercise immutable review inputs, rejected results, and read-only CLI settings."""
import importlib.util
import contextlib
import io
import tarfile
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
            dry_output = Path(tmp) / "dry-output"
            arguments = ["cross-review", "--author", "codex", "--repo", str(repo),
                         "--base", base, "--output", str(dry_output), "--dry-run"]
            with mock.patch.object(sys, "argv", arguments), mock.patch("builtins.print"):
                review.main()
            saved = json.loads((dry_output / "metadata.json").read_text())
            self.assertEqual(saved["status"], "dry_run_not_reviewed")
            self.assertTrue(saved["working_tree_changes_excluded"])
            self.assertNotIn("uncommitted", (dry_output / "review.patch").read_text())
            missing_output = Path(tmp) / "missing-report"
            arguments = ["cross-review", "--author", "claude", "--repo", str(repo),
                         "--base", base, "--output", str(missing_output)]
            with mock.patch.object(sys, "argv", arguments), mock.patch("builtins.print"), mock.patch.object(review.shutil, "which", return_value="/bin/codex"), mock.patch.object(review, "run_process"), mock.patch.object(review, "check_codex_version"):
                with self.assertRaisesRegex(RuntimeError, "returned no review"):
                    review.main()
            saved = json.loads((missing_output / "metadata.json").read_text())
            self.assertEqual(saved["status"], "failed")

    def test_read_only_cli_policies(self):
        claude = review.command("claude", "/bin/claude", Path("/tmp/snapshot"), Path("/tmp/report"))
        self.assertEqual(claude[claude.index("--model") + 1], "opus")
        self.assertIn("--restricted", claude)
        self.assertEqual(claude[claude.index("--tools") + 1], "Read,Glob,Grep")
        self.assertIn("--strict-mcp-config", claude)
        self.assertIn('{"disableAllHooks":true}', claude)
        codex = review.command("codex", "/bin/codex", Path("/tmp/snapshot"), Path("/tmp/report"))
        self.assertEqual(codex[codex.index("--model") + 1], "gpt-6-astra")
        self.assertIn('model_reasoning_effort="high"', codex)
        self.assertNotIn("--sandbox", codex)
        self.assertTrue(any(arg.startswith('permissions.review.filesystem={":minimal"="read",":workspace_roots"="read",') for arg in codex))
        self.assertIn('permissions.review.network.enabled=false', codex)
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
            with mock.patch.object(review, "pr_state", return_value=state), mock.patch.object(review, "pr_discussion", return_value=[]), mock.patch.object(review, "gh", return_value="") as gh:
                review.publish(output, 1, output, metadata)
                self.assertEqual(metadata["publication_status"], "posted")
                self.assertIn("--body-file", gh.call_args.args)
                body = (output / "comment.md").read_text()
                self.assertIn("Reviewed commit: `h`", body)
                self.assertIn("Finding at source.py:4", body)
                self.assertNotIn("@person", body)
            marker = "<!-- battlefield-cross-review:claude:h:b -->"
            with mock.patch.object(review, "pr_state", return_value=state), mock.patch.object(review, "pr_discussion", return_value=[{"id": 1, "body": body, "user_login": "publisher"}]), mock.patch.object(review, "gh", return_value="publisher\n") as gh:
                review.publish(output, 1, output, metadata)
                self.assertEqual(metadata["publication_status"], "already_posted")
                self.assertEqual(gh.call_args.args[1:], ("api", "user", "--jq", ".login"))
            # A copied exact report from another account is not our publication.
            with mock.patch.object(review, "pr_state", return_value=state), mock.patch.object(review, "pr_discussion", return_value=[{"id": 2, "body": body, "user_login": "other"}]), mock.patch.object(review, "gh", return_value="publisher\n") as gh:
                review.publish(output, 1, output, metadata)
                self.assertEqual(metadata["publication_status"], "posted")
                self.assertEqual(gh.call_args.args[1:3], ("pr", "comment"))

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

    def test_discussion_includes_both_sides_and_rounds(self):
        pages = [[{"id": 1, "html_url": "url1", "body": "<!-- battlefield-cross-review:claude:h:b --> F1"},
                  {"id": 2, "html_url": "url2", "body": "CI build output"}],
                 [{"id": 3, "html_url": "url3", "body": "<!-- battlefield-author-response:codex --> F1 disputed with evidence"}]]
        with mock.patch.object(review, "gh", return_value="\n".join(json.dumps(c) for page in pages for c in page)):
            comments, text, number = review.discussion_context(Path("."), 1)
        self.assertEqual([c["id"] for c in comments], [1, 3])
        self.assertIn("disputed with evidence", text)
        self.assertEqual(number, 2)
        changed = [dict(c) for c in comments]
        changed[-1]["body"] = "edited response"
        self.assertNotEqual(review.discussion_digest(comments), review.discussion_digest(changed))

    def test_stale_discussion_is_not_published(self):
        metadata = {"status": "completed_advisory_review", "reviewer": "claude",
                    "head": "h", "base": "b", "merge_base": "m", "review_round": 2,
                    "discussion_ids": [1], "discussion_sha256": review.discussion_digest([{"id": 1, "body": "old"}])}
        state = {"state": "open", "head": "h", "base": "b", "url": "url"}
        for comments in ([{"id": 1, "body": "edited"}], [{"id": 1, "body": "old"}, {"id": 2, "body": "new"}]):
            with tempfile.TemporaryDirectory() as tmp:
                output = Path(tmp)
                (output / "review.md").write_text("Review")
                with mock.patch.object(review, "pr_state", return_value=state), mock.patch.object(review, "pr_discussion", return_value=comments), mock.patch.object(review, "gh") as gh:
                    with self.assertRaises(RuntimeError):
                        review.publish(output, 1, output, metadata)
                    gh.assert_not_called()

    def test_discussion_is_never_silently_truncated(self):
        with mock.patch.object(review, "pr_discussion", return_value=[{"body": "x" * (256 * 1024)}]):
            with self.assertRaisesRegex(RuntimeError, "exceeds"):
                review.discussion_context(Path("."), 1)

    def test_live_base_ref_overrides_stale_pr_base_sha(self):
        pr = {"head": {"sha": "h"}, "base": {"sha": "stale", "ref": "release/1.2",
              "repo": {"full_name": "owner/repo"}}, "state": "open", "html_url": "url"}
        with mock.patch.object(review, "gh", side_effect=[json.dumps(pr), json.dumps({"object": {"sha": "live"}})]) as gh:
            state = review.pr_state(Path("."), 75)
        self.assertEqual(state["base"], "live")
        self.assertEqual(gh.call_args.args[-1], "repos/owner/repo/git/ref/heads/release/1.2")
        review.check_pr({"head": "h", "base": "live"}, state)

    def test_oversized_patch_and_archive_escape_are_rejected(self):
        with tempfile.TemporaryDirectory() as tmp:
            with mock.patch.object(review, "resolve", return_value="sha"), mock.patch.object(review, "git", side_effect=[b"sha", b"x" * (2 * 1024 * 1024 + 1)]):
                with self.assertRaisesRegex(ValueError, "exceeds"):
                    review.prepare(Path(tmp), "base", "head", Path(tmp))
            data = io.BytesIO()
            with tarfile.open(fileobj=data, mode="w") as archive:
                entry = tarfile.TarInfo("../escape.txt")
                entry.size = 4
                archive.addfile(entry, io.BytesIO(b"evil"))
            with mock.patch.object(review, "resolve", return_value="sha"), mock.patch.object(review, "git", side_effect=[b"sha", b"diff", data.getvalue()]):
                with self.assertRaisesRegex(ValueError, "escapes"):
                    review.prepare(Path(tmp), "base", "head", Path(tmp))

    def test_oversized_comment_never_posts(self):
        metadata = {"status": "completed_advisory_review", "reviewer": "claude", "head": "h", "base": "b", "merge_base": "m"}
        state = {"state": "open", "head": "h", "base": "b", "url": "url"}
        with tempfile.TemporaryDirectory() as tmp:
            output = Path(tmp)
            (output / "review.md").write_text("x" * 60000)
            with mock.patch.object(review, "pr_state", return_value=state), mock.patch.object(review, "pr_discussion", return_value=[]), mock.patch.object(review, "gh") as gh:
                with self.assertRaisesRegex(RuntimeError, "limit"):
                    review.publish(output, 1, output, metadata)
                gh.assert_not_called()

    def test_timeout_exit_race_keeps_useful_error(self):
        with tempfile.TemporaryDirectory() as tmp:
            child = mock.Mock(pid=123)
            child.communicate.side_effect = [subprocess.TimeoutExpired("review", 1), None]
            with mock.patch.object(review.subprocess, "Popen", return_value=child), mock.patch.object(review.os, "killpg", side_effect=ProcessLookupError):
                with self.assertRaisesRegex(RuntimeError, "timed out"):
                    review.run_process(["review"], "", Path(tmp), 1, Path(tmp)/"out", Path(tmp)/"err")

    def test_codex_version_requirement(self):
        for version in ("codex-cli 0.155.0\n", "codex-cli 0.156.1\n"):
            with mock.patch.object(review.subprocess, "check_output", return_value=version):
                review.check_codex_version("codex")
        for version in ("codex-cli 0.152.0\n", "unknown", "codex-cli 0.155.0-beta"):
            with mock.patch.object(review.subprocess, "check_output", return_value=version), self.assertRaisesRegex(RuntimeError, "0.155.0"):
                review.check_codex_version("codex")

    def test_argument_validation_and_pre_inference_pr_guard(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp) / "repo"
            root.mkdir()
            output = Path(tmp) / "report"
            base_args = ["review", "--author", "codex", "--repo", str(root), "--output", str(output)]
            cases = (["--output", str(root / "report")], ["--pr", "1", "--dry-run"], ["--publish-only"])
            for extra in cases:
                with self.subTest(extra=extra), mock.patch.object(sys, "argv", base_args + extra), mock.patch.object(review, "git", return_value=str(root).encode()), contextlib.redirect_stderr(io.StringIO()):
                    with self.assertRaises(SystemExit):
                        review.main()
            output.mkdir()
            (output / "metadata.json").write_text(json.dumps({"author": "claude"}))
            with mock.patch.object(sys, "argv", base_args + ["--pr", "1", "--publish-only"]), mock.patch.object(review, "git", return_value=str(root).encode()), contextlib.redirect_stderr(io.StringIO()):
                with self.assertRaises(SystemExit):
                    review.main()
            arguments = base_args + ["--output", str(Path(tmp) / "stale"), "--pr", "1"]
            revisions = {"head": "h", "base": "b", "merge_base": "m"}
            with mock.patch.object(sys, "argv", arguments), mock.patch.object(review, "git", side_effect=[str(root).encode(), b""]), mock.patch.object(review.shutil, "which", return_value="claude"), mock.patch.object(review, "prepare", return_value=(revisions, b"diff")), mock.patch.object(review, "pr_state", return_value={"state": "open", "head": "changed", "base": "b"}), mock.patch.object(review, "run_process") as run:
                with self.assertRaisesRegex(RuntimeError, "differs"):
                    review.main()
                run.assert_not_called()

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
