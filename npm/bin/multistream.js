#!/usr/bin/env node
'use strict';

// CLI shim: exec the platform binary downloaded by install.js, exposing the
// bundled runtime dir (ffmpeg + mediamtx) via $MULTISTREAM_RUNTIME_DIR.

const fs = require('fs');
const path = require('path');
const { spawnSync } = require('child_process');

const binName = process.platform === 'win32' ? 'multistream.exe' : 'multistream';
const binPath = path.join(__dirname, binName);

if (!fs.existsSync(binPath)) {
  console.error(`multistream: binary not found at ${binPath}`);
  console.error('multistream: re-run "npm rebuild @arifyaman/multistream" or install the binary manually');
  process.exit(2);
}

const env = { ...process.env };
const runtimeDir = path.join(__dirname, '..', 'vendor', 'runtime', 'bin');
const ffmpegName = process.platform === 'win32' ? 'ffmpeg.exe' : 'ffmpeg';
if (fs.existsSync(path.join(runtimeDir, ffmpegName))) {
  env.MULTISTREAM_RUNTIME_DIR = runtimeDir;
}

const result = spawnSync(binPath, process.argv.slice(2), { stdio: 'inherit', env });
process.exit(result.status === null ? 1 : result.status);
