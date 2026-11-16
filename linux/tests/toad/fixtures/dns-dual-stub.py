#!/usr/bin/env python3
"""
Deterministic DNS stub for the dual-role managed-routing acceptance
(linux/tests/toad/managed-dual-profile-netns.sh, PROD4).

Unlike dns-stub.py, this fixture answers per query name so two roles can own
distinct selected identities at the same time:

    primary.test     A -> 203.0.113.10      AAAA -> 2001:db8:dead::10
    secondary.test   A -> 203.0.113.20      AAAA -> 2001:db8:dead::20
    catchall.test    A -> 203.0.113.99      AAAA -> 2001:db8:dead::99
    anything else    A -> 203.0.113.99      AAAA -> 2001:db8:dead::99

Other query types receive NOERROR with zero answers. The server binds
127.0.0.1:15354 by default; override with DNS_STUB_PORT.
"""

import os
import socket
import struct

QTYPE_A = 1
QTYPE_AAAA = 28
QCLASS_IN = 1

ANSWERS = {
    "primary": ("203.0.113.10", "2001:db8:dead::10"),
    "secondary": ("203.0.113.20", "2001:db8:dead::20"),
    "catchall": ("203.0.113.99", "2001:db8:dead::99"),
    # The D9 live-edit probe: an address no other row owns, so routing it on
    # the primary interface is proof the running Leshy re-read the edited
    # zone (not upstream forwarding).
    "probe": ("203.0.113.66", "2001:db8:dead::66"),
}
DEFAULT_ANSWER = ("203.0.113.99", "2001:db8:dead::99")


def answer_for(qname):
    """Fresh names in the same family reuse the role answer, so tests can
    bypass the 60 s Leshy cache without adding a name per row."""
    label = qname.split(".", 1)[0]
    for prefix in ("primary", "secondary", "catchall", "probe"):
        if label.startswith(prefix):
            return ANSWERS[prefix]
    return DEFAULT_ANSWER


def parse_question(query):
    if len(query) < 12:
        return None
    qdcount, = struct.unpack("!H", query[4:6])
    if qdcount != 1:
        return None
    tid = query[:2]
    i = 12
    labels = []
    while i < len(query):
        length = query[i]
        i += 1
        if length == 0:
            break
        if i + length > len(query):
            return None
        labels.append(query[i:i + length].decode("ascii", "replace"))
        i += length
    else:
        return None
    if i + 4 > len(query):
        return None
    qtype, qclass = struct.unpack("!HH", query[i:i + 4])
    return tid, ".".join(labels).lower(), qtype, qclass


def build_reply(query):
    parsed = parse_question(query)
    if parsed is None:
        return None
    tid, qname, qtype, qclass = parsed
    if qclass != QCLASS_IN:
        return None

    flags = 0x8180  # QR=1, response, NOERROR
    v4, v6 = answer_for(qname)
    rdata = None
    if qtype == QTYPE_A:
        rdata = socket.inet_aton(v4)
    elif qtype == QTYPE_AAAA:
        rdata = socket.inet_pton(socket.AF_INET6, v6)

    question = query[12:]
    if rdata is None:
        return tid + struct.pack("!HHHHH", flags, 1, 0, 0, 0) + question
    reply = tid + struct.pack("!HHHHH", flags, 1, 1, 0, 0) + question
    reply += b"\xc0\x0c" + struct.pack("!HHIH", qtype, QCLASS_IN, 60, len(rdata)) + rdata
    return reply


def main():
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
