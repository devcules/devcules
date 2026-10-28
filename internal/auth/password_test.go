package auth

import (
	"strings"
	"testing"
)

func TestGeneratePassword(t *testing.T) {
	seen := map[string]bool{}
	for range 50 {
		p, err := GeneratePassword()
		if err != nil {
			t.Fatalf("GeneratePassword: %s", err)
		}
		if len(p) != GeneratedPasswordLength {
			t.Fatalf("length = %d, want %d", len(p), GeneratedPasswordLength)
		}
		for _, c := range p {
			if !strings.ContainsRune(passwordAlphabet, c) {
				t.Fatalf("password %q contains %q, outside the unambiguous alphabet", p, c)
			}
		}
		seen[p] = true
	}
	if len(seen) != 50 {
		t.Fatalf("%d distinct passwords out of 50, want 50", len(seen))
	}
}

func TestPasswordRoundTrip(t *testing.T) {
	encoded, err := HashPassword("correct horse")
	if err != nil {
		t.Fatalf("HashPassword: %s", err)
	}
	if !strings.HasPrefix(encoded, "$argon2id$v=19$") {
		t.Fatalf("hash %q does not carry the expected PHC prefix", encoded)
	}
	if !CheckPassword(encoded, "correct horse") {
		t.Fatal("CheckPassword rejects the password it just hashed")
	}
	if CheckPassword(encoded, "correct horsf") {
		t.Fatal("CheckPassword accepts a wrong password")
	}
	// Two hashes of one password differ: the salt is random.
	again, err := HashPassword("correct horse")
	if err != nil {
		t.Fatalf("HashPassword: %s", err)
	}
	if encoded == again {
		t.Fatal("two hashes of one password are identical: the salt is not random")
	}
}

func TestPasswordRejectsGarbage(t *testing.T) {
	good, err := HashPassword("password")
	if err != nil {
		t.Fatalf("HashPassword: %s", err)
	}
	parts := strings.Split(good, "$")
	cases := map[string]string{
		"empty":           "",
		"not a hash":      "definitely-not-a-hash",
		"too few parts":   "$argon2id$v=19$only",
		"wrong algorithm": strings.Replace(good, "$argon2id$", "$scrypt$", 1),
		"old version":     strings.Replace(good, "$v=19$", "$v=18$", 1),
		"weak memory":     "$argon2id$v=19$m=8,t=1,p=4$" + parts[4] + "$" + parts[5],
		"bad salt base64": parts[0] + "$" + parts[1] + "$" + parts[2] + "$" + parts[3] + "$!!$" + parts[5],
		"short hash":      parts[0] + "$" + parts[1] + "$" + parts[2] + "$" + parts[3] + "$" + parts[4] + "$eA",
	}
	for name, encoded := range cases {
		if CheckPassword(encoded, "password") {
			t.Errorf("CheckPassword(%s) accepts %q", name, encoded)
		}
	}
}

func TestDummyPassword(t *testing.T) {
	// No assertion on the result: the call exists for its duration,
	// proving unknown usernames cost one verification. It must not
	// panic and must not accept anything.
	CheckDummyPassword()
}
