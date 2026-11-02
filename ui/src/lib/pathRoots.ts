export type PathRootMode = 'local' | 'windows' | 'wsl';

export interface PathCrumb {
  label: string;
  path: string;
}

interface ParsedPath {
  /** Comparison key: forward slashes, no trailing separator, Windows lowercased. */
  key: string;
  /** Original-cased segments, without empty or `.` parts. */
  segments: string[];
  /** Windows drive letter, if the path is a drive path. */
  drive?: string;
}

const MANAGED_WSL_DISTRO = 'devstack';

function parseSegments(value: string, allowColon = false, separators: RegExp = /[\\/]+/): string[] | null {
  const parts = value.split(separators);
  const segments: string[] = [];
  for (const part of parts) {
    if (!part || part === '.') continue;
    if (part === '..') return null;
    // Windows rejects alternate data streams, drive letters and device syntax
    // in an individual path component. Linux (local and WSL) allows ':' in a
    // directory name, so only reject NUL there.
    if (part.includes('\0') || (!allowColon && part.includes(':'))) return null;
    segments.push(part);
  }
  return segments;
}

function parsePath(value: string, mode: PathRootMode): ParsedPath | null {
  const input = value.trim();
  if (!input || input.includes('\0')) return null;

  if (mode === 'wsl') {
    const unc = /^\\\\wsl(?:\.localhost|\$)\\([^\\/]+)(?:[\\/](.*))?$/i.exec(input);
    if (unc) {
      // Only the managed devstack distro is a valid WSL namespace. Other
      // distros are rejected even when their Linux path looks similar.
      if (unc[1].toLowerCase() !== MANAGED_WSL_DISTRO) return null;
      const segments = parseSegments(unc[2] ?? '', true);
      if (!segments) return null;
      return { key: `/${segments.join('/')}`, segments };
    }
    if (!input.startsWith('/')) return null;
    // A POSIX WSL path cannot carry a literal backslash: the generated Windows
    // UNC host path would treat it as a separator and diverge from the Linux
    // backend path. The server rejects the same shape.
    if (input.includes('\\')) return null;
    const segments = parseSegments(input.slice(1), true, /\//);
    if (!segments) return null;
    return { key: `/${segments.join('/')}`, segments };
  }

  if (mode === 'windows') {
    const normalized = input.replace(/\//g, '\\');
    const drive = /^([A-Za-z]):(?:\\|$)/.exec(normalized);
    if (!drive) return null;
    const segments = parseSegments(normalized.slice(2));
    if (!segments) return null;
    return {
      key: `${drive[1].toLowerCase()}:/${segments.map((part) => part.toLowerCase()).join('/')}`,
      segments,
      drive: drive[1].toUpperCase(),
    };
  }

  if (!input.startsWith('/')) return null;
  // On Linux a backslash is a literal filename character, not a separator.
  const segments = parseSegments(input.slice(1), true, /\//);
  if (!segments) return null;
  return { key: `/${segments.join('/')}`, segments };
}

function keyIsWithin(rootKey: string, key: string): boolean {
  if (rootKey === key) return true;
  if (rootKey === '/') return key.startsWith('/');
  const prefix = rootKey.endsWith('/') ? rootKey : `${rootKey}/`;
  return key.startsWith(prefix);
}

function renderPath(parsed: ParsedPath, mode: PathRootMode): string {
  if (mode === 'windows') {
    const drive = parsed.drive ?? 'C';
    return `${drive}:\\${parsed.segments.join('\\')}`;
  }
  return `/${parsed.segments.join('/')}`;
}

function rootDisplay(root: string, mode: PathRootMode): string {
  const parsed = parsePath(root, mode);
  return parsed ? renderPath(parsed, mode) : root.trim();
}

/**
 * Normalizes a path into the comparison form used by the managed-root checks.
 * Windows comparison is case-insensitive; Linux is case-sensitive. Managed
 * `\\wsl.localhost\devstack\...` and `\\wsl$\devstack\...` paths are compared
 * in their Linux backend namespace. Invalid or relative paths return ''.
 */
export function normalizePathForCompare(path: string, mode: PathRootMode = 'local'): string {
  return parsePath(path, mode)?.key ?? '';
}

/** True when path is the root itself or any descendant of it. */
export function isPathWithinRoot(
  root: string,
  path: string,
  mode: PathRootMode = 'local',
): boolean {
  const rootPath = parsePath(root, mode);
  const target = parsePath(path, mode);
  if (!rootPath || !target) return false;
  return keyIsWithin(rootPath.key, target.key);
}

/** True only when path is a strict descendant of root. */
export function isStrictlyInsideRoot(
  root: string,
  path: string,
  mode: PathRootMode = 'local',
): boolean {
  const rootPath = parsePath(root, mode);
  const target = parsePath(path, mode);
  if (!rootPath || !target) return false;
  return target.key !== rootPath.key && keyIsWithin(rootPath.key, target.key);
}

/**
 * Clamps a browsed/selected path into the managed root. Valid descendants keep
 * their path; the root itself and every outside or invalid path become the
 * root. The result is safe to send to the filesystem browser.
 */
export function clampToRoot(
  root: string,
  path: string,
  mode: PathRootMode = 'local',
): string {
  const rootPath = parsePath(root, mode);
  if (!rootPath) return root.trim();
  const visibleRoot = renderPath(rootPath, mode);
  const target = parsePath(path, mode);
  if (!target || !keyIsWithin(rootPath.key, target.key)) return visibleRoot;
  if (target.key === rootPath.key) return visibleRoot;
  return renderPath(
    {
      ...target,
      drive: rootPath.drive ?? target.drive,
      segments: [...rootPath.segments, ...target.segments.slice(rootPath.segments.length)],
    },
    mode,
  );
}

/**
 * Returns the path segments below root, never including the root itself. The
 * root label is rendered separately by the browser, so it is not repeated in
 * these child crumbs.
 */
export function crumbsBelowRoot(
  root: string,
  path: string,
  mode: PathRootMode = 'local',
): PathCrumb[] {
  const rootPath = parsePath(root, mode);
  const target = parsePath(path, mode);
  if (!rootPath || !target || !keyIsWithin(rootPath.key, target.key)) return [];
  const relative = target.segments.slice(rootPath.segments.length);
  if (relative.length === 0) return [];

  const crumbs: PathCrumb[] = [];
  let accumulated = renderPath(rootPath, mode);
  const separator = mode === 'windows' ? '\\' : '/';
  for (const segment of relative) {
    accumulated = accumulated.endsWith(separator)
      ? `${accumulated}${segment}`
      : `${accumulated}${separator}${segment}`;
    crumbs.push({ label: segment, path: accumulated });
  }
  return crumbs;
}

/**
 * Returns the parent directory, capped at root. Outside or invalid paths fall
 * back to the root so navigation can never escape the managed boundary.
 */
export function parentWithinRoot(
  root: string,
  path: string,
  mode: PathRootMode = 'local',
): string {
  const rootPath = parsePath(root, mode);
  if (!rootPath) return root.trim();
  const visibleRoot = renderPath(rootPath, mode);
  const target = parsePath(path, mode);
  if (!target || !keyIsWithin(rootPath.key, target.key)) return visibleRoot;
  if (target.key === rootPath.key) return visibleRoot;
  if (target.segments.length <= rootPath.segments.length) return visibleRoot;

  const parent: ParsedPath = {
    ...target,
    segments: target.segments.slice(0, -1),
    key: mode === 'windows'
      ? `${target.drive?.toLowerCase() ?? 'c'}:/${target.segments
          .slice(0, -1)
          .map((part) => part.toLowerCase())
          .join('/')}`
      : `/${target.segments.slice(0, -1).join('/')}`,
  };
  if (parent.segments.length === rootPath.segments.length) return visibleRoot;
  return renderPath(parent, mode);
}
