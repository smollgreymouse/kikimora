#!/usr/bin/env python3
"""
Deterministic DNS stub used by every Kikimora managed-routing integration
test (08D1 Phase F, remediation R5).

Canonical answers for the shared fixture identity:

    A     -> 203.0.113.10
    AAAA  -> 2001:db8:dead::10

Any other query type receives NOERROR with zero answers (NODATA).  The
first question is parsed from byte 12: labels are walked to the
terminating zero, then QTYPE and QCLASS are read.  Malformed frames are
dropped safely (no reply) instead of being answered from wrong bytes.

The server binds 127.0.0.1:15354 by default; override the port with the
DNS_STUB_PORT environment variable.

Usage:
    python3 dns-stub.py                        # bind :15354
    DNS_STUB_PORT=15355 python3 dns-stub.py    # bind :15355
"""

import os
import socket
import struct

FIXTURE_A = "203.0.113.10"
FIXTURE_AAAA = "2001:db8:dead::10"

QTYPE_A = 1
QTYPE_AAAA = 28
QCLASS_IN = 1


def parse_question(query: bytes):
    """Return (tid, qtype, qclass, question_end) or None for a malformed
    frame. The header must declare exactly one question (QDCOUNT=1, the R5
    single-question fixture contract): zero or multiple questions are
    malformed for this fixture and dropped. The question starts at byte 12:
    walk labels to the terminating zero, then read QTYPE and QCLASS."""
    if len(query) < 12:
        return None
    qdcount, = struct.unpack("!H", query[4:6])
    if qdcount != 1:
        return None
    tid = query[:2]
    i = 12
    while i < len(query):
        length = query[i]
        if length == 0:
            i += 1
            break
        i += 1 + length
    else:
        return None
    if i + 4 > len(query):
        return None
    qtype, qclass = struct.unpack("!HH", query[i:i + 4])
    return tid, qtype, qclass, i


def build_reply(query: bytes) -> bytes | None:
    parsed = parse_question(query)
    if parsed is None:
        return None
    tid, qtype, qclass, _ = parsed

    # Single-question, class-IN fixture contract (R5 §7.1).
    if qclass != QCLASS_IN:
        return None

    flags = 0x8180  # QR=1, response, NOERROR
    rdata: bytes | None
    if qtype == QTYPE_A:
        rdata = socket.inet_aton(FIXTURE_A)
    elif qtype == QTYPE_AAAA:
        rdata = socket.inet_pton(socket.AF_INET6, FIXTURE_AAAA)
    else:
        rdata = None

    # Echo the question section verbatim; the fixture never compresses it.
    question = query[12:]
    if rdata is None:
        # NOERROR + zero answers (NODATA).
        return tid + struct.pack("!HHHHH", flags, 1, 0, 0, 0) + question

    # One answer (compressed owner name, 60 s TTL).
    reply = tid + struct.pack("!HHHHH", flags, 1, 1, 0, 0) + question
    reply += b"\xc0\x0c" + struct.pack("!HHIH", qtype, QCLASS_IN, 60, len(rdata)) + rdata
    return reply


def main() -> None:
    port = int(os.environ.get("DNS_STUB_PORT", "15354"))
    sock = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
    sock.bind(("127.0.0.1", port))
    while True:
        data, peer = sock.recvfrom(4096)
        reply = build_reply(data)
        if reply:
            sock.sendto(reply, peer)


if __name__ == "__main__":
    main()
