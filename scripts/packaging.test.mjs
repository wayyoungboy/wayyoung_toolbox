import { test } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import { spawnSync } from 'node:child_process';

test('toolbox opts into the fnOS micro-app host required by its native directory SDK', () => {
  const manifest = fs.readFileSync(new URL('../packaging/wayyoung_toolbox/manifest', import.meta.url), 'utf8');
  assert.match(manifest, /^micro_app\s*=\s*true\s*$/m, 'native SDK cannot initialize in an ordinary iframe');
  const minimum = manifest.match(/^os_min_version\s*=\s*(\S+)/m)?.[1].split('.').map(Number);
  assert.ok(minimum && (minimum[0] > 1 || (minimum[0] === 1 && (minimum[1] > 2 || (minimum[1] === 2 && minimum[2] >= 401)))), 'shared directory API requires fnOS 1.2.0401 or later');
  const resource = JSON.parse(fs.readFileSync(new URL('../packaging/wayyoung_toolbox/config/resource', import.meta.url), 'utf8'));
  assert.ok(resource['api-scope'].includes('trim.file.sharedAccess'));
});

test('stop timeout must preserve the pid marker and never force-kill unfinished cleanup', () => {
  const script = new URL('../packaging/wayyoung_toolbox/cmd/main', import.meta.url).pathname;
  const result = spawnSync('bash', ['-c', `
    export TRIM_APPDEST=/test-app TRIM_PKGVAR=/test-data
    source "$1" status
    read_pid() { echo 99999; }
    owns_process() { return 0; }
    kill() { echo "signal:$1"; }
    sleep() { :; }
    rm() { echo "removed-pid"; }
    stop_app
  `, 'test', script], {encoding:'utf8'});
  assert.equal(result.status, 1, result.stdout + result.stderr);
  assert.match(result.stdout, /signal:-TERM/);
  assert.doesNotMatch(result.stdout, /signal:-KILL|removed-pid/);
});

test('browser decoder licenses are present and staged with the combined binary',()=>{
 const build=fs.readFileSync(new URL('./build.mjs',import.meta.url),'utf8');
 for(const decoder of ['xz','lz4','compress']){
  const relative=`modules/media/licenses/browser-decoders/${decoder}.LICENSE`;
  const license=fs.readFileSync(new URL(`../${relative}`,import.meta.url),'utf8');
  assert.match(license,/Redistribution and use in source and binary forms/);
  assert.ok(build.includes(`browser-decoders/${decoder}.LICENSE`),`missing staging for ${decoder}`);
 }
});
