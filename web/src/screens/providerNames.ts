import type { ProviderFormat } from '../api';

// providerLabel names a provider for people when its profile has no
// name (a serve from before profiles).
export function providerLabel(id: string): string {
  return { meta: 'Meta', anthropic: 'Anthropic', openai: 'OpenAI' }[id] ?? id;
}

// formatLabel names a profile's API format for people.
export function formatLabel(f: ProviderFormat | undefined): string {
  return f === 'anthropic' ? 'Anthropic' : 'OpenAI-compatible';
}
