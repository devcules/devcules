import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, it, vi } from 'vitest';
import Providers from './Providers';

type Reply = { status: number; body?: unknown };

function reply(r: Reply): Response {
  return r.body === undefined ? new Response(null, { status: r.status }) : Response.json(r.body, { status: r.status });
}

function serve(handler: (key: string, body: string) => Reply | undefined): Array<{ key: string; body: string }> {
  const seen: Array<{ key: string; body: string }> = [];
  window.fetch = vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
    const key = `${init?.method ?? 'GET'} ${input.toString()}`;
    const body = typeof init?.body === 'string' ? init.body : '';
    seen.push({ key, body });
    const r = handler(key, body);
    if (!r) {
      throw new Error(`unexpected fetch ${key}`);
    }
    return reply(r);
  }) as typeof fetch;
  return seen;
}

afterEach(() => {
  vi.restoreAllMocks();
});

// fakeKey builds a made-up key from parts, so no file holds a whole
// key shape.
const fakeKey = ['sk', 'test', 'x'.repeat(16)].join('-');

const meta = { id: 'meta', name: 'Meta', format: 'openai', alsoAnthropic: true, baseUrl: 'https://api.meta.ai', api: 'auto', default: true, ready: true };
const vllm = { id: 'localvllm', name: 'Local vLLM', format: 'openai', baseUrl: 'http://127.0.0.1:8000/v1', api: 'chat', model: 'qwen-3', ready: false };

describe('Providers', () => {
  it('lists each profile with its format, key state, address and model', async () => {
    serve((key) => (key === 'GET /api/providers' ? { status: 200, body: { providers: [vllm, meta] } } : undefined));
    render(<Providers onSessionExpired={vi.fn()} />);
    const card = within(await screen.findByRole('listitem', { name: 'Local vLLM' }));
    expect(card.getByText('OpenAI-compatible')).toBeDefined();
    expect(card.getByText('No key')).toBeDefined();
    expect(card.getByText('http://127.0.0.1:8000/v1 · qwen-3')).toBeDefined();
    expect(card.getByRole('button', { name: 'Make default' })).toBeDefined();
    const def = within(screen.getByRole('listitem', { name: 'Meta' }));
    expect(def.getByText('Default')).toBeDefined();
    expect(def.getByText('+ Anthropic')).toBeDefined();
    expect(def.queryByRole('button', { name: 'Make default' })).toBeNull();
  });

  it('adds an OpenAI-compatible profile, loading its models with the typed key', async () => {
    const user = userEvent.setup();
    let added = false;
    const seen = serve((key) => {
      if (key === 'GET /api/providers') return { status: 200, body: { providers: added ? [meta, vllm] : [meta] } };
      if (key === 'POST /api/providers/probe') return { status: 200, body: { models: ['llama-4', 'qwen-3'] } };
      if (key === 'POST /api/providers') {
        added = true;
        return { status: 201, body: { provider: vllm } };
      }
      return undefined;
    });
    render(<Providers onSessionExpired={vi.fn()} />);
    await screen.findByRole('listitem', { name: 'Meta' });
    await user.click(screen.getByRole('button', { name: 'OpenAI-compatible (vLLM, Nebius…)' }));
    await user.type(screen.getByLabelText('Name'), 'Local vLLM');
    await user.type(screen.getByLabelText(/^Base URL/), 'http://127.0.0.1:8000/v1');
    expect((screen.getByLabelText(/^API for agents/) as HTMLSelectElement).value).toBe('chat');
    expect(screen.getByText(/Codex needs \/v1\/responses/)).toBeDefined();
    const keyField = screen.getByLabelText('API key');
    expect(keyField.getAttribute('type')).toBe('password');
    await user.type(keyField, fakeKey);
    await user.click(screen.getByRole('button', { name: 'Load models' }));
    expect(await screen.findByText('2 models.')).toBeDefined();
    expect(JSON.parse(seen.find((s) => s.key === 'POST /api/providers/probe')?.body ?? '{}')).toEqual({
      format: 'openai',
      baseUrl: 'http://127.0.0.1:8000/v1',
      key: fakeKey,
    });
    await user.selectOptions(screen.getByLabelText('Loaded models'), 'qwen-3');
    await user.click(screen.getByRole('button', { name: 'Add provider' }));
    expect(await screen.findByText('Local vLLM added.')).toBeDefined();
    expect(JSON.parse(seen.find((s) => s.key === 'POST /api/providers')?.body ?? '{}')).toEqual({
      name: 'Local vLLM',
      format: 'openai',
      alsoAnthropic: false,
      baseUrl: 'http://127.0.0.1:8000/v1',
      api: 'chat',
      model: 'qwen-3',
      default: false,
      key: fakeKey,
    });
    expect(await screen.findByRole('listitem', { name: 'Local vLLM' })).toBeDefined();
    expect(screen.queryByText(fakeKey)).toBeNull();
    // The form is blank again, the key gone from it.
    expect((screen.getByLabelText('API key') as HTMLInputElement).value).toBe('');
  });

  it('edits a profile, keeping the saved key, and shows a refusal', async () => {
    const user = userEvent.setup();
    let refuse = true;
    const seen = serve((key) => {
      if (key === 'GET /api/providers') return { status: 200, body: { providers: [meta] } };
      if (key === 'POST /api/providers/probe') return { status: 400, body: { error: 'key: paste the API key to load models' } };
      if (key === 'PUT /api/providers/meta') {
        if (refuse) {
          refuse = false;
          return { status: 400, body: { error: 'key: the base URL moved to another host, so paste the API key again' } };
        }
        return { status: 200, body: { provider: { ...meta, model: 'muse-spark-1.3' } } };
      }
      return undefined;
    });
    render(<Providers onSessionExpired={vi.fn()} />);
    await user.click(await screen.findByRole('button', { name: 'Edit Meta' }));
    expect(screen.getByRole('form', { name: 'Edit Meta' })).toBeDefined();
    expect(screen.getByPlaceholderText('Leave blank to keep the saved key')).toBeDefined();
    // An OpenAI-format profile can say it serves Messages too.
    expect((screen.getByLabelText(/Also serves Anthropic/) as HTMLInputElement).checked).toBe(true);
    await user.click(screen.getByRole('button', { name: 'Load models' }));
    expect(await screen.findByText('key: paste the API key to load models')).toBeDefined();
    expect(JSON.parse(seen.find((s) => s.key === 'POST /api/providers/probe')?.body ?? '{}').id).toBe('meta');
    await user.type(screen.getByLabelText(/^Default model/), 'muse-spark-1.3');
    await user.click(screen.getByRole('button', { name: 'Save' }));
    expect((await screen.findByRole('alert')).textContent).toMatch(/paste the API key again/);
    await user.click(screen.getByRole('button', { name: 'Save' }));
    expect(await screen.findByText('Meta saved.')).toBeDefined();
    const put = JSON.parse(seen.filter((s) => s.key === 'PUT /api/providers/meta')[1]?.body ?? '{}');
    expect(put.key).toBe('');
    expect(put.model).toBe('muse-spark-1.3');
  });

  it('switches the format to Anthropic', async () => {
    const user = userEvent.setup();
    serve((key) => (key === 'GET /api/providers' ? { status: 200, body: { providers: [meta] } } : undefined));
    render(<Providers onSessionExpired={vi.fn()} />);
    await screen.findByRole('listitem', { name: 'Meta' });
    await user.click(screen.getByRole('button', { name: 'Anthropic' }));
    expect((screen.getByLabelText('Format') as HTMLSelectElement).value).toBe('anthropic');
    expect(screen.queryByLabelText(/^API for agents/)).toBeNull();
    expect(screen.getByText('API key (x-api-key)')).toBeDefined();
    await user.selectOptions(screen.getByLabelText('Format'), 'openai');
    expect(screen.getByText('API key (Bearer)')).toBeDefined();
  });

  it('makes a profile the default, and removes one after a confirm', async () => {
    const user = userEvent.setup();
    let gone = false;
    const seen = serve((key) => {
      if (key === 'GET /api/providers') return { status: 200, body: { providers: gone ? [meta] : [vllm, meta] } };
      if (key === 'PUT /api/providers/localvllm/default') return { status: 204 };
      if (key === 'DELETE /api/providers/localvllm') {
        gone = true;
        return { status: 204 };
      }
      if (key === 'DELETE /api/providers/meta') return { status: 409, body: { error: 'the default provider cannot be removed: make another one the default first' } };
      return undefined;
    });
    render(<Providers onSessionExpired={vi.fn()} />);
    await user.click(await screen.findByRole('button', { name: 'Make default' }));
    expect(await screen.findByText('Local vLLM is the default.')).toBeDefined();
    expect(seen.some((s) => s.key === 'PUT /api/providers/localvllm/default')).toBe(true);
    await user.click(screen.getByRole('button', { name: 'Remove Meta' }));
    await user.click(screen.getByRole('button', { name: 'Confirm remove Meta' }));
    expect((await screen.findByRole('alert')).textContent).toMatch(/make another one the default/);
    await user.click(screen.getByRole('button', { name: 'Remove Local vLLM' }));
    expect(screen.getByText(/Its key goes too/)).toBeDefined();
    await user.click(screen.getByRole('button', { name: 'Confirm remove Local vLLM' }));
    await waitFor(() => expect(screen.queryByRole('listitem', { name: 'Local vLLM' })).toBeNull());
    expect(screen.getByText('Local vLLM removed.')).toBeDefined();
  });

  it('returns to sign-in when the session is gone', async () => {
    serve((key) => (key === 'GET /api/providers' ? { status: 401 } : undefined));
    const expired = vi.fn();
    render(<Providers onSessionExpired={expired} />);
    await waitFor(() => expect(expired).toHaveBeenCalled());
  });
});
