import { mkdirSync } from 'node:fs';
import { homedir } from 'node:os';
import { join } from 'node:path';

// Directory names must be valid on every supported desktop platform. In
// particular, avoid the ':' commonly used in ISO timestamps because Windows
// does not permit it in a path component.
export function newSessionDirectoryTimestamp(now = new Date()): string {
  const part = (value: number) => String(value).padStart(2, '0');
  return `${now.getFullYear()}${part(now.getMonth() + 1)}${part(now.getDate())}_${part(now.getHours())}${part(now.getMinutes())}${part(now.getSeconds())}`;
}

export function newSessionDirectoryPath(
  home: string,
  now = new Date(),
  joinPath: (...parts: string[]) => string = join,
): string {
  return joinPath(home, 'MothX', `mothx_projects_${newSessionDirectoryTimestamp(now)}`);
}

export function defaultNewSessionDirectory(home = homedir(), now = new Date()): string {
  const directory = newSessionDirectoryPath(home, now);
  mkdirSync(directory, { recursive: true });
  return directory;
}

// A fresh Desktop process has one default for all new sessions until the user
// chooses another directory. Reusing it also keeps the ACP child startup cwd
// and the renderer's initial next-session default aligned.
let initialDirectory = '';

export function initialNewSessionDirectory(): string {
  if (!initialDirectory) initialDirectory = defaultNewSessionDirectory();
  return initialDirectory;
}
