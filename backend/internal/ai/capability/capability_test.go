// Checks capability tokens round-trip, reject wrong secrets, malformed and expired tokens,
// and never panic on arbitrary input.

package capability

import (
	"testing"
	"time"
)

func TestMintAndVerify(t *testing.T) {
	expires := time.Now().Add(time.Minute)
	token, err := Mint("secret", "request", expires)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := Verify("secret", token, time.Now()); err != nil || got != "request" {
		t.Fatalf("Verify=%q,%v", got, err)
	}
	for _, test := range []struct {
		secret, token string
		now           time.Time
	}{{"bad", token, time.Now()}, {"secret", "bad", time.Now()}, {"secret", token, expires.Add(time.Second)}} {
		if _, err := Verify(test.secret, test.token, test.now); err == nil {
			t.Fatal("invalid capability accepted")
		}
	}
	if _, err := Mint("", "request", expires); err == nil {
		t.Fatal("empty secret accepted")
	}
}

func FuzzVerify(f *testing.F) {
	valid, err := Mint("secret", "request", time.Unix(2_000_000_000, 0))
	if err != nil {
		f.Fatal(err)
	}
	f.Add(valid)
	f.Add("not-a-capability")
	f.Fuzz(func(t *testing.T, token string) {
		requestID, err := Verify("secret", token, time.Unix(1_900_000_000, 0))
		if err == nil && requestID == "" {
			t.Fatal("accepted capability has an empty request ID")
		}
	})
}
