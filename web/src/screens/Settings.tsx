import { useCallback, useEffect, useState } from 'react';
import {
  beginRegisterPasskey,
  deletePasskey,
  finishRegisterPasskey,
  listPasskeys,
  passkeyStatus,
  renamePasskey,
  signOut,
  type PasskeySummary,
} from '../api';
import { credentialToJSON, passkeysUsableHere, toCreationOptions } from '../webauthn';
import Redaction from './Redaction';

// 'elsewhere': the server has passkeys on, but this page is open at
// an IP address, where WebAuthn never works.
type PasskeyState = 'loading' | 'on' | 'elsewhere' | 'off' | 'unreachable';

export default function Settings({
  username,
  onSignedOut,
  onSessionExpired,
}: {
  username: string;
  onSignedOut: () => void;
  onSessionExpired: () => void;
}) {
  const [status, setStatus] = useState<PasskeyState>('loading');
  const [reason, setReason] = useState('');
  const [keys, setKeys] = useState<PasskeySummary[]>([]);
  const [notice, setNotice] = useState<string | null>(null);
  const [adding, setAdding] = useState(false);
  const [confirmRemove, setConfirmRemove] = useState<string | null>(null);
  const [renaming, setRenaming] = useState<string | null>(null);
  const [draft, setDraft] = useState('');

  // Promise chains, not await: states resolve off the effect's
  // synchronous path, the way the Terminals refresh does.
  const refresh = useCallback((): Promise<void> => {
    return passkeyStatus().then(
      (st) => {
        if (st === 'unreachable') {
          setStatus('unreachable');
          return;
        }
        setStatus(st.enabled ? (passkeysUsableHere() ? 'on' : 'elsewhere') : 'off');
        if (!st.enabled) {
          setReason(st.reason);
        }
        return listPasskeys().then(
          (res) => {
            if (res === 'unauthorized') {
              onSessionExpired();
              return;
            }
            if (res === 'unreachable') {
              setNotice("Couldn't load your passkeys. Try again.");
              return;
            }
            setKeys(res.passkeys);
          },
          () => {
            setNotice("Couldn't load your passkeys. Try again.");
          },
        );
      },
      () => {
        setStatus('unreachable');
      },
    );
  }, [onSessionExpired]);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  async function leave(): Promise<void> {
    await signOut();
    onSignedOut();
  }

  async function add(): Promise<void> {
    if (adding) {
      return;
    }
    setAdding(true);
    setNotice(null);
    try {
      const begun = await beginRegisterPasskey();
      if (begun === 'unauthorized') {
        onSessionExpired();
        return;
      }
      if (begun === 'disabled' || begun === 'unreachable') {
        setNotice("Couldn't add that passkey. Try again.");
        return;
      }
      let options: PublicKeyCredentialCreationOptions;
      try {
        options = toCreationOptions(begun.options);
      } catch {
        setNotice("Couldn't add that passkey. Try again.");
        return;
      }
      let cred: Credential | null;
      try {
        cred = await navigator.credentials.create({ publicKey: options });
      } catch (err) {
        // A cancel shows nothing; anything else says so once.
        if (!(err instanceof DOMException && err.name === 'NotAllowedError')) {
          setNotice("Couldn't add that passkey. Try again.");
        }
        return;
      }
      if (cred === null) {
        setNotice("Couldn't add that passkey. Try again.");
        return;
      }
      const done = await finishRegisterPasskey(credentialToJSON(cred as PublicKeyCredential));
      if (done === 'unauthorized') {
        onSessionExpired();
        return;
      }
      if (done !== 'created') {
        setNotice("Couldn't add that passkey. Try again.");
        return;
      }
      await refresh();
    } finally {
      setAdding(false);
    }
  }

  async function saveRename(id: string): Promise<void> {
    const name = draft.trim();
    if (name === '') {
      setNotice('Give the passkey a name.');
      return;
    }
    const res = await renamePasskey(id, name);
    if (res === 'unauthorized') {
      onSessionExpired();
      return;
    }
    if (res !== true) {
      setNotice("Couldn't rename that passkey. Try again.");
      return;
    }
    setRenaming(null);
    await refresh();
  }

  async function remove(id: string): Promise<void> {
    const res = await deletePasskey(id);
    if (res === 'unauthorized') {
      onSessionExpired();
      return;
    }
    if (res !== true) {
      setNotice("Couldn't remove that passkey. Try again.");
      return;
    }
    setConfirmRemove(null);
    await refresh();
  }

  // Existing keys stay listed while passkeys are off, so they can
  // still be renamed or removed; the Add button needs them on.
  const showList = status === 'on' || status === 'elsewhere' || keys.length > 0;

  return (
    <div className="flex flex-1 flex-col gap-4 px-6 pt-8">
      <h2 className="text-lead font-semibold text-text">Settings</h2>
      <p className="text-body text-muted">
        Signed in as <span className="text-text">{username}</span>
      </p>
      <section aria-label="Passkeys" className="flex flex-col gap-3">
        <h3 className="text-body font-semibold text-text">Passkeys</h3>
        {status === 'loading' && <p className="text-body text-muted">Checking passkeys…</p>}
        {status === 'unreachable' && (
          <p role="alert" className="text-body text-text">
            Couldn&apos;t check passkey status. Try again.
          </p>
        )}
        {status === 'off' && <p className="text-body text-muted">{reason}</p>}
        {status === 'elsewhere' && (
          <p className="text-body text-muted">
            Passkeys don&apos;t work at an IP address. Open Devcules at its web address
            (DEVCULES_PUBLIC_URL) to add or use one.
          </p>
        )}
        {showList &&
          (keys.length === 0 ? (
            <p className="text-body text-muted">No passkeys yet.</p>
          ) : (
            <ul className="flex flex-col gap-2">
              {keys.map((k) => (
                <li
                  key={k.id}
                  className="flex min-h-11 items-center gap-2 rounded-md border border-muted px-3"
                >
                  {renaming === k.id ? (
                    <>
                      <input
                        aria-label="Passkey name"
                        autoFocus
                        className="min-h-11 min-w-0 flex-1 bg-surface px-2 text-body text-text"
                        value={draft}
                        onChange={(e) => setDraft(e.target.value)}
                      />
                      <button
                        type="button"
                        onClick={() => void saveRename(k.id)}
                        className="min-h-11 px-2 text-body text-text transition-colors duration-150"
                      >
                        Save
                      </button>
                      <button
                        type="button"
                        onClick={() => setRenaming(null)}
                        className="min-h-11 px-2 text-body text-muted transition-colors duration-150"
                      >
                        Cancel
                      </button>
                    </>
                  ) : (
                    <>
                      <span className="min-w-0 flex-1 truncate text-body text-text">{k.name}</span>
                      {k.backedUp && <span className="text-body text-muted">Backed up</span>}
                      <button
                        type="button"
                        aria-label={`Rename ${k.name}`}
                        onClick={() => {
                          setRenaming(k.id);
                          setDraft(k.name);
                        }}
                        className="min-h-11 px-2 text-body text-muted transition-colors duration-150"
                      >
                        Rename
                      </button>
                      {confirmRemove === k.id ? (
                        <>
                          <button
                            type="button"
                            aria-label={`Confirm remove ${k.name}`}
                            onClick={() => void remove(k.id)}
                            className="min-h-11 px-2 text-body text-text transition-colors duration-150"
                          >
                            Remove
                          </button>
                          <button
                            type="button"
                            onClick={() => setConfirmRemove(null)}
                            className="min-h-11 px-2 text-body text-muted transition-colors duration-150"
                          >
                            Keep
                          </button>
                        </>
                      ) : (
                        <button
                          type="button"
                          aria-label={`Remove ${k.name}`}
                          onClick={() => setConfirmRemove(k.id)}
                          className="min-h-11 px-2 text-body text-muted transition-colors duration-150"
                        >
                          Remove
                        </button>
                      )}
                    </>
                  )}
                </li>
              ))}
            </ul>
          ))}
        {status === 'on' && (
          <>
            <button
              type="button"
              onClick={() => void add()}
              disabled={adding}
              className="min-h-11 rounded-md border border-muted px-3 text-body text-text transition-colors duration-150 disabled:opacity-60"
            >
              {adding ? 'Waiting for your passkey…' : 'Add a passkey'}
            </button>
            <p className="text-body text-muted">Your password always works, even with passkeys.</p>
          </>
        )}
        {notice && (
          <p role="alert" className="text-body text-text">
            {notice}
          </p>
        )}
      </section>
      <Redaction onSessionExpired={onSessionExpired} />
      <button
        type="button"
        onClick={leave}
        className="min-h-11 rounded-md border border-muted px-3 text-body text-text transition-colors duration-150"
      >
        Sign out
      </button>
    </div>
  );
}
