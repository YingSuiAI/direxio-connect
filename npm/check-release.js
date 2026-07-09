#!/usr/bin/env node

"use strict";

const https = require("https");
const pkg = require("./package.json");

const repo = "YingSuiAI/dirextalk-connect";
const version = `v${pkg.version}`;
const prefix = `dirextalk-connect-${version}`;
const expectedAssets = [
  "checksums.txt",
  `${prefix}-darwin-amd64`,
  `${prefix}-darwin-amd64.tar.gz`,
  `${prefix}-darwin-arm64`,
  `${prefix}-darwin-arm64.tar.gz`,
  `${prefix}-linux-amd64`,
  `${prefix}-linux-amd64.tar.gz`,
  `${prefix}-linux-arm64`,
  `${prefix}-linux-arm64.tar.gz`,
  `${prefix}-windows-amd64.exe`,
  `${prefix}-windows-amd64.zip`,
  `${prefix}-windows-arm64.exe`,
  `${prefix}-windows-arm64.zip`,
];

function requestJson(url) {
  return new Promise((resolve, reject) => {
    const req = https.get(
      url,
      {
        headers: {
          "Accept": "application/vnd.github+json",
          "User-Agent": "dirextalk-connect-release-check",
        },
      },
      (res) => {
        const chunks = [];
        res.on("data", (chunk) => chunks.push(chunk));
        res.on("end", () => {
          const body = Buffer.concat(chunks).toString("utf8");
          if (res.statusCode !== 200) {
            reject(new Error(`GitHub returned HTTP ${res.statusCode}: ${body.slice(0, 200)}`));
            return;
          }
          try {
            resolve(JSON.parse(body));
          } catch (error) {
            reject(new Error(`GitHub returned invalid JSON: ${error.message}`));
          }
        });
      }
    );
    req.on("error", reject);
    req.setTimeout(30_000, () => req.destroy(new Error("GitHub release check timed out")));
  });
}

async function main() {
  const release = await requestJson(`https://api.github.com/repos/${repo}/releases/tags/${version}`);
  const assetNames = new Set((release.assets || []).map((asset) => asset.name));
  const missing = expectedAssets.filter((asset) => !assetNames.has(asset));
  if (missing.length > 0) {
    throw new Error(
      `Refusing to publish ${pkg.name}@${pkg.version}: GitHub release ${version} is missing assets:\n` +
        missing.map((asset) => `  - ${asset}`).join("\n") +
        `\nCreate the tag/release and upload dist assets before npm publish.`
    );
  }
  console.log(`GitHub release ${version} has all ${expectedAssets.length} expected assets.`);
}

main().catch((error) => {
  console.error(error.message);
  process.exit(1);
});
