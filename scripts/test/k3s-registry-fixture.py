#!/usr/bin/env python3
"""Disposable Basic-auth/TLS registry serving one checksum-verified BusyBox image."""
import base64
import hashlib
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
from pathlib import Path
import ssl
import subprocess
import threading
import urllib.request

DIGEST = 'sha256:dc88b80842580654294472735332ec7c7789a4010ec9a68b69f9b88c19abede6'


class Registry:
    def __init__(self, directory, address):
        self.directory = Path(directory)
        token_url = 'https://auth.docker.io/token?service=registry.docker.io&scope=repository:library/busybox:pull'
        with urllib.request.urlopen(token_url, timeout=30) as response:
            token = json.load(response)['token']
        self.blobs = {}
        def fetch(kind, digest):
            request = urllib.request.Request('https://registry-1.docker.io/v2/library/busybox/' + kind + '/' + digest,
                headers={'Authorization': 'Bearer ' + token, 'Accept': 'application/vnd.oci.image.manifest.v1+json'})
            with urllib.request.urlopen(request, timeout=60) as response:
                data = response.read()
            if hashlib.sha256(data).hexdigest() != digest.split(':')[1]:
                raise ValueError('Registry fixture source checksum mismatch')
            return data
        self.manifest = fetch('manifests', DIGEST)
        info = json.loads(self.manifest)
        for blob in [info['config'], *info['layers']]:
            self.blobs[blob['digest']] = fetch('blobs', blob['digest'])
        self.username, self.password = 'rehearsal', 'synthetic-registry-only'
        self.reject_auth = False
        auth = 'Basic ' + base64.b64encode((self.username + ':' + self.password).encode()).decode()
        registry = self
        class Handler(BaseHTTPRequestHandler):
            def do_HEAD(self):
                self.respond(False)
            def do_GET(self):
                self.respond(True)
            def log_message(self, *_args):
                pass
            def respond(self, body):
                if registry.reject_auth or self.headers.get('Authorization') != auth:
                    self.send_response(401)
                    self.send_header('WWW-Authenticate', 'Basic realm="rehearsal"')
                    self.end_headers()
                    return
                path = self.path.split('?')[0]
                content, kind = None, 'application/octet-stream'
                if path == '/v2/':
                    content, kind = b'{}', 'application/json'
                elif path == '/v2/probe/manifests/' + DIGEST:
                    content, kind = registry.manifest, 'application/vnd.oci.image.manifest.v1+json'
                elif path.startswith('/v2/probe/blobs/'):
                    content = registry.blobs.get(path.rsplit('/', 1)[1])
                if content is None:
                    self.send_response(404)
                    self.end_headers()
                    return
                self.send_response(200)
                self.send_header('Docker-Distribution-Api-Version', 'registry/2.0')
                self.send_header('Content-Type', kind)
                self.send_header('Content-Length', str(len(content)))
                self.send_header('Docker-Content-Digest', 'sha256:' + hashlib.sha256(content).hexdigest())
                self.end_headers()
                if body:
                    self.wfile.write(content)
        self.key, self.cert = self.directory / 'registry.key', self.directory / 'registry.crt'
        subprocess.run(['openssl', 'req', '-x509', '-newkey', 'rsa:2048', '-nodes', '-days', '2',
            '-subj', '/CN=Disposable K3s registry', '-addext', 'subjectAltName=IP:' + address,
            '-addext', 'basicConstraints=critical,CA:TRUE', '-keyout', str(self.key), '-out', str(self.cert)],
            check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        self.server = ThreadingHTTPServer((address, 0), Handler)
        context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
        context.load_cert_chain(str(self.cert), str(self.key))
        self.server.socket = context.wrap_socket(self.server.socket, server_side=True)
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        self.thread.start()
        self.image = address + ':' + str(self.server.server_port) + '/probe@' + DIGEST

    def close(self):
        self.server.shutdown()
        self.server.server_close()
        self.thread.join()
