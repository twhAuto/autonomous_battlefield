#!/usr/bin/env python3
"""Check a local frontend image with production container permissions, without ports."""

import argparse
from html.parser import HTMLParser
import json
import signal
import subprocess
import sys
import time
from urllib.parse import urlsplit
import uuid

# The Content-Security-Policy every response must carry; connect-src also lists the
# image's CSP_API_ORIGIN when the build names a separate API.
SELF, NONE = "'self'", "'none'"
POLICY = {
    "default-src": [SELF],
    "script-src": [SELF],
    "style-src": [SELF],
    "font-src": [SELF, "data:"],
    "img-src": [SELF, "data:"],
    "connect-src": [SELF],
    "frame-ancestors": [NONE],
    "object-src": [NONE],
    "base-uri": [SELF],
    "form-action": [SELF],
}


class Assets(HTMLParser):
    def __init__(self):
        super().__init__()
        self.paths = set()
        self.has_script = False

    def handle_starttag(self, tag, attrs):
        attrs = dict(attrs)
        path = None
        if tag == "style" or (tag == "script" and not attrs.get("src")):
            raise ValueError("Frontend index must not contain inline scripts or styles, which the Content-Security-Policy blocks")
        if tag == "script":
            path = attrs["src"]
            self.has_script = True
        elif tag == "link" and {"stylesheet", "modulepreload"}.intersection(attrs.get("rel", "").split()):
            path = attrs.get("href")
        if path:
            url = urlsplit(path)
            if url.scheme or url.netloc or not url.path.startswith("/") or ".." in url.path.split("/"):
                raise ValueError("Frontend entry assets must use local absolute paths")
            self.paths.add(url.path)


def docker(*args, timeout=30, check=True):
    return subprocess.run(["docker", *args], capture_output=True, timeout=timeout, check=check)


def expected_policy(image):
    env = json.loads(docker("image", "inspect", "--format", "{{json .Config.Env}}", image).stdout)
    origin = next((item.split("=", 1)[1] for item in env if item.startswith("CSP_API_ORIGIN=")), "").strip()
    return {**POLICY, "connect-src": POLICY["connect-src"] + ([origin] if origin else [])}


def parse_policy(header):
    directives = {}
    for directive in filter(None, (part.strip() for part in header.split(";"))):
        name, *sources = directive.split()
        if name in directives:
            raise ValueError("Content-Security-Policy repeats " + name)
        directives[name] = sources
    return directives


def served_policy(name, path):
    # BusyBox wget -S prints the response headers to stderr.
    response = docker("exec", name, "wget", "-q", "-S", "-T", "2", "-O", "/dev/null",
                      "http://127.0.0.1:8080" + path, timeout=5).stderr.decode("utf-8", "replace")
    headers = [line.split(":", 1)[1].strip() for line in response.splitlines()
               if line.strip().lower().startswith("content-security-policy:")]
    if len(headers) != 1:
        raise RuntimeError("Expected one Content-Security-Policy header on " + path)
    return parse_policy(headers[0])


def check_policy(name, image, paths):
    required = expected_policy(image)
    for path in paths:
        served = served_policy(name, path)
        if served != required:
            raise RuntimeError("Unexpected Content-Security-Policy on " + path + ": " + json.dumps(served))


def verify(image):
    name = "battlefield-frontend-check-" + uuid.uuid4().hex
    try:
        docker(
            "run", "--detach", "--pull=never", "--name", name,
            "--network", "none", "--dns", "127.0.0.1", "--user", "101:101", "--read-only",
            "--cap-drop", "ALL", "--security-opt", "no-new-privileges=true",
            "--memory", "128m", "--cpus", "0.2", "--pids-limit", "64",
            "--tmpfs", "/etc/nginx/conf.d:rw,nosuid,nodev,noexec,uid=101,gid=101,mode=0755",
            "--tmpfs", "/var/cache/nginx:rw,nosuid,nodev,noexec,uid=101,gid=101,mode=0755",
            "--tmpfs", "/tmp:rw,nosuid,nodev,noexec,uid=101,gid=101,mode=1777",
            "--env", "API_UPSTREAM=127.0.0.1:9", image,
        )

        def get(path, check=True):
            return docker("exec", name, "wget", "-q", "-T", "2", "-O", "-",
                          "http://127.0.0.1:8080" + path, timeout=5, check=check)

        deadline = time.monotonic() + 30
        while True:
            running = docker("inspect", "--format", "{{.State.Running}}", name).stdout.strip()
            if running != b"true":
                raise RuntimeError("Frontend container exited during startup")
            if get("/startupz", check=False).returncode == 0:
                break
            if time.monotonic() >= deadline:
                raise RuntimeError("Frontend startup probe timed out")
            time.sleep(0.5)
        for path in ("/startupz", "/livez", "/readyz"):
            if get(path).stdout.strip() != b"ok":
                raise RuntimeError("Unexpected frontend probe response: " + path)
        index = docker("exec", name, "cat", "/usr/share/nginx/html/index.html").stdout
        if not index or get("/").stdout != index:
            raise RuntimeError("Frontend did not serve its built index")
        assets = Assets()
        assets.feed(index.decode("utf-8"))
        if not assets.has_script or not assets.paths:
            raise RuntimeError("Frontend index contains no built JavaScript entry point")
        for path in sorted(assets.paths):
            expected = docker("exec", name, "cat", "/usr/share/nginx/html" + path).stdout
            if not expected or get(path).stdout != expected:
                raise RuntimeError("Frontend did not serve built asset: " + path)
        check_policy(name, image, ["/", *sorted(assets.paths)])
        print("Frontend image passed startup, liveness, readiness, index, asset and Content-Security-Policy checks as UID 101.")
    except (subprocess.SubprocessError, OSError, RuntimeError, ValueError) as error:
        print("Frontend image verification failed: " + str(error), file=sys.stderr)
        if getattr(error, "stderr", None):
            sys.stderr.buffer.write(error.stderr[-4096:])
        logs = docker("logs", "--tail", "60", name, check=False)
        sys.stderr.buffer.write(logs.stdout + logs.stderr)
        raise
    finally:
        docker("rm", "--force", name, check=False)


def interrupted(signum, _frame):
    raise SystemExit(128 + signum)


def main():
    signal.signal(signal.SIGTERM, interrupted)
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("image", help="An already-pulled image ID, commit tag or immutable digest")
    args = parser.parse_args()
    if not args.image or args.image.startswith("-"):
        parser.error("Expected an image reference")
    try:
        verify(args.image)
    except KeyboardInterrupt:
        return 130
    except (subprocess.SubprocessError, OSError, RuntimeError, ValueError):
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
