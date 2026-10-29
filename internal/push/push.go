package push

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/devcules/devcules/internal/store"
	"github.com/devcules/devcules/internal/vault"
)

// KeySecret is the vault name of the VAPID private key.
const KeySecret = "push/vapid_key"

const (
	// maxPayload keeps one record well under RFC 8291's 4096 bytes.
	maxPayload = 3000
	// recordSize is the rs field: one record holds the whole message.
	recordSize = 4096
	// maxEndpoint bounds a stored endpoint URL.
	maxEndpoint = 2048
	// maxDevices bounds how many browsers may subscribe.
	maxDevices = 20
	// jwtLife is how long a VAPID JWT lasts (RFC 8292: at most 24h).
	jwtLife = 12 * time.Hour
)

// Keys is where the VAPID key lives: the vault.
type Keys interface {
	Get(ctx context.Context, name string) ([]byte, error)
	Put(ctx context.Context, name string, value []byte) error
}

// Options wires a Service. Store and Keys are required.
type Options struct {
	Store store.Store
	Keys  Keys
	// Contact is the VAPID subject: a mailto: or https: URL push
	// services may use to reach the operator.
	Contact string
	Log     *slog.Logger
	// Client sends pushes; nil uses a 10 second client that never
	// follows redirects.
	Client *http.Client
	// AllowHTTP accepts http:// endpoints. Tests only: real push
	// services are always https.
	AllowHTTP bool
}

// Service holds the VAPID key and sends pushes.
type Service struct {
	o       Options
	repo    store.PushSubscriptionsRepo
	signer  *ecdsa.PrivateKey
	public  []byte // uncompressed P-256 point, 65 bytes
	created bool
	now     func() time.Time
}

// New loads the VAPID key from the vault, making one on first start
// (Created then reports true).
func New(ctx context.Context, o Options) (*Service, error) {
	if o.Store == nil || o.Keys == nil {
		return nil, errors.New("push: store and keys are required")
	}
	if !strings.HasPrefix(o.Contact, "mailto:") && !strings.HasPrefix(o.Contact, "https://") {
		return nil, fmt.Errorf("push: the VAPID contact must be a mailto: or https: URL")
	}
	if o.Log == nil {
		o.Log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if o.Client == nil {
		o.Client = &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}}
	}
	s := &Service{o: o, repo: o.Store.PushSubscriptions(), now: time.Now}
	raw, err := o.Keys.Get(ctx, KeySecret)
	if errors.Is(err, vault.ErrNotFound) {
		k, gerr := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if gerr != nil {
			return nil, gerr
		}
		if raw, err = k.Bytes(); err != nil {
			return nil, err
		}
		if err := o.Keys.Put(ctx, KeySecret, raw); err != nil {
			return nil, fmt.Errorf("push: store the VAPID key: %w", err)
		}
		s.created = true
	} else if err != nil {
		return nil, fmt.Errorf("push: read the VAPID key: %w", err)
	}
	if s.signer, err = ecdsa.ParseRawPrivateKey(elliptic.P256(), raw); err != nil {
		return nil, fmt.Errorf("push: the stored VAPID key does not parse: %w", err)
	}
	if s.public, err = s.signer.PublicKey.Bytes(); err != nil {
		return nil, err
	}
	return s, nil
}

// Created reports whether New made the key.
func (s *Service) Created() bool { return s.created }

// PublicKey is the VAPID public key, base64url, as
// PushManager.subscribe takes it (applicationServerKey).
func (s *Service) PublicKey() string {
	return base64.RawURLEncoding.EncodeToString(s.public)
}

// Device is a subscription as the settings list shows it: never the
// endpoint, which is a capability URL.
type Device struct {
	ID      string    `json:"id"`
	Label   string    `json:"label"`
	Service string    `json:"service"`
	Created time.Time `json:"created"`
	LastOK  time.Time `json:"lastOk,omitzero"`
}

// deviceID names a subscription without revealing its endpoint.
func deviceID(endpoint string) string {
	sum := sha256.Sum256([]byte(endpoint))
	return hex.EncodeToString(sum[:8])
}

// decodeKey reads a base64url (or standard base64) key, padded or not.
func decodeKey(s string) ([]byte, error) {
	s = strings.TrimRight(strings.TrimSpace(s), "=")
	s = strings.NewReplacer("+", "-", "/", "_").Replace(s)
	return base64.RawURLEncoding.DecodeString(s)
}

// Subscribe stores a browser's subscription, as PushSubscription.toJSON
// gives it. It returns the device id.
func (s *Service) Subscribe(ctx context.Context, endpoint, p256dh, auth, label string) (string, error) {
	u, err := url.Parse(endpoint)
	switch {
	case err != nil || u.Host == "" || len(endpoint) > maxEndpoint || u.User != nil:
		return "", errors.New("push: endpoint must be the subscription's URL")
	case u.Scheme != "https" && !(s.o.AllowHTTP && u.Scheme == "http"):
		return "", errors.New("push: endpoint must be https")
	}
	pub, err := decodeKey(p256dh)
	if err != nil {
		return "", errors.New("push: keys.p256dh must be base64url")
	}
	if _, err := ecdh.P256().NewPublicKey(pub); err != nil {
		return "", errors.New("push: keys.p256dh is not a P-256 public key")
	}
	sec, err := decodeKey(auth)
	if err != nil || len(sec) != 16 {
		return "", errors.New("push: keys.auth must be 16 bytes, base64url")
	}
	label = strings.TrimSpace(label)
	if len(label) > 80 {
		label = label[:80]
	}
	if label == "" {
		label = "Browser"
	}
	list, err := s.repo.List(ctx)
	if err != nil {
		return "", err
	}
	known := false
	for _, d := range list {
		known = known || d.Endpoint == endpoint
	}
	if !known && len(list) >= maxDevices {
		return "", fmt.Errorf("push: at most %d devices; remove one first", maxDevices)
	}
	err = s.repo.Put(ctx, store.PushSubscription{Endpoint: endpoint, P256dh: pub, Auth: sec, Label: label, CreatedAt: s.now().UTC()})
	return deviceID(endpoint), err
}

// Devices lists the subscriptions.
func (s *Service) Devices(ctx context.Context) ([]Device, error) {
	list, err := s.repo.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Device, 0, len(list))
	for _, d := range list {
		host := ""
		if u, err := url.Parse(d.Endpoint); err == nil {
			host = u.Hostname()
		}
		out = append(out, Device{ID: deviceID(d.Endpoint), Label: d.Label, Service: host, Created: d.CreatedAt, LastOK: d.LastOKAt})
	}
	return out, nil
}

// Remove deletes the device with id, reporting whether there was one.
func (s *Service) Remove(ctx context.Context, id string) (bool, error) {
	list, err := s.repo.List(ctx)
	if err != nil {
		return false, err
	}
	for _, d := range list {
		if deviceID(d.Endpoint) == id {
			return true, s.repo.Delete(ctx, d.Endpoint)
		}
	}
	return false, nil
}

// Message is one notification. The service worker shows Title and
// Body and opens URL on a tap; Tag replaces an earlier notification
// with the same tag on the device.
type Message struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	URL   string `json:"url,omitempty"`
	Tag   string `json:"tag,omitempty"`
	// TTL is how long the push service keeps it for an offline
	// device; 0 means 10 minutes.
	TTL time.Duration `json:"-"`
	// Urgent asks for immediate delivery (Urgency: high).
	Urgent bool `json:"-"`
}

// Result counts one fan-out.
type Result struct {
	Sent    int `json:"sent"`
	Failed  int `json:"failed"`
	Removed int `json:"removed"`
}

// Send pushes m to every device at once. Dead subscriptions (404 and
// 410) are removed; other failures are logged without the endpoint.
func (s *Service) Send(ctx context.Context, m Message) (Result, error) {
	payload, err := json.Marshal(m)
	if err != nil {
		return Result{}, err
	}
	if len(payload) > maxPayload {
		return Result{}, fmt.Errorf("push: message is %d bytes, over %d", len(payload), maxPayload)
	}
	list, err := s.repo.List(ctx)
	if err != nil {
		return Result{}, err
	}
	var res Result
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, d := range list {
		wg.Go(func() {
			status, err := s.send(ctx, d, payload, m)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil && status/100 == 2:
				res.Sent++
				_ = s.repo.Delivered(ctx, d.Endpoint, s.now().UTC())
			case status == http.StatusNotFound || status == http.StatusGone:
				res.Removed++
				_ = s.repo.Delete(ctx, d.Endpoint)
				s.o.Log.Info("push subscription gone, removed", "device", deviceID(d.Endpoint), "label", d.Label)
			default:
				res.Failed++
				msg := ""
				if err != nil {
					msg = err.Error()
				}
				s.o.Log.Warn("push failed", "device", deviceID(d.Endpoint), "status", status, "err", msg)
			}
		})
	}
	wg.Wait()
	return res, nil
}

// send encrypts and posts one message. A transport error has status 0.
func (s *Service) send(ctx context.Context, d store.PushSubscription, payload []byte, m Message) (int, error) {
	body, err := encrypt(payload, d.P256dh, d.Auth)
	if err != nil {
		return 0, err
	}
	u, err := url.Parse(d.Endpoint)
	if err != nil {
		return 0, err
	}
	jwt, err := s.vapidJWT(u.Scheme + "://" + u.Host)
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.Endpoint, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	ttl := m.TTL
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}
	urgency := "normal"
	if m.Urgent {
		urgency = "high"
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Content-Encoding", "aes128gcm")
	req.Header.Set("TTL", strconv.Itoa(int(ttl.Seconds())))
	req.Header.Set("Urgency", urgency)
	req.Header.Set("Authorization", "vapid t="+jwt+", k="+s.PublicKey())
	resp, err := s.o.Client.Do(req)
	if err != nil {
		// The error text names the endpoint URL: drop it.
		var uerr *url.Error
		if errors.As(err, &uerr) {
			err = uerr.Err
		}
		return 0, err
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
	_ = resp.Body.Close()
	return resp.StatusCode, nil
}

// vapidJWT signs RFC 8292's token for one push service origin.
func (s *Service) vapidJWT(audience string) (string, error) {
	enc := base64.RawURLEncoding
	header := enc.EncodeToString([]byte(`{"typ":"JWT","alg":"ES256"}`))
	claims, err := json.Marshal(map[string]any{"aud": audience, "exp": s.now().Add(jwtLife).Unix(), "sub": s.o.Contact})
	if err != nil {
		return "", err
	}
	signing := header + "." + enc.EncodeToString(claims)
	digest := sha256.Sum256([]byte(signing))
	r, sv, err := ecdsa.Sign(rand.Reader, s.signer, digest[:])
	if err != nil {
		return "", err
	}
	// JWS wants the raw 64-byte r || s, not ASN.1.
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	sv.FillBytes(sig[32:])
	return signing + "." + enc.EncodeToString(sig), nil
}

// encrypt is RFC 8291: an ephemeral ECDH key against the browser's
// key, mixed with its auth secret, then one aes128gcm record (RFC
// 8188) whose header carries the salt and our ephemeral public key.
func encrypt(plaintext, uaPublic, authSecret []byte) ([]byte, error) {
	eph, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	return encryptWith(plaintext, uaPublic, authSecret, eph, salt)
}

// encryptWith is encrypt with the ephemeral key and salt given: the
// RFC's test vector pins both.
func encryptWith(plaintext, uaPublic, authSecret []byte, eph *ecdh.PrivateKey, salt []byte) ([]byte, error) {
	ua, err := ecdh.P256().NewPublicKey(uaPublic)
	if err != nil {
		return nil, fmt.Errorf("push: subscription key: %w", err)
	}
	secret, err := eph.ECDH(ua)
	if err != nil {
		return nil, err
	}
	asPublic := eph.PublicKey().Bytes()
	cek, nonce, err := deriveKeys(secret, authSecret, uaPublic, asPublic, salt)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(cek)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	// One record: the plaintext, then the last-record delimiter 0x02.
	record := append(bytes.Clone(plaintext), 0x02)
	header := make([]byte, 0, 16+4+1+len(asPublic))
	header = append(header, salt...)
	header = binary.BigEndian.AppendUint32(header, recordSize)
	header = append(header, byte(len(asPublic)))
	header = append(header, asPublic...)
	return gcm.Seal(header, nonce, record, nil), nil
}

// deriveKeys runs RFC 8291 §3.4 and RFC 8188 §2.2 key derivation.
func deriveKeys(ecdhSecret, authSecret, uaPublic, asPublic, salt []byte) (cek, nonce []byte, err error) {
	keyInfo := "WebPush: info\x00" + string(uaPublic) + string(asPublic)
	ikm, err := hkdf.Key(sha256.New, ecdhSecret, authSecret, keyInfo, 32)
	if err != nil {
		return nil, nil, err
	}
	if cek, err = hkdf.Key(sha256.New, ikm, salt, "Content-Encoding: aes128gcm\x00", 16); err != nil {
		return nil, nil, err
	}
	if nonce, err = hkdf.Key(sha256.New, ikm, salt, "Content-Encoding: nonce\x00", 12); err != nil {
		return nil, nil, err
	}
	return cek, nonce, nil
}
