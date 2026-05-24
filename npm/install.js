const https = require("https");
const http = require("http");
const fs = require("fs");
const path = require("path");
const { execSync } = require("child_process");

const pkg = require("./package.json");
const version = pkg.version;

const PLATFORM_MAP = {
  darwin: "darwin",
  linux: "linux",
  win32: "windows",
};

const ARCH_MAP = {
  x64: "amd64",
  arm64: "arm64",
};

function getPlatformArch() {
  const os = PLATFORM_MAP[process.platform];
  const arch = ARCH_MAP[process.arch];
  if (!os) {
    throw new Error(`Unsupported platform: ${process.platform}`);
  }
  if (!arch) {
    throw new Error(`Unsupported architecture: ${process.arch}`);
  }
  return { os, arch };
}

function downloadUrl() {
  const { os, arch } = getPlatformArch();
  const ext = os === "windows" ? "zip" : "tar.gz";
  return `https://github.com/smm-h/pgdesign/releases/download/v${version}/pgdesign_${version}_${os}_${arch}.${ext}`;
}

function follow(url, redirects) {
  if (redirects > 5) {
    return Promise.reject(new Error("Too many redirects"));
  }
  return new Promise((resolve, reject) => {
    const proto = url.startsWith("https") ? https : http;
    proto.get(url, { headers: { "User-Agent": "pgdesign-npm" } }, (res) => {
      if (res.statusCode >= 300 && res.statusCode < 400 && res.headers.location) {
        resolve(follow(res.headers.location, redirects + 1));
        return;
      }
      if (res.statusCode !== 200) {
        reject(new Error(`Download failed: HTTP ${res.statusCode} from ${url}`));
        return;
      }
      resolve(res);
    }).on("error", reject);
  });
}

async function download(url, dest) {
  const res = await follow(url, 0);
  return new Promise((resolve, reject) => {
    const file = fs.createWriteStream(dest);
    res.pipe(file);
    file.on("finish", () => file.close(resolve));
    file.on("error", reject);
  });
}

function extract(archive, destDir) {
  const isZip = archive.endsWith(".zip");
  if (isZip) {
    if (process.platform === "win32") {
      execSync(
        `powershell -Command "Expand-Archive -Path '${archive}' -DestinationPath '${destDir}' -Force"`,
      );
    } else {
      execSync(`unzip -o "${archive}" -d "${destDir}"`);
    }
  } else {
    execSync(`tar xzf "${archive}" -C "${destDir}"`);
  }
}

function findBinary(dir) {
  const name = process.platform === "win32" ? "pgdesign.exe" : "pgdesign";
  // Check top-level first
  const top = path.join(dir, name);
  if (fs.existsSync(top)) return top;
  // Check subdirectories
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    if (entry.isDirectory()) {
      const nested = path.join(dir, entry.name, name);
      if (fs.existsSync(nested)) return nested;
    }
  }
  throw new Error(`Could not find ${name} in extracted archive`);
}

async function main() {
  const url = downloadUrl();
  const isZip = url.endsWith(".zip");
  const archiveName = `pgdesign-download${isZip ? ".zip" : ".tar.gz"}`;
  const archivePath = path.join(__dirname, archiveName);
  const extractDir = path.join(__dirname, "pgdesign-extract");
  const binaryName = process.platform === "win32" ? "pgdesign.exe" : "pgdesign";
  const binaryDest = path.join(__dirname, binaryName);

  console.log(`Downloading pgdesign v${version}...`);
  console.log(`  ${url}`);

  await download(url, archivePath);

  fs.mkdirSync(extractDir, { recursive: true });
  extract(archivePath, extractDir);

  const binarySrc = findBinary(extractDir);
  fs.copyFileSync(binarySrc, binaryDest);

  if (process.platform !== "win32") {
    fs.chmodSync(binaryDest, 0o755);
  }

  // Clean up
  fs.unlinkSync(archivePath);
  fs.rmSync(extractDir, { recursive: true, force: true });

  console.log(`pgdesign v${version} installed successfully.`);
}

main().catch((err) => {
  console.error(`Failed to install pgdesign: ${err.message}`);
  process.exit(1);
});
