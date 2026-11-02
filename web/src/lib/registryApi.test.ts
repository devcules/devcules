import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  getMCPFleet, getSkillsFleet, inspectEntry, installEntry, listInstalled,
  registryErr, removeInstalled, searchRegistry,
  type CatalogEntry, type Result,
} from './registryApi';

function respond(status: number, body: unknown = null): void {
  const response = body === null ? new Response(null, { status }) : Response.json(body, { status });
  window.fetch = vi.fn(async () => response) as typeof fetch;
}

afterEach(() => vi.unstubAllGlobals());

const mcpEntry: CatalogEntry = {
  id: 'mcp-official:x/y', kind: 'mcp', name: 'x/y', description: 'd',
  source_id: 'mcp-official', source_name: 'Official MCP Registry',
  install: { type: 'stdio', command: 'npx', args: ['-y', 'x/y'] },
};

describe('registryApi', () => {
  it('maps 401 to unauthorized', async () => {
    respond(401);
    expect(await getMCPFleet()).toBe('unauthorized');
    expect(await listInstalled('skill')).toBe('unauthorized');
  });

  it('maps network failure and 5xx to unreachable', async () => {
    window.fetch = vi.fn(async () => { throw new TypeError('down'); }) as typeof fetch;
    expect(await getMCPFleet()).toBe('unreachable');
    respond(500);
    expect(await searchRegistry('mcp', 'x')).toBe('unreachable');
  });

  it('returns fleet/body JSON on success', async () => {
    respond(200, [{ name: 'github', status: 'ok', agents: ['claude-code'] }]);
    const fleet = await getMCPFleet();
    expect(fleet).not.toBe('unauthorized');
    expect(fleet).not.toBe('unreachable');
    expect(Array.isArray(fleet) && fleet[0]).toMatchObject({ name: 'github', status: 'ok' });
  });

  it('surfaces the {error:{code,message}} body via registryErr', async () => {
    respond(409, { error: { code: 'no_inference_backend', message: 'registry explain is not wired' } });
    const bad = await inspectEntry('mcp', mcpEntry, {});
    const e = registryErr(bad);
    expect(e).toEqual({ code: 'no_inference_backend', message: 'registry explain is not wired' });
  });

  it('registryErr is null for data, unauthorized and unreachable', async () => {
    respond(200, []);
    const ok: Result<unknown[]> = await listInstalled('mcp');
    expect(registryErr(ok)).toBeNull();
    expect(registryErr('unauthorized')).toBeNull();
    expect(registryErr('unreachable')).toBeNull();
  });

  it('install/remove/report send the right method and path', async () => {
    respond(200, { ok: true });
    await installEntry('mcp', 'github', mcpEntry, {});
    let [url, init] = (window.fetch as ReturnType<typeof vi.fn>).mock.calls[0] as [string, RequestInit];
    expect(url).toBe('/api/registry/mcp/install');
    expect(init.method).toBe('POST');
    expect(JSON.parse(String(init.body))).toMatchObject({ name: 'github' });

    respond(204);
    await removeInstalled('skill', 'pdf', 'claude-code');
    [url, init] = (window.fetch as ReturnType<typeof vi.fn>).mock.calls[0] as [string, RequestInit];
    expect(url).toBe('/api/registry/skill/installed/pdf?harness=claude-code');
    expect(init.method).toBe('DELETE');
  });

  it('search encodes the query and skill fleet returns data', async () => {
    respond(200, []);
    await searchRegistry('mcp', 'a b&c');
    const [url] = (window.fetch as ReturnType<typeof vi.fn>).mock.calls[0] as [string];
    expect(url).toBe('/api/registry/mcp/search?q=a%20b%26c');

    respond(200, [{ agent: 'claude-code', skills: [] }]);
    const f = await getSkillsFleet();
    expect(Array.isArray(f) && f[0]).toMatchObject({ agent: 'claude-code' });
  });
});
