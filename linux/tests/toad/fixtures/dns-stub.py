#!/usr/bin/env python3
"""
Deterministic DNS stub used by Kikimora integration tests (Phase F).

Answers every A query with 203.0.113.10 and every AAAA query with
2001:db8::10.  All other query types receive a reply with zero answers
(NOERROR + zero answer count).  The server binds to 127.0.0.1:15354
by default; override with the DSN_STUB_PORT environment variable.

Usage:
    python3 dns-stub.py                        # bind :15354
    DSN_STUB_PORT=15355 python3 dns-stub.py    # bind :15355
"""

import os
import socket
import struct


def build_reply(query: bytes) -> bytes | None:
    if len(query) < 12:
        return None
    tid = query[:2]
    qtype = struct.unpack("!H", query[4:6])[0]
    flags = 0x8180  # QR=1, response, NOERROR

    # Determine which address (if any) to return.
    rdata: bytes | None
    if qtype == 1:
        rdata = socket.inet_aton("203.0.113.10")
    elif qtype == 28:
        rdata = socket.inet_pton(socket.AF_INET6, "2001:db8::10")
    else:
        rdata = None

    if rdata is None:
        # NOERROR + zero answers
        return tid + struct.pack("!HHHHH", flags, 1, 0, 0, 0) + query[12:]

    # One answer (compressed name, 60 s TTL).
    answer_count = 1
    reply = tid + struct.pack("!HHHHH", flags, 1, answer_count, 0, 0) + query[12:]
    reply += b"\xc0\x0c" + struct.pack("!HHIH", qtype, 1, 60, len(rdata)) + rdata
    return reply


def main() -> None:
    port = int(os.environ.get("DSN_STUB_PORT", "15354"))
    sock = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
    sock.bind(("127.0.0.1", port))
    while True:
        data, peer = sock.recvfrom(4096)
        reply = build_reply(data)
        if reply:
            sock.sendto(reply, peer)


if __name__ == "__main__":
    main()