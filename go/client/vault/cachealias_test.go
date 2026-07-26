package vault

import (
	"sync"
	"testing"
)

// Regression test for finding 7: ReadSecret used to return the cached map
// itself (and, on a cold read, secret.Data, which then became the cache
// entry). Every caller therefore held a reference to one shared map. A
// caller adding or deleting a key is an unsynchronised map write racing
// every other reader, which Go turns into "fatal error: concurrent map
// writes" — a process-killing crash that the RWMutex in this package
// cannot prevent, because the write happens outside the package. cacheMint
// already copies on write precisely because it knew this.

func TestReadSecretReturnsACopy(t *testing.T) {
	fv := fixture()
	c := newTestClient(t, fv)

	first, err := c.ReadSecret(t.Context(), MountClusterSecrets, "host-sea1-core-1")
	if err != nil {
		t.Fatalf("ReadSecret: %v", err)
	}

	// A caller does what callers do with a map they were handed.
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
	if &first == &second {
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

// TestConcurrentReadersAndAMutatingCaller is the -race companion: one
// caller mutating what it was given must not be able to race the readers.
// Before the fix this was an unsynchronised write to a map several
// goroutines were ranging over.
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
				// Half the callers mutate their copy...
				data["mine"] = i
				delete(data, "ttl")
				return
			}
			// ...while the other half read theirs.
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

// TestMintDoesNotAliasIntoACallersMap keeps cacheMint's copy-on-write
// honest now that ReadSecret clones: a minted key must land in the cache
// without either side aliasing the other.
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
