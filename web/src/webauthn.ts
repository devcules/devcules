// WebAuthn without a library: base64url helpers plus the conversions
// between our server's JSON and the navigator.credentials shapes.
// The converters take unknown JSON and throw on malformed input;
// screens catch and show plain words (a user cancel shows nothing).

// passkeysUsableHere says whether this page's address can use
// passkeys at all. WebAuthn refuses an IP address as the relying
// party, so a page opened at http://127.0.0.1 or a Tailscale 100.x
// address can never complete a ceremony, even when the server has
// passkeys on for its DEVCULES_PUBLIC_URL name.
export function passkeysUsableHere(hostname: string = window.location.hostname): boolean {
  if (hostname.startsWith('[') || hostname.includes(':')) {
    return false;
  }
  return !/^\d{1,3}(\.\d{1,3}){3}$/.test(hostname);
}

export function bytesToBase64url(data: ArrayBuffer | Uint8Array): string {
  const bytes = data instanceof Uint8Array ? data : new Uint8Array(data);
  let binary = '';
  for (let i = 0; i < bytes.length; i += 0x8000) {
    binary += String.fromCharCode(...bytes.subarray(i, i + 0x8000));
  }
  return btoa(binary).replaceAll('+', '-').replaceAll('/', '_').replace(/=+$/, '');
}

export function base64urlToBytes(s: string): Uint8Array {
  const binary = atob(s.replaceAll('-', '+').replaceAll('_', '/'));
  const out = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i++) {
    out[i] = binary.charCodeAt(i);
  }
  return out;
}

function bytesOf(value: unknown, what: string): ArrayBuffer {
  if (typeof value !== 'string') {
    throw new Error(`bad WebAuthn options: ${what} is not a string`);
  }
  // A fresh exact-size buffer: .buffer of a subarray would over-share.
  return base64urlToBytes(value).buffer as ArrayBuffer;
}

function recordOf(value: unknown, what: string): Record<string, unknown> {
  if (typeof value !== 'object' || value === null) {
    throw new Error(`bad WebAuthn options: ${what} is not an object`);
  }
  return value as Record<string, unknown>;
}

function descriptorsOf(value: unknown, what: string): PublicKeyCredentialDescriptor[] {
  if (value === undefined) {
    return [];
  }
  if (!Array.isArray(value)) {
    throw new Error(`bad WebAuthn options: ${what} is not an array`);
  }
  return value.map((entry) => {
    const e = recordOf(entry, what);
    if (e.type !== 'public-key') {
      throw new Error(`bad WebAuthn options: ${what} holds a ${String(e.type)} credential`);
    }
    return {
      type: 'public-key',
      id: bytesOf(e.id, `${what} id`),
      transports: e.transports as AuthenticatorTransport[] | undefined,
    } satisfies PublicKeyCredentialDescriptor;
  });
}

// toCreationOptions converts register/begin JSON into create()
// options, decoding every base64url member into an ArrayBuffer.
export function toCreationOptions(json: unknown): PublicKeyCredentialCreationOptions {
  const o = recordOf(json, 'creation options');
  const user = recordOf(o.user, 'user');
  if (typeof user.name !== 'string' || typeof user.displayName !== 'string') {
    throw new Error('bad WebAuthn options: user needs a name and display name');
  }
  return {
    challenge: bytesOf(o.challenge, 'challenge'),
    rp: o.rp as PublicKeyCredentialRpEntity,
    user: {
      id: bytesOf(user.id, 'user id'),
      name: user.name,
      displayName: user.displayName,
    },
    pubKeyCredParams: (o.pubKeyCredParams ?? []) as PublicKeyCredentialParameters[],
    excludeCredentials: descriptorsOf(o.excludeCredentials, 'excludeCredentials'),
    authenticatorSelection: o.authenticatorSelection as AuthenticatorSelectionCriteria | undefined,
    attestation: o.attestation as AttestationConveyancePreference | undefined,
    timeout: o.timeout as number | undefined,
  };
}

// toRequestOptions converts login/begin JSON into get() options.
export function toRequestOptions(json: unknown): PublicKeyCredentialRequestOptions {
  const o = recordOf(json, 'request options');
  return {
    challenge: bytesOf(o.challenge, 'challenge'),
    rpId: o.rpId as string | undefined,
    allowCredentials: descriptorsOf(o.allowCredentials, 'allowCredentials'),
    userVerification: o.userVerification as UserVerificationRequirement | undefined,
    timeout: o.timeout as number | undefined,
  };
}

function isAttestationResponse(
  response: AuthenticatorResponse,
): response is AuthenticatorAttestationResponse {
  return 'attestationObject' in response;
}

// credentialToJSON converts a created or fetched credential into the
// JSON our finish endpoints parse: base64url strings throughout,
// like PublicKeyCredential.toJSON but without depending on it.
export function credentialToJSON(cred: PublicKeyCredential): Record<string, unknown> {
  const base: Record<string, unknown> = {
    id: cred.id,
    rawId: bytesToBase64url(cred.rawId),
    type: cred.type,
    clientExtensionResults: cred.getClientExtensionResults(),
  };
  if (cred.authenticatorAttachment !== null) {
    base.authenticatorAttachment = cred.authenticatorAttachment;
  }
  const response = cred.response;
  if (isAttestationResponse(response)) {
    return {
      ...base,
      response: {
        clientDataJSON: bytesToBase64url(response.clientDataJSON),
        attestationObject: bytesToBase64url(response.attestationObject),
        transports: response.getTransports(),
      },
    };
  }
  // AuthenticatorResponse is a base interface, not a union, so the
  // guard above does not narrow the fallthrough: anything without an
  // attestation object is an assertion response.
  const assertion = response as AuthenticatorAssertionResponse;
  return {
    ...base,
    response: {
      clientDataJSON: bytesToBase64url(assertion.clientDataJSON),
      authenticatorData: bytesToBase64url(assertion.authenticatorData),
      signature: bytesToBase64url(assertion.signature),
      userHandle: assertion.userHandle === null ? null : bytesToBase64url(assertion.userHandle),
    },
  };
}
