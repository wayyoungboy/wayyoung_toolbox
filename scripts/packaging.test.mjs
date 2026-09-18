import { test } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';

test('toolbox opts into the fnOS micro-app host required by its native directory SDK', () => {
  const manifest = fs.readFileSync(new URL('../packaging/wayyoung_toolbox/manifest', import.meta.url), 'utf8');
  assert.match(manifest, /^micro_app\s*=\s*true\s*$/m, 'native SDK cannot initialize in an ordinary iframe');
  const minimum = manifest.match(/^os_min_version\s*=\s*(\S+)/m)?.[1].split('.').map(Number);
  assert.ok(minimum && (minimum[0] > 1 || (minimum[0] === 1 && (minimum[1] > 2 || (minimum[1] === 2 && minimum[2] >= 401)))), 'shared directory API requires fnOS 1.2.0401 or later');
  const resource = JSON.parse(fs.readFileSync(new URL('../packaging/wayyoung_toolbox/config/resource', import.meta.url), 'utf8'));
  assert.ok(resource['api-scope'].includes('trim.file.sharedAccess'));
});
