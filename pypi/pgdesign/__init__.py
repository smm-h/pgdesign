"""Thin PyPI wrapper that lazy-downloads the pgdesign Go binary on first invocation."""

import os
import platform
import stat
import subprocess
import sys
import tarfile
import zipfile

try:
    from importlib.metadata import version as _pkg_version
except ImportError:
    _pkg_version = None

__version__ = "0.16.1"


def _get_version():
    """Return the installed package version, falling back to __version__."""
    if _pkg_version is not None:
        try:
            return _pkg_version("pgdesign")
        except Exception:
            pass
    return __version__


_BIN_DIR = os.path.join(os.path.dirname(__file__), "_bin")


def main():
    binary = _ensure_binary()
    result = subprocess.run([binary] + sys.argv[1:])
    sys.exit(result.returncode)


def _ensure_binary():
    name = "pgdesign.exe" if _detect_os() == "windows" else "pgdesign"
    path = os.path.join(_BIN_DIR, name)
    if not os.path.isfile(path):
        _download_binary(path)
    return path


def _download_binary(dest):
    import urllib.request
    import tempfile

    ver = _get_version()
    os_name = _detect_os()
    arch = _detect_arch()
    ext = "zip" if os_name == "windows" else "tar.gz"
    url = (
        f"https://github.com/smm-h/pgdesign/releases/download/"
        f"v{ver}/pgdesign_{ver}_{os_name}_{arch}.{ext}"
    )

    os.makedirs(_BIN_DIR, exist_ok=True)

    with tempfile.TemporaryDirectory() as tmp:
        archive_path = os.path.join(tmp, f"pgdesign.{ext}")

        print(f"Downloading pgdesign v{ver} for {os_name}/{arch}...")
        urllib.request.urlretrieve(url, archive_path)

        if ext == "zip":
            with zipfile.ZipFile(archive_path, "r") as zf:
                zf.extractall(tmp)
        else:
            with tarfile.open(archive_path, "r:gz") as tf:
                tf.extractall(tmp)

        # Find the binary in extracted files
        binary_name = "pgdesign.exe" if os_name == "windows" else "pgdesign"
        extracted = None
        for root, _dirs, files in os.walk(tmp):
            if binary_name in files:
                extracted = os.path.join(root, binary_name)
                break

        if extracted is None:
            raise RuntimeError(
                f"Could not find {binary_name} in downloaded archive from {url}"
            )

        # Move binary to cache directory
        with open(extracted, "rb") as src, open(dest, "wb") as dst:
            dst.write(src.read())

        # Make executable on non-Windows
        if os_name != "windows":
            os.chmod(dest, os.stat(dest).st_mode | stat.S_IXUSR | stat.S_IXGRP | stat.S_IXOTH)

    print(f"pgdesign v{ver} installed to {dest}")


def _detect_os():
    system = platform.system().lower()
    mapping = {
        "linux": "linux",
        "darwin": "darwin",
        "windows": "windows",
    }
    if system not in mapping:
        raise RuntimeError(f"Unsupported operating system: {system}")
    return mapping[system]


def _detect_arch():
    machine = platform.machine().lower()
    mapping = {
        "x86_64": "amd64",
        "amd64": "amd64",
        "aarch64": "arm64",
        "arm64": "arm64",
    }
    if machine not in mapping:
        raise RuntimeError(f"Unsupported architecture: {machine}")
    return mapping[machine]
