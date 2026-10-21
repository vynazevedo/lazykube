import contextlib
import io
import os
from pathlib import Path
import runpy
import unittest
from unittest.mock import patch
import urllib.error


SCRIPT = Path(__file__).with_name("check-release.py")


class ReleaseGuardTest(unittest.TestCase):
    def execute(self, status=200, body=b'{"draft": true}', failure=None):
        response = io.BytesIO(body)
        response.status = status
        env = {
            "RELEASE_TAG": "v1.0.0/test",
            "GITHUB_REPOSITORY": "owner/repo",
            "GITHUB_API_URL": "https://api.github.com",
            "GH_TOKEN": "fixture",
        }
        with patch.dict(os.environ, env, clear=True), patch(
            "urllib.request.urlopen", return_value=response, side_effect=failure
        ) as opened, contextlib.redirect_stdout(io.StringIO()), contextlib.redirect_stderr(io.StringIO()):
            try:
                runpy.run_path(str(SCRIPT), run_name="__main__")
            except SystemExit as error:
                return error.code
            self.assertTrue(opened.call_args.args[0].full_url.endswith("v1.0.0%2Ftest"))
            return 0

    def test_draft_allowed(self):
        self.assertEqual(self.execute(), 0)

    def test_missing_allowed(self):
        error = urllib.error.HTTPError("fixture", 404, "missing", {}, None)
        self.assertEqual(self.execute(failure=error), 0)

    def test_public_or_invalid_refused(self):
        for body in (b'{"draft": false}', b'{}', b'null', b'[]', b'{"draft": "true"}', b'broken'):
            with self.subTest(body=body):
                self.assertEqual(self.execute(body=body), 1)

    def test_api_failures_refused(self):
        for code in (401, 403, 429, 500, 502):
            with self.subTest(code=code):
                error = urllib.error.HTTPError("fixture", code, "failure", {}, None)
                self.assertEqual(self.execute(failure=error), 1)
        self.assertEqual(self.execute(failure=urllib.error.URLError("offline")), 1)
        self.assertEqual(self.execute(failure=TimeoutError("timeout")), 1)
        self.assertEqual(self.execute(status=202), 1)


if __name__ == "__main__":
    unittest.main()
