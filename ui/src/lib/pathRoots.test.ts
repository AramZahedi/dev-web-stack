import { describe, expect, it } from 'vitest';
import {
  clampToRoot,
  crumbsBelowRoot,
  isPathWithinRoot,
  isStrictlyInsideRoot,
  normalizePathForCompare,
  parentWithinRoot,
} from './pathRoots';

const WINDOWS_ROOT = 'C:\\Users\\Aram\\.devstack\\projects';
const WSL_ROOT = '/data/devstack/projects';
const LOCAL_ROOT = '/home/dev/.devstack/projects';

describe('normalizePathForCompare', () => {
  it('normalizes separators, trailing separators and Windows case', () => {
    expect(
      normalizePathForCompare('C:\\Users\\Aram\\.devstack\\projects\\App\\', 'windows'),
    ).toBe('c:/users/aram/.devstack/projects/app');
    expect(normalizePathForCompare('C:/Users/Aram/.devstack/projects/app', 'windows')).toBe(
      'c:/users/aram/.devstack/projects/app',
    );
    expect(normalizePathForCompare('/data/devstack/projects/app/', 'local')).toBe(
      '/data/devstack/projects/app',
    );
  });

  it('keeps Linux comparison case-sensitive', () => {
    expect(normalizePathForCompare('/data/devstack/projects/App', 'local')).toBe(
      '/data/devstack/projects/App',
    );
    expect(normalizePathForCompare('/data/devstack/projects/App', 'local')).not.toBe(
      normalizePathForCompare('/data/devstack/projects/app', 'local'),
    );
  });

  it('maps managed WSL UNC paths to the backend namespace', () => {
    expect(
      normalizePathForCompare(
        '\\\\wsl.localhost\\devstack\\data\\devstack\\projects\\app',
        'wsl',
      ),
    ).toBe('/data/devstack/projects/app');
    expect(
      normalizePathForCompare('\\\\wsl$\\devstack\\data\\devstack\\projects\\app', 'wsl'),
    ).toBe('/data/devstack/projects/app');
  });

  it('allows colons in Linux and WSL folder names but not Windows components', () => {
    expect(normalizePathForCompare('/data/devstack/projects/app:v2', 'local')).toBe(
      '/data/devstack/projects/app:v2',
    );
    expect(normalizePathForCompare('/data/devstack/projects/app:v2', 'wsl')).toBe(
      '/data/devstack/projects/app:v2',
    );
    expect(normalizePathForCompare('C:\\projects\\file:stream', 'windows')).toBe('');
  });

  it('rejects relative paths and parent components', () => {
    expect(normalizePathForCompare('projects/app', 'local')).toBe('');
    expect(normalizePathForCompare('/data/devstack/projects/../other', 'local')).toBe('');
    expect(normalizePathForCompare('C:\\projects\\..\\other', 'windows')).toBe('');
  });
});

describe('isStrictlyInsideRoot', () => {
  it('rejects the root itself, including trailing separators and case variants', () => {
    expect(isStrictlyInsideRoot(WINDOWS_ROOT, WINDOWS_ROOT, 'windows')).toBe(false);
    expect(isStrictlyInsideRoot(WINDOWS_ROOT, `${WINDOWS_ROOT}\\`, 'windows')).toBe(false);
    expect(
      isStrictlyInsideRoot(WINDOWS_ROOT, WINDOWS_ROOT.toLowerCase(), 'windows'),
    ).toBe(false);
    expect(isStrictlyInsideRoot(WSL_ROOT, `${WSL_ROOT}/`, 'wsl')).toBe(false);
    expect(isStrictlyInsideRoot(LOCAL_ROOT, LOCAL_ROOT, 'local')).toBe(false);
  });

  it('is component-aware and rejects sibling roots', () => {
    expect(isStrictlyInsideRoot(WINDOWS_ROOT, `${WINDOWS_ROOT}\\app`, 'windows')).toBe(true);
    expect(isStrictlyInsideRoot(WINDOWS_ROOT, `${WINDOWS_ROOT}-old\\app`, 'windows')).toBe(
      false,
    );
    expect(isStrictlyInsideRoot(WSL_ROOT, `${WSL_ROOT}/app`, 'wsl')).toBe(true);
    expect(isStrictlyInsideRoot(WSL_ROOT, `${WSL_ROOT}-old/app`, 'wsl')).toBe(false);
    expect(isStrictlyInsideRoot(LOCAL_ROOT, `${LOCAL_ROOT}/app`, 'local')).toBe(true);
    expect(isStrictlyInsideRoot(LOCAL_ROOT, `${LOCAL_ROOT}-old/app`, 'local')).toBe(false);
  });

  it('uses Windows case folding and mixed separators', () => {
    expect(
      isStrictlyInsideRoot(WINDOWS_ROOT, 'c:/users/aram/.devstack/projects/App/src', 'windows'),
    ).toBe(true);
    expect(
      isStrictlyInsideRoot(WINDOWS_ROOT, 'C:\\Users\\Aram\\.devstack\\Projects\\app', 'windows'),
    ).toBe(true);
  });

  it('is case-sensitive for local Linux paths', () => {
    expect(isStrictlyInsideRoot(LOCAL_ROOT, '/home/dev/.devstack/projects/app', 'local')).toBe(
      true,
    );
    expect(isStrictlyInsideRoot(LOCAL_ROOT, '/home/dev/.devstack/projects/App', 'local')).toBe(
      true,
    );
    expect(isStrictlyInsideRoot('/home/dev/.devstack/Projects', '/home/dev/.devstack/projects/app', 'local')).toBe(
      false,
    );
  });

  it('accepts Linux folder names containing a colon', () => {
    expect(isStrictlyInsideRoot(WSL_ROOT, `${WSL_ROOT}/app:v2`, 'wsl')).toBe(true);
    expect(isStrictlyInsideRoot(LOCAL_ROOT, `${LOCAL_ROOT}/app:v2`, 'local')).toBe(true);
    expect(
      isStrictlyInsideRoot(
        WSL_ROOT,
        '\\\\wsl.localhost\\devstack\\data\\devstack\\projects\\app:v2',
        'wsl',
      ),
    ).toBe(true);
  });

  it('rejects any parent component even when the cleaned path would be inside', () => {
    expect(
      isStrictlyInsideRoot(LOCAL_ROOT, `${LOCAL_ROOT}/app/../other`, 'local'),
    ).toBe(false);
    expect(
      isStrictlyInsideRoot(WINDOWS_ROOT, `${WINDOWS_ROOT}\\app\\..\\other`, 'windows'),
    ).toBe(false);
    expect(isStrictlyInsideRoot(WSL_ROOT, `${WSL_ROOT}/app/../../etc`, 'wsl')).toBe(false);
  });

  it('accepts managed WSL UNC paths and rejects other distros or siblings', () => {
    expect(
      isStrictlyInsideRoot(
        WSL_ROOT,
        '\\\\wsl.localhost\\devstack\\data\\devstack\\projects\\app',
        'wsl',
      ),
    ).toBe(true);
    expect(
      isStrictlyInsideRoot(WSL_ROOT, '\\\\wsl$\\devstack\\data\\devstack\\projects\\app', 'wsl'),
    ).toBe(true);
    expect(
      isStrictlyInsideRoot(
        WSL_ROOT,
        '\\\\wsl.localhost\\Ubuntu\\data\\devstack\\projects\\app',
        'wsl',
      ),
    ).toBe(false);
    expect(
      isStrictlyInsideRoot(
        WSL_ROOT,
        '\\\\wsl.localhost\\devstack\\data\\devstack\\projects-old\\app',
        'wsl',
      ),
    ).toBe(false);
  });
});

describe('isPathWithinRoot', () => {
  it('allows the root itself for browsing but not for site selection', () => {
    expect(isPathWithinRoot(WSL_ROOT, WSL_ROOT, 'wsl')).toBe(true);
    expect(isPathWithinRoot(WSL_ROOT, `${WSL_ROOT}/`, 'wsl')).toBe(true);
    expect(isPathWithinRoot(WSL_ROOT, `${WSL_ROOT}/app`, 'wsl')).toBe(true);
    expect(isPathWithinRoot(WSL_ROOT, '/data/devstack', 'wsl')).toBe(false);
  });
});

describe('clampToRoot', () => {
  it('keeps a valid child path and normalizes its display form', () => {
    expect(clampToRoot(WINDOWS_ROOT, 'c:/users/aram/.devstack/projects/App', 'windows')).toBe(
      'C:\\Users\\Aram\\.devstack\\projects\\App',
    );
    expect(clampToRoot(WSL_ROOT, '\\\\wsl.localhost\\devstack\\data\\devstack\\projects\\app', 'wsl')).toBe(
      '/data/devstack/projects/app',
    );
    expect(clampToRoot(LOCAL_ROOT, `${LOCAL_ROOT}/app/`, 'local')).toBe(`${LOCAL_ROOT}/app`);
  });

  it('returns the root for outside, invalid and root paths', () => {
    expect(clampToRoot(WINDOWS_ROOT, 'C:\\Users\\Aram\\projects\\app', 'windows')).toBe(
      WINDOWS_ROOT,
    );
    expect(clampToRoot(WINDOWS_ROOT, WINDOWS_ROOT, 'windows')).toBe(WINDOWS_ROOT);
    expect(clampToRoot(WSL_ROOT, '/home/dev/projects/app', 'wsl')).toBe(WSL_ROOT);
    expect(clampToRoot(LOCAL_ROOT, `${LOCAL_ROOT}/app/../other`, 'local')).toBe(LOCAL_ROOT);
    expect(clampToRoot(LOCAL_ROOT, LOCAL_ROOT, 'local')).toBe(LOCAL_ROOT);
  });
});

describe('crumbsBelowRoot', () => {
  it('returns only segments below the root', () => {
    expect(crumbsBelowRoot(WSL_ROOT, WSL_ROOT, 'wsl')).toEqual([]);
    expect(crumbsBelowRoot(WSL_ROOT, `${WSL_ROOT}/app/src`, 'wsl')).toEqual([
      { label: 'app', path: `${WSL_ROOT}/app` },
      { label: 'src', path: `${WSL_ROOT}/app/src` },
    ]);
  });

  it('does not include the root label or outside segments', () => {
    const crumbs = crumbsBelowRoot(LOCAL_ROOT, `${LOCAL_ROOT}/app`, 'local');
    expect(crumbs.map((crumb) => crumb.label)).toEqual(['app']);
    expect(crumbsBelowRoot(LOCAL_ROOT, '/home/dev/projects/app', 'local')).toEqual([]);
    expect(crumbsBelowRoot(WSL_ROOT, `${WSL_ROOT}-old/app`, 'wsl')).toEqual([]);
  });

  it('builds Windows crumbs with the root drive and separators', () => {
    expect(crumbsBelowRoot(WINDOWS_ROOT, `${WINDOWS_ROOT}\\app\\src`, 'windows')).toEqual([
      { label: 'app', path: `${WINDOWS_ROOT}\\app` },
      { label: 'src', path: `${WINDOWS_ROOT}\\app\\src` },
    ]);
  });

  it('maps WSL UNC paths to crumbs below the managed root', () => {
    expect(
      crumbsBelowRoot(
        WSL_ROOT,
        '\\\\wsl.localhost\\devstack\\data\\devstack\\projects\\app\\src',
        'wsl',
      ),
    ).toEqual([
      { label: 'app', path: `${WSL_ROOT}/app` },
      { label: 'src', path: `${WSL_ROOT}/app/src` },
    ]);
  });
});

describe('parentWithinRoot', () => {
  it('walks up one level but never above the root', () => {
    expect(parentWithinRoot(WSL_ROOT, `${WSL_ROOT}/app/src`, 'wsl')).toBe(`${WSL_ROOT}/app`);
    expect(parentWithinRoot(WSL_ROOT, `${WSL_ROOT}/app`, 'wsl')).toBe(WSL_ROOT);
    expect(parentWithinRoot(WSL_ROOT, WSL_ROOT, 'wsl')).toBe(WSL_ROOT);
    expect(parentWithinRoot(WSL_ROOT, '/data/devstack', 'wsl')).toBe(WSL_ROOT);
  });

  it('keeps Windows parents inside the root', () => {
    expect(parentWithinRoot(WINDOWS_ROOT, `${WINDOWS_ROOT}\\app\\src`, 'windows')).toBe(
      `${WINDOWS_ROOT}\\app`,
    );
    expect(parentWithinRoot(WINDOWS_ROOT, `${WINDOWS_ROOT}\\app`, 'windows')).toBe(WINDOWS_ROOT);
    expect(parentWithinRoot(WINDOWS_ROOT, 'C:\\Users\\Aram\\projects\\app', 'windows')).toBe(
      WINDOWS_ROOT,
    );
  });
});
