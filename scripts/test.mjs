import { spawnSync } from 'node:child_process';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const requested = new Set(process.argv.slice(2));
const runGo = requested.size === 0 || requested.has('--go');
const runFrontend = requested.size === 0 || requested.has('--frontend');

function run(command, args, cwd) {
  const result = spawnSync(command, args, { cwd, stdio: 'inherit' });
  if (result.error) throw result.error;
  if (result.status !== 0) throw new Error(`${command} failed with exit code ${result.status} in ${cwd}`);
}

if (runGo) {
  const goModules = [
    { directory: root, packages: ['./...'] },
    { directory: path.join(root, 'modules/backup'), packages: ['./internal/...', './module/...'] },
    { directory: path.join(root, 'modules/media'), packages: ['./internal/...', './module/...'] },
    { directory: path.join(root, 'modules/media/third_party/javsp'), packages: ['./...'] },
    { directory: path.join(root, 'modules/music'), packages: ['./internal/...', './module/...'] },
    { directory: path.join(root, 'modules/music/third_party/ncmdump-go'), packages: ['./...'] },
    { directory: path.join(root, 'modules/ha500'), packages: ['./internal/...', './module/...'] },
  ];
  for (const project of goModules) {
    run('go', ['vet', ...project.packages], project.directory);
    run('go', ['test', '-race', ...project.packages], project.directory);
  }
}

if (runFrontend) {
  const projects = [
    { directory: path.join(root, 'modules/backup/frontend'), build: 'build', test: 'test' },
    { directory: path.join(root, 'modules/media'), build: 'build:frontend', test: 'test:frontend', lint: true },
    { directory: path.join(root, 'modules/music'), build: 'build:frontend', test: 'test:frontend' },
  ];
  for (const project of projects) {
    run('npm', ['ci', '--no-audit', '--no-fund'], project.directory);
    if (project.lint) run('npm', ['run', 'lint'], project.directory);
    run('npm', ['run', project.test], project.directory);
    run('npm', ['run', project.build], project.directory);
  }
}

if (!runGo && !runFrontend) {
  throw new Error('expected --go or --frontend');
}
