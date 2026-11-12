#!/usr/bin/env python3
"""
Self-test for the shared managed-routing DNS fixtures (R5 §7.3), stdlib
unittest only.

It starts the real dns-stub.py on a loopback port and drives it with the
real dnsq.py plus raw frames, asserting the canonical fixture identity:

    A     -> ANCOUNT=1, answer TYPE=A,    RDATA=203.0.113.10
    AAAA  -> ANCOUNT=1, answer TYPE=AAAA, RDATA=2001:db8:dead::10
    TXT   -> ANCOUNT=0
    malformed frame -> no reply

Run:
    python3 linux/tests/toad/fixtures/test_dns_fixtures.py
"""

import os
import socket
import struct
import subprocess
import sys
import time
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
STUB = os.path.join(HERE, "dns-stub.py")
DNSQ = os.path.join(HERE, "dnsq.py")

FIXTURE_A = "203.0.113.10"
FIXTURE_AAAA = "2001:db8:dead::10"


def build_query(name: str, qtype: int, tid: int = 0x1234) -> bytes:
    # Full 12-byte header: tid, flags, QDCOUNT, ANCOUNT, NSCOUNT, ARCOUNT.
    q = struct.pack("!HHHHHH", tid, 0x0100, 1, 0, 0, 0)
    for label in name.encode().split(b"."):
        q += bytes([len(label)]) + label
    q += b"\x00" + struct.pack("!HH", qtype, 1)
    return q


class DnsFixtureTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.port = 15354
        # Find a free port so parallel runs never collide.
        probe = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
        probe.bind(("127.0.0.1", 0))
        cls.port = probe.getsockname()[1]
        probe.close()
        cls.stub = subprocess.Popen(
            [sys.executable, STUB],
            env={**os.environ, "DNS_STUB_PORT": str(cls.port)},
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
        )
        cls.sock = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
        cls.sock.settimeout(4)
        deadline = time.time() + 5
        while time.time() < deadline:
            try:
                cls.exchange(build_query("probe.test", 16))
                break
            except (socket.timeout, OSError):
                if cls.stub.poll() is not None:
                    raise RuntimeError("dns-stub.py exited during startup")
                time.sleep(0.1)
        else:
            raise RuntimeError("dns-stub.py never answered")

    @classmethod
    def tearDownClass(cls):
        cls.sock.close()
        cls.stub.terminate()
        cls.stub.wait(timeout=5)

    @classmethod
    def exchange(cls, query: bytes, timeout: float = 4.0) -> bytes | None:
        cls.sock.settimeout(timeout)
        cls.sock.sendto(query, ("127.0.0.1", cls.port))
        try:
            data, _ = cls.sock.recvfrom(4096)
        except socket.timeout:
            return None
        return data

    @staticmethod
    def parse_reply(reply: bytes):
        """Return (rcode, ancount, [(type, rdata)]) from a response frame."""
        tid, flags, qd, an, ns, ar = struct.unpack("!HHHHHH", reply[:12])
        rcode = flags & 0x0F
        answers = []
        i = 12
        # Skip the echoed question.
        while reply[i] != 0:
            i += 1 + reply[i]
        i += 1 + 4
        for _ in range(an):
            # Owner name: compressed (c0 0c) in this fixture.
            if reply[i] & 0xC0 == 0xC0:
                i += 2
            else:
                while reply[i] != 0:
                    i += 1 + reply[i]
                i += 1
            rtype, rclass, ttl, rdlength = struct.unpack("!HHIH", reply[i:i + 10])
            i += 10
            answers.append((rtype, reply[i:i + rdlength]))
            i += rdlength
        return rcode, an, answers

    def test_a_answer_carries_the_canonical_ipv4(self):
        reply = self.exchange(build_query("g1.test", 1))
        self.assertIsNotNone(reply)
        rcode, ancount, answers = self.parse_reply(reply)
        self.assertEqual(rcode, 0)
        self.assertEqual(ancount, 1)
        self.assertEqual(answers[0][0], 1)  # TYPE A
        self.assertEqual(socket.inet_ntop(socket.AF_INET, answers[0][1]), FIXTURE_A)

    def test_aaaa_answer_carries_the_canonical_ipv6(self):
        reply = self.exchange(build_query("g1.test", 28))
        self.assertIsNotNone(reply)
        rcode, ancount, answers = self.parse_reply(reply)
        self.assertEqual(rcode, 0)
        self.assertEqual(ancount, 1)
        self.assertEqual(answers[0][0], 28)  # TYPE AAAA
        self.assertEqual(socket.inet_ntop(socket.AF_INET6, answers[0][1]), FIXTURE_AAAA)

    def test_txt_query_returns_nodata(self):
        reply = self.exchange(build_query("g1.test", 16))
        self.assertIsNotNone(reply)
        rcode, ancount, answers = self.parse_reply(reply)
        self.assertEqual(rcode, 0)
        self.assertEqual(ancount, 0)
        self.assertEqual(answers, [])

    def test_malformed_frame_gets_no_reply(self):
        # Truncated header and a truncated question both drop silently.
        self.assertIsNone(self.exchange(b"\x12\x34", timeout=0.6))
        self.assertIsNone(self.exchange(build_query("g1.test", 1)[:-3], timeout=0.6))

    def test_question_count_is_pinned_to_one(self):
        # The R5 fixture contract is single-question: QDCOUNT=0 and
        # QDCOUNT=2 are malformed for this stub and must drop silently.
        zero = struct.pack("!HHHHHH", 0x1234, 0x0100, 0, 0, 0, 0) + b"\x00" + struct.pack("!HH", 1, 1)
        self.assertIsNone(self.exchange(zero, timeout=0.6))

        double = struct.pack("!HHHHHH", 0x1234, 0x0100, 2, 0, 0, 0)
        for label in b"g1.test".split(b"."):
            double += bytes([len(label)]) + label
        double += b"\x00" + struct.pack("!HH", 1, 1)
        double += b"\x03www" + b"\x02ru" + b"\x00" + struct.pack("!HH", 28, 1)
        self.assertIsNone(self.exchange(double, timeout=0.6))

    def test_dnsq_helper_reads_the_same_stub(self):
        out = subprocess.run(
            [sys.executable, DNSQ, "g1.test", "1"],
            env={**os.environ, "DNSQ_RESOLVER": f"127.0.0.1:{self.port}"},
            capture_output=True, text=True, timeout=10,
        )
        self.assertEqual(out.returncode, 0, out.stderr)
        self.assertEqual(out.stdout.strip(), "rcode=0 answers=1")


if __name__ == "__main__":
    unittest.main()
