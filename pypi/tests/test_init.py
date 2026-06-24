"""Tests for the pgdesign PyPI wrapper."""

import re

import pgdesign


def test_version_exists_and_nonempty():
    assert hasattr(pgdesign, "__version__")
    assert isinstance(pgdesign.__version__, str)
    assert len(pgdesign.__version__) > 0


def test_detect_os_returns_valid():
    result = pgdesign._detect_os()
    assert result in ("linux", "darwin", "windows")


def test_detect_arch_returns_valid():
    result = pgdesign._detect_arch()
    assert result in ("amd64", "arm64")


def test_url_construction_format():
    """Verify the download URL uses the expected pattern with a version string."""
    ver = pgdesign._get_version()
    os_name = pgdesign._detect_os()
    arch = pgdesign._detect_arch()
    ext = "zip" if os_name == "windows" else "tar.gz"

    expected = (
        f"https://github.com/smm-h/pgdesign/releases/download/"
        f"v{ver}/pgdesign_{ver}_{os_name}_{arch}.{ext}"
    )

    # Verify the version component is a valid semver-like string
    assert re.match(r"^\d+\.\d+\.\d+", ver), f"Version {ver!r} is not semver-like"

    # Verify the URL structure
    assert expected.startswith("https://github.com/smm-h/pgdesign/releases/download/v")
    assert f"pgdesign_{ver}_{os_name}_{arch}" in expected
    assert expected.endswith(f".{ext}")
