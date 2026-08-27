#!/usr/bin/env node
'use strict';

// CLI shim: exec the platform binary downloaded by install.js.

const fs = require('fs');
const path = require('path');
const { spawnSync } = require('child_process');

const binName = process.platform === 'win32' ? 'multistream.exe' : 'multistream';
const binPath = path.join(__dirname, binName);

if (!fs.existsSync(binPath)) {
  console.error(`multistream: binary not found at ${binPath}`);
  console.error('multistream: re-run "npm rebuild @xlip/multistream" or install the binary manually');
  process.exit(2);
}

const result = spawnSync(binPath, process.argv.slice(2), { stdio: 'inherit' });
process.exit(result.status === null ? 1 : result.status);
