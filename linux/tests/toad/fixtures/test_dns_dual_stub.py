#!/usr/bin/env python3
"""Unit contract for the dual-role DNS stub (T1R §7.5, 2026-11-16
post-e7ec9c2 audit): the managed-dual-profile harness's D9/D10/D12/D13 rows
depend on this fixture answering deterministically per role, so its parsing
and answer mapping are pinned here instead of only py_compile."""

import importlib.util
import os
import struct
import unittest

FIXTURE = os.path.join(os.path.dirname(os.path.abspath(__file__)), "dns-dual-stub.py")

spec = importlib.util.spec_from_file_location("dns_dual_stub", FIXTURE)
dns_dual_stub = importlib.util.module_from_spec(spec)
spec.loader.exec_module(dns_dual_stub)

PRIMARY = ("203.0.113.10", "2001:db8:dead::10")
SECONDARY = ("203.0.113.20", "2001:db8:dead::20")
CATCHALL = ("203.0.113.99", "2001:db8:dead::99")

QTYPE_A = 1
QTYPE_AAAA = 28
QTYPE_TXT = 16
QCLASS_IN = 1
QCLASS_CH = 3


def build_query(name, qtype, qclass=QCLASS_IN, qdcount=1, tid=b"\x12\x34"):
    query = tid + struct.pack("!HHHHH", 0x0100, qdcount, 0, 0, 0)
    if name:
        for label in name.encode().split(b"."):
            query += bytes([len(label)]) + label
        query += b"\x00"
    query += struct.pack("!HH", qtype, qclass)
    return query


def decode_name(reply, offset=12):
    labels = []
    while True:
        length = reply[offset]
        offset += 1
        if length == 0:
            break
        labels.append(reply[offset:offset + length].decode())
        offset += length
    return ".".join(labels), offset


class DnsDualStubTest(unittest.TestCase):
    def assert_answer(self, reply, constant):
        self.assertIsNotNone(reply)
        tid = reply[:2]
        flags, _, an, _, _ = struct.unpack("!HHHHH", reply[2:12])
        self.assertEqual(tid, b"\x12\x34")
        self.assertEqual(an, 1)
        rcode = flags & 0x0F
        self.assertEqual(rcode, 0)
        name, offset = decode_name(reply)
        self.assertEqual(name, self.current_name)
        # The echoed question's QTYPE/QCLASS (4 bytes) and the answer's
        # compressed 2-byte name pointer sit before the answer RR header.
        offset += 6
        atype, aclass, ttl, rdlength = struct.unpack("!HHIH", reply[offset:offset + 10])
        self.assertEqual(aclass, QCLASS_IN)
        self.assertEqual(ttl, 60)
        rdata = reply[offset + 10:offset + 10 + rdlength]
        if atype == QTYPE_A:
            import socket
            self.assertEqual(socket.inet_ntoa(rdata), constant[0])
        else:
            import socket
            self.assertEqual(socket.inet_ntop(socket.AF_INET6, rdata), constant[1])

    def query(self, name, qtype):
        self.current_name = name
        return dns_dual_stub.build_reply(build_query(name, qtype))

    def test_primary_names_answer_primary_constants(self):
        for name in ("primary.test", "primary2.test", "primary-extra.test"):
            self.assert_answer(self.query(name, QTYPE_A), PRIMARY)
            self.assert_answer(self.query(name, QTYPE_AAAA), PRIMARY)

    def test_secondary_names_answer_secondary_constants(self):
        for name in ("secondary.test", "secondary9.test"):
            self.assert_answer(self.query(name, QTYPE_A), SECONDARY)
            self.assert_answer(self.query(name, QTYPE_AAAA), SECONDARY)

    def test_catchall_and_unknown_answer_catchall_constants(self):
        for name in ("catchall.test", "unrouted.example", "random.xyz"):
            self.assert_answer(self.query(name, QTYPE_A), CATCHALL)
            self.assert_answer(self.query(name, QTYPE_AAAA), CATCHALL)

    def test_probe_prefix_answers_the_probe_constant(self):
        # The D9 live-edit probe: 203.0.113.66 is owned by no other row, so
        # routing it on the primary interface proves the running Leshy
        # re-read the edited zone rather than forwarding upstream.
        self.assert_answer(self.query("probe-refresh.test", QTYPE_A), ("203.0.113.66", "2001:db8:dead::66"))

    def test_qdcount_not_one_is_rejected(self):
        self.assertIsNone(dns_dual_stub.parse_question(build_query("primary.test", QTYPE_A, qdcount=0)))
        self.assertIsNone(dns_dual_stub.parse_question(build_query("primary.test", QTYPE_A, qdcount=2)))

    def test_truncated_qname_is_rejected(self):
        truncated = b"\x12\x34" + struct.pack("!HHHHH", 0x0100, 1, 0, 0, 0) + b"\x0cprimary"
        self.assertIsNone(dns_dual_stub.parse_question(truncated))
        # A declared label longer than the remaining bytes is rejected too.
        self.assertIsNone(dns_dual_stub.parse_question(
            b"\x12\x34" + struct.pack("!HHHHH", 0x0100, 1, 0, 0, 0) + b"\x20pri\x00" + struct.pack("!HH", QTYPE_A, QCLASS_IN)))

    def test_non_in_class_gets_no_reply(self):
        self.assertIsNone(dns_dual_stub.build_reply(build_query("primary.test", QTYPE_A, qclass=QCLASS_CH)))

    def test_unsupported_qtype_is_noerror_with_zero_answers(self):
        reply = self.query("primary.test", QTYPE_TXT)
        self.assertIsNotNone(reply)
        tid = reply[:2]
        flags, _, an, _, _ = struct.unpack("!HHHHH", reply[2:12])
        _ = tid
        self.assertEqual(flags & 0x0F, 0)
        self.assertEqual(an, 0)

    def test_response_preserves_transaction_id_and_question(self):
        tid = b"\xbe\xef"
        query = tid + struct.pack("!HHHHH", 0x0100, 1, 0, 0, 0)
        for label in "secondary.test".encode().split(b"."):
            query += bytes([len(label)]) + label
        query += b"\x00" + struct.pack("!HH", QTYPE_A, QCLASS_IN)
        reply = dns_dual_stub.build_reply(query)
        self.assertTrue(reply.startswith(tid))
        # The echoed question is the whole query minus the 12-byte header; the
        # answer records follow it.
        self.assertEqual(reply[12:12 + len(query) - 12], query[12:])

    def test_short_query_is_rejected(self):
        self.assertIsNone(dns_dual_stub.parse_question(b"\x12\x34\x00"))


if __name__ == "__main__":
    unittest.main()
