import { afterEach, describe, expect, it, vi } from 'vitest';
import * as api from './api';

// Every fetch wrapper in api.ts keeps one contract the screens rely on:
// it never throws, whatever the server or the network does, and a 401
// comes back as 'unauthorized' wherever the signature offers it, so a
// screen can always route an expired session to sign-in.

type Scenario = { name: string; respond: () => Promise<Response> };

const json = (status: number, body: unknown) => async () => Response.json(body, { status });
const empty = (status: number) => async () => new Response(null, { status });

const scenarios: Scenario[] = [
  { name: 'network down', respond: async () => Promise.reject(new TypeError('Failed to fetch')) },
  { name: '401', respond: empty(401) },
  { name: '400 with a reason', respond: json(400, { error: 'refused for a reason' }) },
  { name: '403', respond: empty(403) },
  { name: '404', respond: json(404, { error: 'not here' }) },
  { name: '409', respond: json(409, { error: 'conflict' }) },
  { name: '413', respond: empty(413) },
  { name: '429', respond: empty(429) },
  { name: '500', respond: empty(500) },
  { name: '502 with a reason', respond: json(502, { error: 'upstream' }) },
  { name: '503', respond: json(503, { error: 'offline' }) },
  { name: '504', respond: empty(504) },
  { name: '200 junk', respond: async () => new Response('not json', { status: 200 }) },
  { name: '200 empty object', respond: json(200, {}) },
  { name: '201 empty object', respond: json(201, {}) },
  { name: '204', respond: empty(204) },
];

const where = { machine: 'studio' };
const draft: api.ProviderDraft = { name: 'n', format: 'openai', alsoAnthropic: false, baseUrl: 'https://x', api: 'auto', model: '', default: false, key: '' };

// The call list: every async wrapper, with plausible arguments. offer401
// marks the ones whose signature promises 'unauthorized'.
const calls: Array<{ name: string; call: () => Promise<unknown>; offer401?: boolean }> = [
  { name: 'me', call: () => api.me() },
  { name: 'signOut', call: () => api.signOut() },
  { name: 'passkeyStatus', call: () => api.passkeyStatus() },
  { name: 'beginRegisterPasskey', call: () => api.beginRegisterPasskey(), offer401: true },
  { name: 'finishRegisterPasskey', call: () => api.finishRegisterPasskey({}), offer401: true },
  { name: 'beginPasskeyLogin', call: () => api.beginPasskeyLogin() },
  { name: 'finishPasskeyLogin', call: () => api.finishPasskeyLogin({}) },
  { name: 'listPasskeys', call: () => api.listPasskeys(), offer401: true },
  { name: 'renamePasskey', call: () => api.renamePasskey('id', 'n'), offer401: true },
  { name: 'deletePasskey', call: () => api.deletePasskey('id'), offer401: true },
  { name: 'listTerminals', call: () => api.listTerminals(where), offer401: true },
  { name: 'openTerminal', call: () => api.openTerminal(where, 'n'), offer401: true },
  { name: 'closeTerminal', call: () => api.closeTerminal('aaaaaaaa00000000', where), offer401: true },
  { name: 'renameTerminal', call: () => api.renameTerminal('aaaaaaaa00000000', 'n', where), offer401: true },
  { name: 'listAgents', call: () => api.listAgents(), offer401: true },
  { name: 'startAgent', call: () => api.startAgent('codex', { project: 'p', provider: 'meta', model: 'm', name: 'n' }), offer401: true },
  { name: 'listProjects', call: () => api.listProjects(), offer401: true },
  { name: 'addProject', call: () => api.addProject({ kind: 'runner', target: 'box', dir: '/srv' }), offer401: true },
  { name: 'removeProject', call: () => api.removeProject('p'), offer401: true },
  { name: 'openProjectTerminal', call: () => api.openProjectTerminal('p', 'n'), offer401: true },
  { name: 'browse', call: () => api.browse(where, '/srv'), offer401: true },
  { name: 'addProvider', call: () => api.addProvider(draft), offer401: true },
  { name: 'editProvider', call: () => api.editProvider('meta', draft), offer401: true },
  { name: 'removeProvider', call: () => api.removeProvider('meta'), offer401: true },
  { name: 'makeDefaultProvider', call: () => api.makeDefaultProvider('meta'), offer401: true },
  { name: 'probeModels', call: () => api.probeModels({ format: 'openai', baseUrl: 'https://x' }), offer401: true },
  { name: 'listProviders', call: () => api.listProviders(), offer401: true },
  { name: 'setProviderKey', call: () => api.setProviderKey('meta', 'k'), offer401: true },
  { name: 'removeProviderKey', call: () => api.removeProviderKey('meta'), offer401: true },
  { name: 'providerModels', call: () => api.providerModels('meta'), offer401: true },
  { name: 'hostInfo', call: () => api.hostInfo(), offer401: true },
  { name: 'getRedaction', call: () => api.getRedaction(), offer401: true },
  { name: 'setRedactionPolicy', call: () => api.setRedactionPolicy({ SECRET: 'mask', EMAIL: 'off', PHONE: 'off', CARD: 'off', IPV4: 'off' }), offer401: true },
  { name: 'testRedaction', call: () => api.testRedaction('text'), offer401: true },
  { name: 'listTraces', call: () => api.listTraces(), offer401: true },
  { name: 'getTrace', call: () => api.getTrace('l1'), offer401: true },
  { name: 'listAudit', call: () => api.listAudit(5), offer401: true },
  { name: 'checkAudit', call: () => api.checkAudit(), offer401: true },
  { name: 'listApprovals', call: () => api.listApprovals('pending', 5), offer401: true },
  { name: 'answerApproval', call: () => api.answerApproval('a1', true), offer401: true },
  { name: 'getApprovalSettings', call: () => api.getApprovalSettings(), offer401: true },
  { name: 'setAccessMode', call: () => api.setAccessMode('runner:box', 'critical'), offer401: true },
  { name: 'setCriticalRules', call: () => api.setCriticalRules([]), offer401: true },
  { name: 'checkCommand', call: () => api.checkCommand('ls'), offer401: true },
  { name: 'getPush', call: () => api.getPush(), offer401: true },
  { name: 'addPushDevice', call: () => api.addPushDevice({ endpoint: 'https://push.example' }, 'phone'), offer401: true },
  { name: 'removePushDevice', call: () => api.removePushDevice('d1'), offer401: true },
  { name: 'testPush', call: () => api.testPush(), offer401: true },
  { name: 'listMachines', call: () => api.listMachines(), offer401: true },
  { name: 'machineBudget', call: () => api.machineBudget('studio'), offer401: true },
  { name: 'machineScreen', call: () => api.machineScreen('studio'), offer401: true },
  { name: 'mintEnrolCode', call: () => api.mintEnrolCode('studio'), offer401: true },
  { name: 'removeMachine', call: () => api.removeMachine('studio'), offer401: true },
  { name: 'machineTree', call: () => api.machineTree('studio'), offer401: true },
  { name: 'machineInput', call: () => api.machineInput({ kind: 'click', x: 1, y: 2 }, 'studio'), offer401: true },
  { name: 'machineGround', call: () => api.machineGround('Pay button', 'studio'), offer401: true },
  { name: 'machineVerify', call: () => api.machineVerify('studio'), offer401: true },
  { name: 'listAssistants', call: () => api.listAssistants(), offer401: true },
  { name: 'chatAssistant', call: () => api.chatAssistant('builtin', 'hi', 's1'), offer401: true },
  { name: 'readAssistant', call: () => api.readAssistant('builtin', 's1'), offer401: true },
];

afterEach(() => {
  vi.restoreAllMocks();
});

describe('api wrappers', () => {
  for (const s of scenarios) {
    it(`never throw on ${s.name}`, async () => {
      window.fetch = vi.fn(s.respond) as typeof fetch;
      const threw: string[] = [];
      for (const c of calls) {
        let got: unknown;
        try {
          got = await c.call();
        } catch (e) {
          threw.push(`${c.name}: ${String(e).slice(0, 60)}`);
          continue;
        }
        if (s.name === '401' && c.offer401) {
          expect(got, `${c.name} on 401`).toBe('unauthorized');
        }
      }
      expect(threw, `threw on ${s.name}`).toEqual([]);
    });
  }

  it('builds addresses without a fetch', () => {
    expect(api.streamURL('aaaaaaaa00000000', 80, 24, where)).toContain('machine=studio');
    expect(api.machineScreenURL('studio')).toContain('studio');
    expect(api.projectWhere({ id: 'p', name: 'p', kind: 'machine', target: 'studio', dir: '/x', createdAt: '' })).toEqual(where);
    // A runner project names its runner: with more than one link the
    // terminal cannot be opened on "whichever is free".
    expect(api.projectWhere({ id: 'p', name: 'p', kind: 'runner', target: 'box', dir: '/x', createdAt: '' })).toEqual({ runner: 'box' });
  });
});
