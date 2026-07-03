const assert = require("assert");
const fs = require("fs");
const path = require("path");
const test = require("node:test");
const vm = require("vm");

function loadInstallerForTest() {
  const installPath = path.join(__dirname, "install.js");
  const source = fs.readFileSync(installPath, "utf8");
  const testSource = source.replace(
    /main\(\)\.catch\([\s\S]*$/,
    "module.exports = { downloadWithFallback };"
  );
  const context = {
    __dirname,
    __filename: installPath,
    console,
    module: { exports: {} },
    process,
    require,
  };
  vm.runInNewContext(testSource, context, { filename: installPath });
  return context.module.exports;
}

test("downloadWithFallback uses the system downloader when the primary downloader fails", async () => {
  const { downloadWithFallback } = loadInstallerForTest();
  let fallbackURL = "";

  const result = await downloadWithFallback(["https://example.invalid/asset.zip"], {
    primaryDownload: async () => {
      throw new Error("connect ETIMEDOUT");
    },
    systemDownload: async (url) => {
      fallbackURL = url;
      return Buffer.from("ok");
    },
  });

  assert.equal(fallbackURL, "https://example.invalid/asset.zip");
  assert.equal(result.toString(), "ok");
});
