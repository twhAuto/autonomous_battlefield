#!/usr/bin/env python3
"""Use installed Helm or fetch a checksum-pinned temporary Linux amd64 binary."""
import hashlib
import io
from pathlib import Path
import platform
import shutil
import sys
import tarfile
import urllib.request

HELM_VERSION = "3.19.0"
HELM_SHA256 = "a7f81ce08007091b86d8bd696eb4d86b8d0f2e1b9f6c714be62f82f96a594496"


def ensure_helm(directory):
    installed = shutil.which("helm")
    if installed:
        return installed
    if platform.system() != "Linux" or platform.machine() not in ("x86_64", "amd64"):
        raise RuntimeError("Install Helm on this platform; automatic bootstrap supports Linux amd64.")
    url = f"https://get.helm.sh/helm-v{HELM_VERSION}-linux-amd64.tar.gz"
    print(f"Helm is missing; downloading verified Helm {HELM_VERSION} temporarily...", file=sys.stderr)
    with urllib.request.urlopen(url, timeout=30) as response:
        archive = response.read()
    if hashlib.sha256(archive).hexdigest() != HELM_SHA256:
        raise RuntimeError("Helm checksum mismatch; refusing to run the downloaded binary.")
    binary = Path(directory) / "helm"
    # Extract only the expected binary, never archive paths or links.
    with tarfile.open(fileobj=io.BytesIO(archive), mode="r:gz") as package:
        member = package.getmember("linux-amd64/helm")
        if not member.isfile():
            raise RuntimeError("Helm archive does not contain a regular binary.")
        with package.extractfile(member) as source, binary.open("xb") as target:
            shutil.copyfileobj(source, target)
    binary.chmod(0o700)
    return str(binary)


if __name__ == "__main__":
    try:
        print(ensure_helm(sys.argv[1]))
    except (OSError, RuntimeError, tarfile.TarError, KeyError) as error:
        print(f"Helm bootstrap failed: {error}", file=sys.stderr)
        sys.exit(1)
