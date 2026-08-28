'use strict';

// postinstall: download the multistream binary for this platform from the
// GitHub release, verify its SHA-256, and place it next to the CLI shim.

const fs = require('fs');
const path = require('path');
const crypto = require('crypto');
const https = require('https');
const { URL } = require('url');

const pkg = require(path.join(__dirname, 'package.json'));
const key = `${process.platform}-${process.arch}`;
const spec = (pkg.binaries || {})[key];
const binName = process.platform === 'win32' ? 'multistream.exe' : 'multistream';
const binPath = path.join(__dirname, 'bin', binName);

function fetchBuffer(url) {
  return new Promise((resolve, reject) => {
    const req = https.get(new URL(url), (res) => {
      if ([301, 302, 307, 308].includes(res.statusCode) && res.headers.location) {
        res.resume();
        return fetchBuffer(new URL(res.headers.location, url).toString()).then(resolve, reject);
      }
      if (res.statusCode !== 200) {
        res.resume();
        return reject(new Error(`HTTP ${res.statusCode} for ${url}`));
      }
      const chunks = [];
      res.on('data', (c) => chunks.push(c));
      res.on('end', () => resolve(Buffer.concat(chunks)));
      res.on('error', reject);
    });
    req.on('error', reject);
    req.setTimeout(120000, () => req.destroy(new Error('download timed out')));
  });
}

(async () => {
  if (!spec) {
    console.warn(`[multistream] no prebuilt binary for ${key}; install manually from the GitHub release`);
    return;
  }
  try {
    console.log(`[multistream] downloading v${pkg.version} (${key}) ...`);
    const data = await fetchBuffer(spec.url);
    const sha256 = crypto.createHash('sha256').update(data).digest('hex');
    if (sha256 !== spec.sha256) {
      throw new Error(`sha256 mismatch: got ${sha256}, want ${spec.sha256}`);
    }
    fs.mkdirSync(path.dirname(binPath), { recursive: true });
    fs.writeFileSync(binPath, data);
    fs.chmodSync(binPath, 0o755);
    console.log(`[multistream] installed ${binPath}`);
  } catch (err) {
    console.error(`[multistream] postinstall failed: ${err.message}`);
    process.exit(1);
  }
})();
