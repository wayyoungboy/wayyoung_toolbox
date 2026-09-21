import { spawnSync } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const modules = {
  backup: path.join(root, 'modules/backup'),
  ha500: path.join(root, 'modules/ha500'),
  media: path.join(root, 'modules/media'),
  music: path.join(root, 'modules/music'),
};
const version = '0.6.1';
const stage = path.join(root, 'dist/stage/wayyoung_toolbox');
const output = path.join(root, `dist/wayyoung_toolbox_${version}_x86_64.fpk`);

function run(command, args, options = {}) {
  const result = spawnSync(command, args, { stdio: 'inherit', ...options });
  if (result.error) throw result.error;
  if (result.status !== 0) throw new Error(`${command} failed with exit code ${result.status}`);
}

function copy(source, destination) {
  if (!fs.existsSync(source)) throw new Error(`required source is missing: ${source}`);
  fs.mkdirSync(path.dirname(destination), { recursive: true });
  fs.cpSync(source, destination, { recursive: true, force: true });
}

function ensureNodeDependencies(directory) {
  if (!fs.existsSync(path.join(directory, 'node_modules'))) {
    run('npm', ['ci', '--no-audit', '--no-fund'], { cwd: directory });
  }
}

function buildFrontends() {
  const backupFrontend = path.join(modules.backup, 'frontend');
  ensureNodeDependencies(backupFrontend);
  run('npm', ['run', 'build'], { cwd: backupFrontend });

  for (const directory of [modules.media, modules.music]) {
    ensureNodeDependencies(directory);
    run('npm', ['run', 'build:frontend'], { cwd: directory });
  }
}

function replacePrefix(directory, from, to) {
  const textual = new Set(['.html', '.js', '.css', '.json', '.map', '.mjs']);
  const visit = current => {
    for (const entry of fs.readdirSync(current, { withFileTypes: true })) {
      const item = path.join(current, entry.name);
      if (entry.isDirectory()) visit(item);
      else if (entry.isFile() && textual.has(path.extname(entry.name))) {
        const before = fs.readFileSync(item, 'utf8');
        const after = before.split(from).join(to);
        if (after !== before) fs.writeFileSync(item, after);
      }
    }
  };
  visit(directory);
}

function stageJavsp() {
  const source = path.join(modules.media, 'third_party/javsp');
  const destination = path.join(stage, 'app/modules/media/javsp');
  copy(path.join(modules.media, 'resources/javsp/config.yml'), path.join(destination, 'runtime/config.yml'));
  copy(path.join(source, 'data'), path.join(destination, 'runtime/data'));
  copy(path.join(source, 'image'), path.join(destination, 'runtime/image'));
  for (const file of ['LICENSE', 'README.md', 'THIRD_PARTY_NOTICES.md']) {
    copy(path.join(source, file), path.join(destination, file));
  }
  fs.writeFileSync(
    path.join(destination, 'build-info.json'),
    `${JSON.stringify({ described: `wayyoung-toolbox-${version}`, mode: 'embedded-source' }, null, 2)}\n`,
  );
}

function stageLicenses() {
  const destination = path.join(stage, 'app/licenses');
  const licenses = [
    ['wayyoung-toolbox/LICENSE', path.join(root, 'LICENSE')],
    ['wayyoung-toolbox/THIRD_PARTY_NOTICES.md', path.join(root, 'THIRD_PARTY_NOTICES.md')],
    ['backup/LICENSE', path.join(modules.backup, 'LICENSE')],
    ['backup/THIRD_PARTY_NOTICES.md', path.join(modules.backup, 'THIRD_PARTY_NOTICES.md')],
    ['media/LICENSE', path.join(modules.media, 'LICENSE')],
    ['javsp/LICENSE', path.join(modules.media, 'third_party/javsp/LICENSE')],
    ['javsp/THIRD_PARTY_NOTICES.md', path.join(modules.media, 'third_party/javsp/THIRD_PARTY_NOTICES.md')],
    ['music/LICENSE', path.join(modules.music, 'LICENSE')],
    ['music/THIRD_PARTY_NOTICES.md', path.join(modules.music, 'THIRD_PARTY_NOTICES.md')],
    ['ncmdump-go/LICENSE', path.join(modules.music, 'third_party/ncmdump-go/LICENSE')],
    ['ha500/LICENSE', path.join(modules.ha500, 'LICENSE')],
  ];
  for (const [relative, source] of licenses) copy(source, path.join(destination, relative));
}

buildFrontends();
fs.rmSync(path.join(root, 'dist/stage'), { recursive: true, force: true });
fs.mkdirSync(stage, { recursive: true });
copy(path.join(root, 'packaging/wayyoung_toolbox'), stage);
for (const file of ['index.html', 'app.js', 'shell.js', 'api.js', 'overview.js', 'styles.css', 'embedded.css', 'directory-picker.js']) {
  copy(path.join(root, 'web', file), path.join(stage, 'app/public', file));
}
copy(path.join(modules.backup, 'frontend/src/directory-authorization.js'), path.join(stage, 'app/public/directory-authorization.js'));
copy(path.join(modules.backup, 'frontend/node_modules/@trimjs/web-app/dist/index.js'), path.join(stage, 'app/public/vendor/trimjs-web-app.js'));
copy(path.join(root, 'web/integrity'), path.join(stage, 'app/modules/integrity/public'));

const supervisorBinary = path.join(stage, 'app/bin/wayyoung-toolbox');
fs.mkdirSync(path.dirname(supervisorBinary), { recursive: true });
run('go', ['build', '-trimpath', '-ldflags', `-s -w -X main.version=${version}`, '-o', supervisorBinary, './cmd/wayyoung-toolbox'], {
  cwd: root,
  env: { ...process.env, GOTOOLCHAIN: 'go1.25.13', CGO_ENABLED: '0', GOOS: 'linux', GOARCH: 'amd64' },
});

copy(path.join(modules.backup, 'frontend/dist'), path.join(stage, 'app/modules/backup/public'));
copy(path.join(modules.media, 'frontend/dist'), path.join(stage, 'app/modules/media/public'));
stageJavsp();
copy(path.join(modules.music, 'frontend/dist'), path.join(stage, 'app/modules/music/public'));

const unified = '/app/wayyoung_toolbox/modules';
replacePrefix(path.join(stage, 'app/modules/backup/public'), '/app/backup_center', `${unified}/backup`);
replacePrefix(path.join(stage, 'app/modules/media/public'), '/app/media_acquisition_center', `${unified}/media`);
replacePrefix(path.join(stage, 'app/modules/music/public'), '/app/music_converter', `${unified}/music`);

const ha500Assets = path.join(modules.ha500, 'assets');
copy(path.join(ha500Assets, 'icon_64.png'), path.join(stage, 'ICON.PNG'));
copy(path.join(ha500Assets, 'icon_256.png'), path.join(stage, 'ICON_256.PNG'));
copy(path.join(ha500Assets, 'icon_64.png'), path.join(stage, 'app/ui/images/icon_64.png'));
copy(path.join(ha500Assets, 'icon_256.png'), path.join(stage, 'app/ui/images/icon_256.png'));
stageLicenses();

for (const file of fs.readdirSync(path.join(stage, 'cmd'))) fs.chmodSync(path.join(stage, 'cmd', file), 0o755);
fs.chmodSync(supervisorBinary, 0o755);

for (const [id, childPrefix] of Object.entries({ backup: '/app/backup_center', media: '/app/media_acquisition_center', music: '/app/music_converter' })) {
  const publicDir = path.join(stage, `app/modules/${id}/public`);
  for (const entry of fs.readdirSync(publicDir, { recursive: true })) {
    const item = path.join(publicDir, entry);
    if (!fs.statSync(item).isFile()) continue;
    const extension = path.extname(item);
    if (!['.html', '.js', '.css', '.json', '.map', '.mjs'].includes(extension)) continue;
    if (fs.readFileSync(item, 'utf8').includes(childPrefix)) throw new Error(`child frontend prefix remains in ${item}`);
  }
}

fs.mkdirSync(path.dirname(output), { recursive: true });
if (process.env.SKIP_FNPACK === '1') {
  console.log(`Staged ${stage}`);
  process.exit(0);
}
fs.rmSync(output, { force: true });
const temporaryOutput = path.join(root, 'dist/wayyoung_toolbox.fpk');
fs.rmSync(temporaryOutput, { force: true });
run(process.env.FNPACK || 'fnpack', ['build', '--directory', stage], { cwd: path.join(root, 'dist') });
if (!fs.existsSync(temporaryOutput)) throw new Error('fnpack did not produce wayyoung_toolbox.fpk');
fs.renameSync(temporaryOutput, output);
console.log(`Built ${output}`);
