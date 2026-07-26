package vault

import (
	"reflect"
	"sync"
	"testing"
)

// Regression: ReadSecret used to hand out the cached map itself, so a caller
// mutating it was an unsynchronised write racing every other reader — "fatal
// error: concurrent map writes", which this package's RWMutex cannot prevent
// because the write happens outside the package.

func TestReadSecretReturnsACopy(t *testing.T) {
	fv := fixture()
	c := newTestClient(t, fv)

	first, err := c.ReadSecret(t.Context(), MountClusterSecrets, "host-sea1-core-1")
	if err != nil {
		t.Fatalf("ReadSecret: %v", err)
	}

	first["admin-password"] = "tampered"
	first["injected"] = true
	delete(first, "enable-password")

	second, err := c.ReadSecret(t.Context(), MountClusterSecrets, "host-sea1-core-1")
	if err != nil {
		t.Fatalf("ReadSecret (cached): %v", err)
	}
	if got := second["admin-password"]; got != "pw" {
		t.Fatalf("the cache was mutated through a returned map: admin-password = %v", got)
	}
	if _, ok := second["injected"]; ok {
		t.Fatal("a caller's key leaked into the cache")
	}
	if got := second["enable-password"]; got != "en" {
		t.Fatalf("a caller's delete leaked into the cache: enable-password = %v", got)
	}
	// Map identity, not the identity of two distinct local variables:
	// &first == &second is always false and asserts nothing.
	if reflect.ValueOf(first).Pointer() == reflect.ValueOf(second).Pointer() {
		t.Fatal("the same map was handed out twice")
	}

	// Get reads through the same cache and must be unaffected too.
	value, err := c.Get(t.Context(), MountClusterSecrets, "host-sea1-core-1", "admin-password")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if value != "pw" {
		t.Fatalf("Get returned %q, want %q", value, "pw")
	}
}

// The -race companion: before the fix this was an unsynchronised write to a
// map several goroutines were ranging over.
func TestConcurrentReadersAndAMutatingCaller(t *testing.T) {
	fv := fixture()
	c := newTestClient(t, fv)

	// Warm the cache so every goroutine below takes the cached path.
	if _, err := c.ReadSecret(t.Context(), MountClusterSecrets, "host-sea1-core-1"); err != nil {
		t.Fatalf("warming: %v", err)
	}

	var wg sync.WaitGroup
	for i := range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			data, err := c.ReadSecret(t.Context(), MountClusterSecrets, "host-sea1-core-1")
			if err != nil {
				t.Errorf("reader %d: %v", i, err)
				return
			}
			if i%2 == 0 {
				data["mine"] = i
				delete(data, "ttl")
				return
			}
			for k := range data {
				_ = data[k]
			}
		}()
	}
	wg.Wait()

	final, err := c.ReadSecret(t.Context(), MountClusterSecrets, "host-sea1-core-1")
	if err != nil {
		t.Fatalf("final read: %v", err)
	}
	if len(final) != 3 {
		t.Fatalf("cache entry has %d keys after concurrent mutation, want 3: %v", len(final), final)
	}
}

// cacheMint must copy on write: a minted key lands in the cache without
// either side aliasing the other.
func TestMintDoesNotAliasIntoACallersMap(t *testing.T) {
	fv := fixture()
	srv := newTestClient(t, fv)
	srv.allowMint = true

	before, err := srv.ReadSecret(t.Context(), MountClusterSecrets, "host-sea1-core-1")
	if err != nil {
		t.Fatalf("ReadSecret: %v", err)
	}

	srv.cacheMint(MountClusterSecrets, "host-sea1-core-1", "tacacs-key", "minted")

	if _, ok := before["tacacs-key"]; ok {
		t.Fatal("minting reached into a map a caller was already holding")
	}
	after, err := srv.ReadSecret(t.Context(), MountClusterSecrets, "host-sea1-core-1")
	if err != nil {
		t.Fatalf("ReadSecret after mint: %v", err)
	}
	if after["tacacs-key"] != "minted" {
		t.Fatalf("the minted key is not cached: %v", after)
	}
}
