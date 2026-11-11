#!/usr/bin/env python3
"""
Deterministic DNS query helper used by Kikimora integration tests (Phase F).

Performs a single UDP DNS transaction for the given name and type, prints
"rcode=N answers=M" to stdout, and exits 0 on success or 1 on timeout /
error.

Usage:
    python3 dnsq.py <name> <qtype>

Examples:
    python3 dnsq.py example.com 1       # A query
    python3 dnsq.py example.com 28      # AAAA query

The target resolver defaults to 127.0.0.1:15353; override with the
DNSQ_RESOLVER environment variable (host:port).
"""

import os
import socket
import struct
import sys


def main() -> None:
    qname: str = sys.argv[1]
    qtype: int = int(sys.argv[2])
    resolver: str = os.environ.get("DNSQ_RESOLVER", "127.0.0.1:15353")
    host, port_str = resolver.rsplit(":", 1)
    port: int = int(port_str)

    # Build the query frame.
    q = b"\x12\x34" + struct.pack("!HHHHH", 0x0100, 1, 0, 0, 0)
    for label in qname.encode().split(b"."):
        q += bytes([len(label)]) + label
    q += b"\x00" + struct.pack("!HH", qtype, 1)

    sock = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
    sock.settimeout(4)
    sock.sendto(q, (host, port))
    try:
        data, _ = sock.recvfrom(4096)
    except socket.timeout:
        print("rcode=timeout answers=0")
        sys.exit(1)

    rcode = data[3] & 0x0F
    ancount = struct.unpack("!H", data[6:8])[0]
    print(f"rcode={rcode} answers={ancount}")


if __name__ == "__main__":
    main()