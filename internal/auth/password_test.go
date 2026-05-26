package auth

import "testing"

func TestPasswordMatchesHashedPassword(t *testing.T) {
	hash, err := hashPassword("secret-password")
	if err != nil {
		t.Fatalf("hashPassword failed: %v", err)
	}

	if hash == "secret-password" {
		t.Fatal("hashPassword returned the plaintext password")
	}

	if !isPasswordHash(hash) {
		t.Fatal("hashPassword did not return a password hash")
	}

	if !passwordMatches(hash, "secret-password") {
		t.Fatal("hashed password should match")
	}

	if passwordMatches(hash, "wrong-password") {
		t.Fatal("wrong password should not match hash")
	}
}

func TestPasswordMatchesLegacyPlaintext(t *testing.T) {
	if !passwordMatches("legacy-password", "legacy-password") {
		t.Fatal("legacy plaintext password should match")
	}

	if passwordMatches("legacy-password", "wrong-password") {
		t.Fatal("wrong password should not match legacy plaintext")
	}

	if isPasswordHash("legacy-password") {
		t.Fatal("plain legacy password should not be detected as a hash")
	}
}
