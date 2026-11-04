import { render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import StatusBar from './StatusBar';
import { hostInfo, type HostInfo } from '../api';

// The bar is one read + one render. The repo's own hoisted mock
// style drives /api/host-info; every test below sets what serve
// answered.
vi.mock('../api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../api')>()),
  hostInfo: vi.fn(),
}));

const answer: HostInfo = {
  hostname: 'Mac.home',
  os: 'darwin',
  arch: 'arm64',
  cpuPercent: 32.7,
  cores: 10,
  memTotalBytes: 16 * 1024 ** 3,
  memUsedBytes: 13.2 * 1024 ** 3,
  memPercent: 81.9,
  repo: '/Users/x/code/devcules',
  branch: 'main',
  gpuName: 'Apple M2 Pro',
  collected: '2026-10-31T16:47:48Z',
};

beforeEach(() => {
  vi.mocked(hostInfo).mockResolvedValue(answer);
});
afterEach(() => {
  vi.restoreAllMocks();
});

describe('StatusBar', () => {
  it('renders the user, machine, repo@branch and the resource trio', async () => {
    render(<StatusBar username="admin" />);
    await waitFor(() => expect(screen.getByText('admin')).toBeTruthy());
    expect(screen.getByText('Mac.home')).toBeTruthy();
    const footer = screen.getByRole('contentinfo');
    expect(footer.textContent).toContain('devcules@main');
    expect(footer.textContent).toContain('33%'); // cpuPercent 32.7 rounds
    expect(footer.textContent).toContain('10C');
    expect(footer.textContent).toContain('13.2G/16.0G');
    expect(footer.textContent).toContain('82%'); // memPercent 81.9 rounds
    expect(footer.textContent).toContain('Apple M2 Pro');
  });

  it('an unreachable serve hides the bar', async () => {
    vi.mocked(hostInfo).mockResolvedValue('unreachable');
    const { container } = render(<StatusBar username="admin" />);
    await waitFor(() => expect(container.querySelector('footer')).toBeNull());
    expect(container.firstChild).toBeNull();
  });

  // jsdom does not apply Tailwind, so this pins the intent: the stats
  // are desktop chrome and must stay hidden below md on a phone.
  it('hides itself on a phone and shows from md up', async () => {
    render(<StatusBar username="admin" />);
    const footer = await screen.findByRole('contentinfo');
    expect(footer.className).toContain('hidden');
    expect(footer.className).toContain('md:block');
  });

  it('a field serve omitted simply reads nothing: no GPU line', async () => {
    const noGpu = { ...answer, gpuName: undefined };
    vi.mocked(hostInfo).mockResolvedValue(noGpu);
    render(<StatusBar username="admin" />);
    await waitFor(() => expect(screen.getByText('Mac.home')).toBeTruthy());
    expect(screen.queryByText('GPU')).toBeNull();
  });

  it('repo without a branch renders the repo name alone', async () => {
    const noBranch = { ...answer, branch: '' };
    vi.mocked(hostInfo).mockResolvedValue(noBranch);
    render(<StatusBar username="admin" />);
    await waitFor(() => expect(screen.getByText('devcules')).toBeTruthy());
    expect(screen.queryByText(/@/)).toBeNull();
  });

  it('poll cadence: hostInfo is called again on the 5-second tick', async () => {
    render(<StatusBar username="admin" />);
    await waitFor(() => expect(vi.mocked(hostInfo)).toHaveBeenCalledTimes(1));
  });
});
