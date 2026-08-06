from __future__ import annotations

import importlib.util
import io
import pathlib
import subprocess
import unittest
from unittest import mock

HOST_PATH = pathlib.Path(__file__).parents[1] / "native-host" / "kikimora_native_host.py"
SPEC = importlib.util.spec_from_file_location("kikimora_native_host", HOST_PATH)
assert SPEC and SPEC.loader
host = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(host)


class NativeHostTests(unittest.TestCase):
    def test_domain_normalization(self):
        self.assertEqual(host.normalize_domain(" GitLab.Example.COM. "), "gitlab.example.com")
        self.assertEqual(host.normalize_domain("*.example.com"), "*.example.com")

    def test_domain_rejects_urls_and_shell_text(self):
        for value in ("https://example.com", "example.com/path", "example.com;id", "localhost"):
            with self.subTest(value=value):
                with self.assertRaises(host.RequestError):
                    host.normalize_domain(value)

    def test_zone_validation(self):
        self.assertEqual(host.normalize_zone("primary"), "primary")
        self.assertEqual(host.normalize_zone("secondary"), "secondary")
        with self.assertRaises(host.RequestError):
            host.normalize_zone("bypass")

    def test_origin_validation(self):
        host.validate_origin(["native-host", host.ALLOWED_ORIGIN])
        host.validate_origin(["native-host", host.ALLOWED_ORIGIN.rstrip("/")])
        with self.assertRaises(host.RequestError):
            host.validate_origin(["native-host", "chrome-extension://aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/"])

    def test_message_framing(self):
        outgoing = io.BytesIO()
        host.write_message(outgoing, {"ok": True, "message": "готово"})
        outgoing.seek(0)
        decoded = host.read_message(outgoing)
        self.assertEqual(decoded, {"ok": True, "message": "готово"})

    def test_domain_list_parser_sorts_deduplicates_and_ignores_blanks(self):
        self.assertEqual(
            host.parse_domain_list("z.example.com\n\nA.example.com\na.example.com\n"),
            ["a.example.com", "z.example.com"],
        )

    def test_domain_list_parser_rejects_unexpected_cli_output(self):
        with self.assertRaises(host.RequestError):
            host.parse_domain_list("== primary ==\nexample.com\n")

    @mock.patch.object(host.os.path, "isfile", return_value=True)
    @mock.patch.object(host.os, "access", return_value=True)
    @mock.patch.object(host.subprocess, "run")
    def test_list_reads_both_zones_without_pkexec(self, run, _access, _isfile):
        run.side_effect = [
            subprocess.CompletedProcess([], 0, "alpha.example.com\n", ""),
            subprocess.CompletedProcess([], 0, "beta.example.com\n", ""),
        ]

        result = host.process_message({"action": "list-domains"})

        self.assertEqual(
            result["zones"],
            {
                "primary": ["alpha.example.com"],
                "secondary": ["beta.example.com"],
            },
        )
        self.assertEqual(result["counts"], {"primary": 1, "secondary": 1})
        self.assertEqual(
            [call.args[0] for call in run.call_args_list],
            [
                ["/usr/local/sbin/kikimora", "domains", "list", "primary"],
                ["/usr/local/sbin/kikimora", "domains", "list", "secondary"],
            ],
        )

    @mock.patch.object(host.os.path, "isfile", return_value=True)
    @mock.patch.object(host.os, "access", return_value=True)
    @mock.patch.object(host.subprocess, "run")
    def test_add_calls_validated_kikimora_cli(self, run, _access, _isfile):
        run.return_value = subprocess.CompletedProcess([], 0, "Added: example.com (secondary)\n", "")
        result = host.process_message({
            "action": "add-domain",
            "domain": "example.com",
            "zone": "secondary",
        })
        self.assertTrue(result["ok"])
        self.assertEqual(
            run.call_args.args[0],
            [
                "/usr/bin/pkexec",
                "/usr/local/sbin/kikimora",
                "domains",
                "add",
                "example.com",
                "--secondary",
            ],
        )

    @mock.patch.object(host.os.path, "isfile", return_value=True)
    @mock.patch.object(host.os, "access", return_value=True)
    @mock.patch.object(host.subprocess, "run")
    def test_remove_calls_validated_kikimora_cli(self, run, _access, _isfile):
        run.return_value = subprocess.CompletedProcess([], 0, "Removed: example.com (primary)\n", "")
        result = host.process_message({
            "action": "remove-domain",
            "domain": "example.com",
            "zone": "primary",
        })
        self.assertEqual(result["action"], "remove")
        self.assertEqual(
            run.call_args.args[0],
            [
                "/usr/bin/pkexec",
                "/usr/local/sbin/kikimora",
                "domains",
                "remove",
                "example.com",
                "--primary",
            ],
        )

    def test_ping_does_not_require_privileges(self):
        result = host.process_message({"action": "ping"})
        self.assertEqual(result["extension_id"], host.EXTENSION_ID)


if __name__ == "__main__":
    unittest.main()
