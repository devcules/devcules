import { describe, expect, it } from 'vitest';
import {
  base64urlToBytes,
  bytesToBase64url,
  credentialToJSON,
  passkeysUsableHere,
  toCreationOptions,
  toRequestOptions,
} from './webauthn';

function bytes(list: number[]): ArrayBuffer {
  return new Uint8Array(list).buffer;
}

describe('webauthn', () => {
  it('rules out IP addresses, which WebAuthn refuses', () => {
    expect(passkeysUsableHere('localhost')).toBe(true);
    expect(passkeysUsableHere('devcules.tail1234.ts.net')).toBe(true);
    expect(passkeysUsableHere('127.0.0.1')).toBe(false);
    expect(passkeysUsableHere('100.64.1.2')).toBe(false);
    expect(passkeysUsableHere('[::1]')).toBe(false);
    expect(passkeysUsableHere('::1')).toBe(false);
    // jsdom serves localhost.
    expect(passkeysUsableHere()).toBe(true);
  });

  it('round-trips base64url without padding or URL-unsafe chars', () => {
    const original = bytes([0, 1, 2, 250, 251, 252, 253, 254, 255]);
    const text = bytesToBase64url(original);
    expect(text).not.toMatch(/[+/=]/);
    expect(base64urlToBytes(text)).toEqual(new Uint8Array(original));
    expect(bytesToBase64url(new Uint8Array(original))).toBe(text);
  });

  it('encodes the known vector', () => {
    expect(bytesToBase64url(bytes([104, 101, 108, 108, 111]))).toBe('aGVsbG8');
  });

  it('throws on malformed base64url', () => {
    expect(() => base64urlToBytes('!!!')).toThrow();
  });

  it('converts creation options, decoding every binary member', () => {
    const options = toCreationOptions({
      challenge: 'aGVsbG8',
      rp: { id: 'localhost', name: 'Devcules' },
      user: { id: 'dXNlcg', name: 'admin', displayName: 'admin' },
      pubKeyCredParams: [{ type: 'public-key', alg: -7 }],
      excludeCredentials: [{ type: 'public-key', id: 'ZXhjbHVkZQ', transports: ['internal'] }],
      authenticatorSelection: { residentKey: 'required', userVerification: 'required' },
      attestation: 'none',
      timeout: 300000,
    });
    expect(bytesToBase64url(options.challenge as ArrayBuffer)).toBe('aGVsbG8');
    expect(options.rp).toEqual({ id: 'localhost', name: 'Devcules' });
    expect(bytesToBase64url(options.user.id as ArrayBuffer)).toBe('dXNlcg');
    expect(options.user.name).toBe('admin');
    expect(options.pubKeyCredParams).toEqual([{ type: 'public-key', alg: -7 }]);
    const excluded = options.excludeCredentials ?? [];
    expect(excluded).toHaveLength(1);
    expect(bytesToBase64url(excluded[0].id as ArrayBuffer)).toBe('ZXhjbHVkZQ');
    expect(excluded[0].transports).toEqual(['internal']);
    expect(options.authenticatorSelection).toEqual({ residentKey: 'required', userVerification: 'required' });
    expect(options.attestation).toBe('none');
    expect(options.timeout).toBe(300000);
  });

  it('accepts minimal creation options with no exclusions', () => {
    const options = toCreationOptions({
      challenge: 'aGVsbG8',
      rp: { name: 'Devcules' },
      user: { id: 'dXNlcg', name: 'admin', displayName: 'admin' },
    });
    expect(options.excludeCredentials).toEqual([]);
    expect(options.pubKeyCredParams).toEqual([]);
  });

  it('rejects malformed creation options', () => {
    expect(() => toCreationOptions(null)).toThrow();
    expect(() => toCreationOptions({})).toThrow();
    expect(() => toCreationOptions({ challenge: 7 })).toThrow();
    expect(() =>
      toCreationOptions({ challenge: 'aGVsbG8', user: { id: '!!!', name: 'a', displayName: 'a' } }),
    ).toThrow();
    expect(() => toCreationOptions({ challenge: 'aGVsbG8', user: { id: 'dXNlcg' } })).toThrow();
    expect(() =>
      toCreationOptions({
        challenge: 'aGVsbG8',
        user: { id: 'dXNlcg', name: 'a', displayName: 'a' },
        excludeCredentials: [{ type: 'magic', id: 'eA' }],
      }),
    ).toThrow();
    expect(() =>
      toCreationOptions({
        challenge: 'aGVsbG8',
        user: { id: 'dXNlcg', name: 'a', displayName: 'a' },
        excludeCredentials: 'nope',
      }),
    ).toThrow();
  });

  it('converts request options', () => {
    const options = toRequestOptions({
      challenge: 'Y2hhbGxlbmdl',
      rpId: 'localhost',
      allowCredentials: [{ type: 'public-key', id: 'YWxsb3c' }],
      userVerification: 'required',
      timeout: 300000,
    });
    expect(bytesToBase64url(options.challenge as ArrayBuffer)).toBe('Y2hhbGxlbmdl');
    expect(options.rpId).toBe('localhost');
    expect(options.allowCredentials).toHaveLength(1);
    expect(options.userVerification).toBe('required');
    expect(options.timeout).toBe(300000);
  });

  it('accepts discoverable request options with no allow list', () => {
    const options = toRequestOptions({ challenge: 'Y2hhbGxlbmdl', userVerification: 'required' });
    expect(options.allowCredentials).toEqual([]);
  });

  it('rejects malformed request options', () => {
    expect(() => toRequestOptions(null)).toThrow();
    expect(() => toRequestOptions({ challenge: '!!!' })).toThrow();
  });

  it('encodes a created credential like toJSON', () => {
    const cred = {
      id: 'Y3JlZA',
      rawId: bytes([1, 2, 3]),
      type: 'public-key',
      authenticatorAttachment: 'platform',
      getClientExtensionResults: () => ({}),
      response: {
        clientDataJSON: bytes([4, 5]),
        attestationObject: bytes([6, 7, 8]),
        getTransports: () => ['internal'],
      },
    } as unknown as PublicKeyCredential;
    expect(credentialToJSON(cred)).toEqual({
      id: 'Y3JlZA',
      rawId: bytesToBase64url(bytes([1, 2, 3])),
      type: 'public-key',
      authenticatorAttachment: 'platform',
      clientExtensionResults: {},
      response: {
        clientDataJSON: bytesToBase64url(bytes([4, 5])),
        attestationObject: bytesToBase64url(bytes([6, 7, 8])),
        transports: ['internal'],
      },
    });
  });

  it('encodes a fetched credential, omitting a null attachment', () => {
    const cred = {
      id: 'Y3JlZA',
      rawId: bytes([1]),
      type: 'public-key',
      authenticatorAttachment: null,
      getClientExtensionResults: () => ({}),
      response: {
        clientDataJSON: bytes([2]),
        authenticatorData: bytes([3]),
        signature: bytes([4]),
        userHandle: bytes([5]),
      },
    } as unknown as PublicKeyCredential;
    const json = credentialToJSON(cred);
    expect(json.authenticatorAttachment).toBeUndefined();
    expect(json.response).toEqual({
      clientDataJSON: bytesToBase64url(bytes([2])),
      authenticatorData: bytesToBase64url(bytes([3])),
      signature: bytesToBase64url(bytes([4])),
      userHandle: bytesToBase64url(bytes([5])),
    });
  });

  it('encodes a null user handle as null', () => {
    const cred = {
      id: 'Y3JlZA',
      rawId: bytes([1]),
      type: 'public-key',
      authenticatorAttachment: null,
      getClientExtensionResults: () => ({}),
      response: {
        clientDataJSON: bytes([2]),
        authenticatorData: bytes([3]),
        signature: bytes([4]),
        userHandle: null,
      },
    } as unknown as PublicKeyCredential;
    expect((credentialToJSON(cred).response as Record<string, unknown>).userHandle).toBeNull();
  });
});
