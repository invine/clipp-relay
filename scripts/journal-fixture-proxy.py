#!/usr/bin/env python3
"""Local TLS CONNECT fixture for disposable smoke tests only.

It serves one prequalified journal head and refuses all writes. Production
transport remains the region-derived OCI HTTPS endpoint.
"""

import json
import socketserver
import ssl
import sys

cert, key, port_file = sys.argv[1:]
host = "objectstorage.us-ashburn-1.oraclecloud.com"
zero = "0" * 64
body = json.dumps(
    {
        "repository_id": "local-smoke-repository",
        "coverage_floor": 0,
        "coverage_hash": zero,
        "sequence": 0,
        "hash": zero,
        "maintenance_generation": 0,
        "format": 1,
    },
    separators=(",", ":"),
).encode()
tls = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
tls.load_cert_chain(cert, key)


def header(sock):
    data = bytearray()
    while b"\r\n\r\n" not in data and len(data) < 16384:
        part = sock.recv(1)
        if not part:
            break
        data.extend(part)
    return bytes(data)


class Handler(socketserver.BaseRequestHandler):
    def handle(self):
        first = header(self.request)
        if not first.startswith(f"CONNECT {host}:443 HTTP/1.".encode()):
            return
        self.request.sendall(b"HTTP/1.1 200 Connection Established\r\n\r\n")
        with tls.wrap_socket(self.request, server_side=True) as stream:
            request = header(stream)
            line = request.split(b"\r\n", 1)[0]
            if line not in (
                b"GET /n/fixture/b/events/o/journal%2Fhead.json HTTP/1.1",
                b"GET /n/fixture/b/events/o/journal/head.json HTTP/1.1",
            ):
                stream.sendall(b"HTTP/1.1 403 Forbidden\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
                return
            stream.sendall(
                b"HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nETag: local-head-1\r\nContent-Length: "
                + str(len(body)).encode()
                + b"\r\nConnection: close\r\n\r\n"
                + body
            )


class Server(socketserver.ThreadingTCPServer):
    allow_reuse_address = True
    daemon_threads = True


with Server(("127.0.0.1", 0), Handler) as server:
    with open(port_file, "w", encoding="ascii") as output:
        output.write(str(server.server_address[1]))
    server.serve_forever()
