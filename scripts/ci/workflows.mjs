import { execFileSync, spawnSync } from 'node:child_process';
import { readFileSync, readdirSync } from 'node:fs';
import path from 'node:path';
import yaml from 'js-yaml';

const directory = '.github/workflows';
{
  const paths = [];
  for (const name of readdirSync(directory).filter((name) => /\.ya?ml$/u.test(name)).sort()) {
    const file = path.join(directory, name);
    let source = readFileSync(file, 'utf8');
    const workflow = yaml.load(source);
    const queues = Object.entries(workflow.jobs ?? {}).filter(([, job]) => job.concurrency?.queue !== undefined);
    if (workflow.concurrency?.queue !== undefined || queues.length) {
      if (name !== 'release-linux-packages.yml' || workflow.concurrency?.queue !== undefined || queues.length !== 1 || queues[0][0] !== 'publish' || JSON.stringify(queues[0][1].concurrency) !== JSON.stringify({ group: 'linux-package-repository', 'cancel-in-progress': false, queue: 'max' })) throw new Error('Unsupported concurrency queue declaration');
      // actionlint 1.7.12 predates GitHub's queue:max. Validate this one exact
      // declaration above, then omit only that line from a temporary lint input.
      // Remove this compatibility adapter when the pinned actionlint accepts queue.
      const lines = source.split('\n');
      const queueLines = lines.filter((line) => /^\s+queue:/u.test(line));
      if (queueLines.length !== 1 || queueLines[0] !== '      queue: max') throw new Error('Unexpected queue syntax');
      source = lines.map((line) => line === '      queue: max' ? '      # queue:max validated by repository contract' : line).join('\n');
      const result = spawnSync('go', ['tool', 'actionlint', '-stdin-filename', file, '-'], { input: source, encoding: 'utf8' });
      process.stdout.write(result.stdout ?? ''); process.stderr.write(result.stderr ?? '');
      if (result.status !== 0) process.exitCode = 1;
    } else paths.push(file);
  }
  if (paths.length) execFileSync('go', ['tool', 'actionlint', ...paths], { stdio: 'inherit' });
}
