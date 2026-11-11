#!/usr/bin/env python3
import importlib.util
import os
from pathlib import Path
import sys
import unittest
from unittest import mock

MODULE_PATH = Path(__file__).with_name("vm.py")
SPEC = importlib.util.spec_from_file_location("kikimora_lab_vm", MODULE_PATH)
vm = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = vm
assert SPEC.loader is not None
SPEC.loader.exec_module(vm)


class GuestPropertyParsingTest(unittest.TestCase):
    def test_prefers_requested_lan_adapter(self):
        text = """
/VirtualBox/GuestInfo/Net/0/V4/IP = '10.8.1.3'
/VirtualBox/GuestInfo/Net/1/V4/IP = '192.168.3.72'
/VirtualBox/GuestInfo/Net/2/V4/IP = '172.17.0.1'
"""
        self.assertEqual(vm.parse_guest_ipv4(text, 1), "192.168.3.72")

    def test_rejects_non_lab_addresses(self):
        text = """
/VirtualBox/GuestInfo/Net/0/V4/IP = '10.8.1.3'
/VirtualBox/GuestInfo/Net/1/V4/IP = '192.0.2.1'
"""
        self.assertIsNone(vm.parse_guest_ipv4(text, 0))


class HostResolutionTest(unittest.TestCase):
    def test_explicit_host_wins(self):
        target = vm.TARGETS["ubuntu"]
        with mock.patch.dict(os.environ, {target.host_env: "192.168.3.99"}):
            with mock.patch.object(vm, "discover_vbox_host", return_value="192.168.3.98"):
                self.assertEqual(
                    vm.resolve_host(target, "192.168.3.97"),
                    ("192.168.3.97", "--host"),
                )

    def test_environment_wins_over_vbox(self):
        target = vm.TARGETS["ubuntu"]
        with mock.patch.dict(os.environ, {target.host_env: "192.168.3.99"}):
            with mock.patch.object(vm, "discover_vbox_host", return_value="192.168.3.98"):
                self.assertEqual(
                    vm.resolve_host(target, None),
                    ("192.168.3.99", target.host_env),
                )

    def test_vbox_wins_over_fallback(self):
        target = vm.TARGETS["windows"]
        with mock.patch.dict(os.environ, {}, clear=False):
            with mock.patch.object(vm, "discover_vbox_host", return_value="192.168.3.73"):
                self.assertEqual(
                    vm.resolve_host(target, None),
                    ("192.168.3.73", "VirtualBox guest property"),
                )

    def test_documented_fallbacks(self):
        with mock.patch.object(vm, "discover_vbox_host", return_value=None):
            with mock.patch.dict(
                os.environ,
                {
                    "KIKIMORA_UBUNTU_VM_HOST": "",
                    "KIKIMORA_WINDOWS_VM_HOST": "",
                },
            ):
                self.assertEqual(
                    vm.resolve_host(vm.TARGETS["ubuntu"], None),
                    ("192.168.3.74", "documented fallback"),
                )
                self.assertEqual(
                    vm.resolve_host(vm.TARGETS["windows"], None),
                    ("192.168.3.72", "documented fallback"),
                )


if __name__ == "__main__":
    unittest.main()
