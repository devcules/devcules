package push

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/devcules/devcules/internal/store"
	"github.com/devcules/devcules/internal/store/sqlite"
	"github.com/devcules/devcules/internal/vault"
)

func b64(t *testing.T, s string) []byte {
	t.Helper()
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestRFC8291Vector reproduces RFC 8291 Appendix A byte for byte.
func TestRFC8291Vector(t *testing.T) {
	plaintext := b64(t, "V2hlbiBJIGdyb3cgdXAsIEkgd2FudCB0byBiZSBhIHdhdGVybWVsb24")
	asPrivate, err := ecdh.P256().NewPrivateKey(b64(t, "yfWPiYE-n46HLnH0KqZOF1fJJU3MYrct3AELtAQ-oRw"))
	if err != nil {
		t.Fatal(err)
	}
	uaPublic := b64(t, "BCVxsr7N_eNgVRqvHtD0zTZsEc6-VV-JvLexhqUzORcxaOzi6-AYWXvTBHm4bjyPjs7Vd8pZGH6SRpkNtoIAiw4")
	auth := b64(t, "BTBZMqHH6r4Tts7J_aSIgg")
	salt := b64(t, "DGv6ra1nlYgDCS1FRnbzlw")
	want := "DGv6ra1nlYgDCS1FRnbzlwAAEABBBP4z9KsN6nGRTbVYI_c7VJSPQTBtkgcy27mlmlMoZIIgDll6e3vCYLocInmYWAmS6TlzAC8wEqKK6PBru3jl7A_yl95bQpu6cVPTpK4Mqgkf1CXztLVBSt2Ks3oZwbuwXPXLWyouBWLVWGNWQexSgSxsj_Qulcy4a-fN"
	got, err := encryptWith(plaintext, uaPublic, auth, asPrivate, salt)
	if err != nil {
		t.Fatal(err)
	}
	if enc := base64.RawURLEncoding.EncodeToString(got); enc != want {
		t.Fatalf("message =\n%s\nwant\n%s", enc, want)
	}
	cek, nonce, _ := deriveKeys(b64(t, "kyrL1jIIOHEzg3sM2ZWRHDRB62YACZhhSlknJ672kSs"), auth, uaPublic, asPrivate.PublicKey().Bytes(), salt)
	if !bytes.Equal(cek, b64(t, "oIhVW04MRdy2XN9CiKLxTg")) || !bytes.Equal(nonce, b64(t, "4h_95klXJ5E_qnoN")) {
		t.Fatalf("cek %x nonce %x", cek, nonce)
	}
	if _, err := encryptWith(plaintext, []byte{4, 1}, auth, asPrivate, salt); err == nil {
		t.Fatal("a bad subscription key must fail")
	}
}

// subscriber is a test browser plus its push service: it decrypts what
// it receives and checks the VAPID token.
type subscriber struct {
	t      *testing.T
	key    *ecdh.PrivateKey
	auth   []byte
	status int
	mu     sync.Mutex
	got    []Message
	header http.Header
	srv    *httptest.Server
}

func newSubscriber(t *testing.T, vapid func() string) *subscriber {
	key, _ := ecdh.P256().GenerateKey(rand.Reader)
	s := &subscriber{t: t, key: key, auth: make([]byte, 16), status: http.StatusCreated}
	_, _ = rand.Read(s.auth)
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		s.mu.Lock()
		defer s.mu.Unlock()
		s.header = r.Header.Clone()
		if msg, err := s.open(body); err != nil {
			t.Errorf("subscriber cannot decrypt: %v", err)
		} else {
			s.got = append(s.got, msg)
		}
		if err := checkVAPID(r.Header.Get("Authorization"), vapid(), "http://"+r.Host); err != nil {
			t.Errorf("VAPID: %v", err)
		}
		w.WriteHeader(s.status)
	}))
	t.Cleanup(s.srv.Close)
	return s
}

// open decrypts one aes128gcm body as a browser would.
func (s *subscriber) open(body []byte) (Message, error) {
	if len(body) < 21 {
		return Message{}, errors.New("short body")
	}
	salt, rs, idLen := body[:16], binary.BigEndian.Uint32(body[16:20]), int(body[20])
	if rs != recordSize || idLen != 65 {
		return Message{}, errors.New("bad header")
	}
	asPub, err := ecdh.P256().NewPublicKey(body[21 : 21+idLen])
	if err != nil {
		return Message{}, err
	}
	secret, err := s.key.ECDH(asPub)
	if err != nil {
		return Message{}, err
	}
	cek, nonce, err := deriveKeys(secret, s.auth, s.key.PublicKey().Bytes(), asPub.Bytes(), salt)
	if err != nil {
		return Message{}, err
	}
	block, _ := aes.NewCipher(cek)
	gcm, _ := cipher.NewGCM(block)
	plain, err := gcm.Open(nil, nonce, body[21+idLen:], nil)
	if err != nil {
		return Message{}, err
	}
	if plain[len(plain)-1] != 0x02 {
		return Message{}, errors.New("no last-record delimiter")
	}
	var m Message
	return m, json.Unmarshal(plain[:len(plain)-1], &m)
}

func checkVAPID(header, publicKey, audience string) error {
	rest, ok := strings.CutPrefix(header, "vapid t=")
	if !ok {
		return errors.New("no vapid scheme: " + header)
	}
	jwt, k, ok := strings.Cut(rest, ", k=")
	if !ok || k != publicKey {
		return errors.New("k does not match the public key")
	}
	parts := strings.Split(jwt, ".")
	if len(parts) != 3 {
		return errors.New("jwt shape")
	}
	var claims struct {
		Aud string `json:"aud"`
		Exp int64  `json:"exp"`
		Sub string `json:"sub"`
	}
	raw, _ := base64.RawURLEncoding.DecodeString(parts[1])
	if err := json.Unmarshal(raw, &claims); err != nil {
		return err
	}
	if claims.Aud != audience || claims.Sub != "mailto:owner@example.test" {
		return errors.New("claims " + string(raw))
	}
	if left := time.Until(time.Unix(claims.Exp, 0)); left <= 0 || left > 24*time.Hour {
		return errors.New("exp must be within 24 hours")
	}
	pubBytes, _ := base64.RawURLEncoding.DecodeString(k)
	pub, err := ecdsa.ParseUncompressedPublicKey(elliptic.P256(), pubBytes)
	if err != nil {
		return err
	}
	sig, _ := base64.RawURLEncoding.DecodeString(parts[2])
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if len(sig) != 64 || !ecdsa.Verify(pub, digest[:], new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])) {
		return errors.New("signature does not verify")
	}
	return nil
}

type memKeys struct {
	mu sync.Mutex
	m  map[string][]byte
}

func (k *memKeys) Get(_ context.Context, name string) ([]byte, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	v, ok := k.m[name]
	if !ok {
		return nil, vault.ErrNotFound
	}
	return v, nil
}

func (k *memKeys) Put(_ context.Context, name string, v []byte) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.m[name] = v
	return nil
}

func newService(t *testing.T) (*Service, store.Store, *memKeys) {
	t.Helper()
	s, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	keys := &memKeys{m: map[string][]byte{}}
	svc, err := New(context.Background(), Options{Store: s, Keys: keys, Contact: "mailto:owner@example.test", AllowHTTP: true})
	if err != nil {
		t.Fatal(err)
	}
	return svc, s, keys
}

func (s *subscriber) subscribe(t *testing.T, svc *Service, label string) string {
	t.Helper()
	id, err := svc.Subscribe(context.Background(), s.srv.URL+"/push/"+label,
		base64.RawURLEncoding.EncodeToString(s.key.PublicKey().Bytes()), base64.StdEncoding.EncodeToString(s.auth), label)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// TestPushToSubscriber is M3.2's gate: a push reaches a test subscriber,
// decrypts, and carries a valid VAPID token.
func TestPushToSubscriber(t *testing.T) {
	svc, _, keys := newService(t)
	if !svc.Created() || len(keys.m[KeySecret]) != 32 {
		t.Fatal("the VAPID key was not made and stored")
	}
	sub := newSubscriber(t, svc.PublicKey)
	id := sub.subscribe(t, svc, "phone")

	res, err := svc.Send(context.Background(), Message{Title: "Approval needed", Body: "musecode on laptop", URL: "/approvals", Tag: "a1", Urgent: true, TTL: time.Minute})
	if err != nil || res != (Result{Sent: 1}) {
		t.Fatalf("Send = %+v %v", res, err)
	}
	if len(sub.got) != 1 || sub.got[0].Title != "Approval needed" || sub.got[0].URL != "/approvals" || sub.got[0].Tag != "a1" {
		t.Fatalf("subscriber got %+v", sub.got)
	}
	h := sub.header
	if h.Get("Content-Encoding") != "aes128gcm" || h.Get("TTL") != "60" || h.Get("Urgency") != "high" {
		t.Fatalf("headers = %v", h)
	}
	devices, _ := svc.Devices(context.Background())
	if len(devices) != 1 || devices[0].ID != id || devices[0].Label != "phone" || devices[0].Service != "127.0.0.1" || devices[0].LastOK.IsZero() {
		t.Fatalf("devices = %+v", devices)
	}
	b, _ := json.Marshal(devices)
	if strings.Contains(string(b), "/push/") {
		t.Fatal("the device list must not reveal the endpoint")
	}

	// The same key comes back on the next start.
	again, err := New(context.Background(), Options{Store: svc.o.Store, Keys: keys, Contact: "https://devcules.example.test"})
	if err != nil || again.Created() || again.PublicKey() != svc.PublicKey() {
		t.Fatalf("reload = %v created=%v", err, again.Created())
	}
}

func TestSendRemovesGoneAndLogsFailures(t *testing.T) {
	svc, _, _ := newService(t)
	var logs bytes.Buffer
	svc.o.Log = slog.New(slog.NewTextHandler(&logs, nil))
	gone := newSubscriber(t, svc.PublicKey)
	gone.status = http.StatusGone
	broken := newSubscriber(t, svc.PublicKey)
	broken.status = http.StatusInternalServerError
	ok := newSubscriber(t, svc.PublicKey)
	gone.subscribe(t, svc, "old")
	broken.subscribe(t, svc, "flaky")
	ok.subscribe(t, svc, "phone")
	res, err := svc.Send(context.Background(), Message{Title: "t", Body: "b"})
	if err != nil || res != (Result{Sent: 1, Failed: 1, Removed: 1}) {
		t.Fatalf("Send = %+v %v", res, err)
	}
	devices, _ := svc.Devices(context.Background())
	if len(devices) != 2 {
		t.Fatalf("devices after a 410 = %+v", devices)
	}
	if strings.Contains(logs.String(), "/push/") || !strings.Contains(logs.String(), "push failed") {
		t.Fatalf("logs = %s", logs.String())
	}
	// A dead push service is a transport error, logged without the URL.
	dead := newSubscriber(t, svc.PublicKey)
	dead.subscribe(t, svc, "dead")
	dead.srv.Close()
	res, _ = svc.Send(context.Background(), Message{Title: "t"})
	if res.Failed != 2 || strings.Contains(logs.String(), dead.srv.URL) {
		t.Fatalf("res = %+v logs = %s", res, logs.String())
	}
	if _, err := svc.Send(context.Background(), Message{Body: strings.Repeat("x", maxPayload)}); err == nil {
		t.Fatal("an oversized message must fail")
	}
}

func TestSubscribeValidates(t *testing.T) {
	svc, _, _ := newService(t)
	ctx := context.Background()
	key, _ := ecdh.P256().GenerateKey(rand.Reader)
	pub := base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes())
	auth := base64.RawURLEncoding.EncodeToString(make([]byte, 16))
	for name, c := range map[string][3]string{
		"not a url":   {"::", pub, auth},
		"no host":     {"https:///x", pub, auth},
		"userinfo":    {"https://u:p@push.example.test/x", pub, auth},
		"ftp":         {"ftp://push.example.test/x", pub, auth},
		"long":        {"https://push.example.test/" + strings.Repeat("x", maxEndpoint), pub, auth},
		"bad p256dh":  {"https://push.example.test/x", "!!", auth},
		"not a point": {"https://push.example.test/x", base64.RawURLEncoding.EncodeToString([]byte{4, 1, 2}), auth},
		"short auth":  {"https://push.example.test/x", pub, "AAAA"},
	} {
		if _, err := svc.Subscribe(ctx, c[0], c[1], c[2], "x"); err == nil {
			t.Errorf("%s: subscribed", name)
		}
	}
	svc.o.AllowHTTP = false
	if _, err := svc.Subscribe(ctx, "http://push.example.test/x", pub, auth, ""); err == nil {
		t.Fatal("http accepted without AllowHTTP")
	}
	for i := range maxDevices {
		if _, err := svc.Subscribe(ctx, "https://push.example.test/"+string(rune('a'+i)), pub, auth, strings.Repeat("l", 100)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := svc.Subscribe(ctx, "https://push.example.test/one-too-many", pub, auth, ""); err == nil {
		t.Fatal("more than maxDevices accepted")
	}
	if _, err := svc.Subscribe(ctx, "https://push.example.test/a", pub, auth, "renamed"); err != nil {
		t.Fatalf("re-subscribing a known endpoint at the cap: %v", err)
	}
	devices, _ := svc.Devices(ctx)
	if len(devices[1].Label) != 80 {
		t.Fatalf("label not capped: %q", devices[1].Label)
	}
	removed, err := svc.Remove(ctx, devices[0].ID)
	if err != nil || !removed {
		t.Fatalf("Remove = %v %v", removed, err)
	}
	if removed, _ := svc.Remove(ctx, "nope"); removed {
		t.Fatal("removed an unknown device")
	}
}

func TestNewChecks(t *testing.T) {
	ctx := context.Background()
	if _, err := New(ctx, Options{}); err == nil {
		t.Fatal("New with nothing")
	}
	_, s, _ := newService(t)
	if _, err := New(ctx, Options{Store: s, Keys: &memKeys{m: map[string][]byte{}}, Contact: "admin@example.test"}); err == nil {
		t.Fatal("a contact that is not mailto: or https: must fail")
	}
	bad := &memKeys{m: map[string][]byte{KeySecret: {1, 2, 3}}}
	if _, err := New(ctx, Options{Store: s, Keys: bad, Contact: "mailto:x@example.test"}); err == nil {
		t.Fatal("a broken stored key must fail")
	}
}
