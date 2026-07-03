#!/usr/bin/env node

"use strict";

const { execFileSync, execSync } = require("child_process");
const fs = require("fs");
const os = require("os");
const path = require("path");
const https = require("https");
const http = require("http");
const zlib = require("zlib");

const PACKAGE = require("./package.json");
const VERSION = `v${PACKAGE.version}`;
const NAME = "dirextalk-connect";
const LOG_PREFIX = "dirextalk-connect";
const LOCAL_BINARY_ENV = "DIREXTALK_CONNECT_LOCAL_BINARY";

const GITHUB_REPO = "YingSuiAI/dirextalk-connect";

const PLATFORM_MAP = {
  darwin: "darwin",
  linux: "linux",
  win32: "windows",
};

const ARCH_MAP = {
  x64: "amd64",
  arm64: "arm64",
};

function getPlatformInfo() {
  const platform = PLATFORM_MAP[process.platform];
  const arch = ARCH_MAP[process.arch];
  if (!platform || !arch) {
    throw new Error(
      `Unsupported platform: ${process.platform}/${process.arch}. ` +
        `Supported: linux/darwin/windows x64/arm64`
    );
  }
  const ext = platform === "windows" ? ".zip" : ".tar.gz";
  const filename = `${NAME}-${VERSION}-${platform}-${arch}${ext}`;
  return { platform, arch, ext, filename };
}

function getDownloadURLs(filename) {
  return [
    `https://github.com/${GITHUB_REPO}/releases/download/${VERSION}/${filename}`,
  ];
}

function fetch(url, redirects = 5) {
  return new Promise((resolve, reject) => {
    if (redirects <= 0) return reject(new Error("Too many redirects"));
    const mod = url.startsWith("https") ? https : http;
    const req = mod
      .get(url, { headers: { "User-Agent": "dirextalk-connect-npm" } }, (res) => {
        if (res.statusCode >= 300 && res.statusCode < 400 && res.headers.location) {
          return resolve(fetch(res.headers.location, redirects - 1));
        }
        if (res.statusCode !== 200) {
          res.resume();
          return reject(new Error(`HTTP ${res.statusCode} for ${url}`));
        }
        const chunks = [];
        res.on("data", (c) => chunks.push(c));
        res.on("end", () => resolve(Buffer.concat(chunks)));
        res.on("error", reject);
      })
      .on("error", reject);
    req.setTimeout(30_000, () => {
      req.destroy(new Error(`timeout after 30s for ${url}`));
    });
  });
}

async function download(urls) {
  const maxAttempts = 3;
  for (const url of urls) {
    for (let attempt = 1; attempt <= maxAttempts; attempt++) {
      try {
        const suffix = attempt === 1 ? "" : ` (attempt ${attempt}/${maxAttempts})`;
        console.log(`[${LOG_PREFIX}] Downloading from ${url}${suffix}`);
        const data = await fetch(url);
        console.log(`[${LOG_PREFIX}] Downloaded ${(data.length / 1024 / 1024).toFixed(1)} MB`);
        return data;
      } catch (err) {
        const lastAttempt = attempt === maxAttempts;
        console.warn(
          `[${LOG_PREFIX}] Failed: ${err.message}` +
            (lastAttempt ? ", trying next source..." : ", retrying...")
        );
        if (!lastAttempt) {
          await new Promise((resolve) => setTimeout(resolve, 1000 * attempt));
        }
      }
    }
  }
  throw new Error(
    `[${LOG_PREFIX}] Could not download binary from any source.\n` +
      `  Tried: ${urls.join(", ")}\n` +
      `  You can download manually from https://github.com/${GITHUB_REPO}/releases`
  );
}

function psQuote(s) {
  return `'${s.replace(/'/g, "''")}'`;
}

async function downloadWithSystemTool(url) {
  const tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), "dirextalk-connect-download-"));
  const tmpFile = path.join(tmpDir, "asset");
  try {
    try {
      execFileSync("curl", [
        "-L",
        "--fail",
        "--retry",
        "3",
        "--connect-timeout",
        "20",
        "--max-time",
        "300",
        "-o",
        tmpFile,
        url,
      ], { stdio: "pipe", timeout: 330_000 });
      return fs.readFileSync(tmpFile);
    } catch (curlErr) {
      if (process.platform !== "win32") {
        throw curlErr;
      }
      const script =
        "$ProgressPreference = 'SilentlyContinue'; " +
        `Invoke-WebRequest -Uri ${psQuote(url)} -OutFile ${psQuote(tmpFile)} -MaximumRedirection 5`;
      execFileSync("powershell", [
        "-NoProfile",
        "-ExecutionPolicy",
        "Bypass",
        "-Command",
        script,
      ], { stdio: "pipe", timeout: 330_000 });
      return fs.readFileSync(tmpFile);
    }
  } finally {
    fs.rmSync(tmpDir, { recursive: true, force: true });
  }
}

async function downloadWithFallback(urls, opts = {}) {
  const primaryDownload = opts.primaryDownload || download;
  const systemDownload = opts.systemDownload || downloadWithSystemTool;
  try {
    return await primaryDownload(urls);
  } catch (primaryErr) {
    console.warn(`[${LOG_PREFIX}] Built-in downloader failed: ${primaryErr.message}`);
    for (const url of urls) {
      try {
        console.log(`[${LOG_PREFIX}] Trying system downloader for ${url}`);
        return await systemDownload(url);
      } catch (fallbackErr) {
        console.warn(`[${LOG_PREFIX}] System downloader failed: ${fallbackErr.message}`);
      }
    }
    throw primaryErr;
  }
}

function extractTarGz(buffer, destDir, binaryName) {
  const tmpFile = path.join(destDir, "_tmp.tar.gz");
  fs.writeFileSync(tmpFile, buffer);
  try {
    execSync(`tar xzf "${tmpFile}" -C "${destDir}"`, { stdio: "pipe" });
  } finally {
    fs.unlinkSync(tmpFile);
  }
  const extracted = fs.readdirSync(destDir).find((f) => f.startsWith(NAME) && !f.endsWith(".tar.gz"));
  if (extracted && extracted !== binaryName) {
    fs.renameSync(path.join(destDir, extracted), path.join(destDir, binaryName));
  }
}

function extractZip(buffer, destDir, binaryName) {
  const tmpFile = path.join(destDir, "_tmp.zip");
  fs.writeFileSync(tmpFile, buffer);
  try {
    try {
      execSync(`unzip -o "${tmpFile}" -d "${destDir}"`, { stdio: "pipe" });
    } catch {
      execSync(`powershell -Command "Expand-Archive -Force '${tmpFile}' '${destDir}'"`, {
        stdio: "pipe",
      });
    }
  } finally {
    try { fs.unlinkSync(tmpFile); } catch {}
  }
  const extracted = fs.readdirSync(destDir).find((f) => f.startsWith(NAME) && f.endsWith(".exe"));
  if (extracted && extracted !== binaryName) {
    fs.renameSync(path.join(destDir, extracted), path.join(destDir, binaryName));
  }
}

// parseVersion splits "1.2.3-beta.1" into { nums: [1,2,3], preTag: "beta", preNum: 1 }
function parseVersion(v) {
  v = v.replace(/^v/, "").trim();
  const [base, ...rest] = v.split("-");
  const nums = base.split(".").map(Number);
  const pre = rest.join("-");
  const m = pre.match(/^([a-zA-Z]+)\.?(\d+)?$/);
  return { nums, preTag: m ? m[1] : pre, preNum: m && m[2] ? parseInt(m[2], 10) : 0, hasPre: pre !== "" };
}

// isNewerOrEqual returns true if installed >= expected
function isNewerOrEqual(installed, expected) {
  const a = parseVersion(installed);
  const b = parseVersion(expected);
  const len = Math.max(a.nums.length, b.nums.length);
  for (let i = 0; i < len; i++) {
    const av = a.nums[i] || 0;
    const bv = b.nums[i] || 0;
    if (av > bv) return true;
    if (av < bv) return false;
  }
  if (!a.hasPre && b.hasPre) return true;
  if (a.hasPre && !b.hasPre) return false;
  if (!a.hasPre && !b.hasPre) return true;
  // Both pre-release: compare tag then number (rc > beta, beta.10 > beta.9)
  if (a.preTag !== b.preTag) return a.preTag > b.preTag;
  return a.preNum >= b.preNum;
}

async function main() {
  const { platform, arch, ext, filename } = getPlatformInfo();
  console.log(`[${LOG_PREFIX}] Platform: ${platform}/${arch}`);

  const binDir = path.join(__dirname, "bin");
  fs.mkdirSync(binDir, { recursive: true });

  const binaryName = platform === "windows" ? `${NAME}.exe` : NAME;
  const binaryPath = path.join(binDir, binaryName);
  const localBinary = (process.env[LOCAL_BINARY_ENV] || "").trim();
  if (localBinary) {
    if (!fs.existsSync(localBinary)) {
      throw new Error(`[${LOG_PREFIX}] ${LOCAL_BINARY_ENV} does not exist: ${localBinary}`);
    }
    fs.copyFileSync(localBinary, binaryPath);
    if (platform !== "windows") {
      fs.chmodSync(binaryPath, 0o755);
    }
    console.log(`[${LOG_PREFIX}] Installed local binary from ${localBinary}`);
    return;
  }

  if (fs.existsSync(binaryPath)) {
    try {
      const out = execSync(`"${binaryPath}" --version`, { encoding: "utf8", timeout: 5000 });
      const expectedVer = VERSION.slice(1); // remove leading "v"
      if (out.includes(expectedVer)) {
        console.log(`[${LOG_PREFIX}] Binary ${VERSION} already installed, skipping.`);
        return;
      }
      // Don't downgrade: if existing binary is newer, keep it
      const match = out.match(/(\d+\.\d+\.\d+[^\s]*)/);
      if (match && isNewerOrEqual(match[1], expectedVer)) {
        console.log(`[${LOG_PREFIX}] Binary ${match[1]} is newer than ${VERSION}, skipping.`);
        return;
      }
      console.log(`[${LOG_PREFIX}] Existing binary is outdated, upgrading to ${VERSION}...`);
      fs.unlinkSync(binaryPath);
    } catch {
      console.log(`[${LOG_PREFIX}] Replacing existing binary with ${VERSION}...`);
      fs.unlinkSync(binaryPath);
    }
  }

  const urls = getDownloadURLs(filename);
  const data = await downloadWithFallback(urls);

  if (ext === ".tar.gz") {
    extractTarGz(data, binDir, binaryName);
  } else {
    extractZip(data, binDir, binaryName);
  }

  if (platform !== "windows") {
    fs.chmodSync(binaryPath, 0o755);
  }

  if (platform === "darwin") {
    try {
      execSync(`xattr -d com.apple.quarantine "${binaryPath}"`, { stdio: "pipe" });
      console.log(`[${LOG_PREFIX}] Removed macOS quarantine attribute`);
    } catch {
      // xattr fails if the attribute doesn't exist, which is fine
    }
  }

  console.log(`[${LOG_PREFIX}] Installed to ${binaryPath}`);
}

main().catch((err) => {
  console.error(err.message);
  console.error(
    `[${LOG_PREFIX}] Installation failed. You can install manually:\n` +
      `  https://github.com/${GITHUB_REPO}/releases/tag/${VERSION}`
  );
  process.exit(1);
});
