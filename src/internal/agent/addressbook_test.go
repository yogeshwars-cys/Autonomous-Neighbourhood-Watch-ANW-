package agent

import "testing"

func TestAddressBookAddReportsNewVsExisting(t *testing.T) {
	ab := NewAddressBook()

	isNew := ab.Add("node-b", "127.0.0.1:9002")
	if !isNew {
		t.Error("expected first Add of node-b to report new=true")
	}

	isNew = ab.Add("node-b", "127.0.0.1:9002")
	if isNew {
		t.Error("expected second Add of the same ID to report new=false")
	}

	// Re-adding with a DIFFERENT address (peer moved/restarted) still
	// isn't "new" in the discovery sense, but should still update.
	ab.Add("node-b", "127.0.0.1:9999")
	addr, ok := ab.Get("node-b")
	if !ok || addr != "127.0.0.1:9999" {
		t.Errorf("expected address to refresh to new value, got %q", addr)
	}
}

func TestAddressBookGetMissingID(t *testing.T) {
	ab := NewAddressBook()
	_, ok := ab.Get("node-ghost")
	if ok {
		t.Error("expected Get on unknown ID to report not-found")
	}
}

func TestAddressBookAllReturnsIndependentCopy(t *testing.T) {
	ab := NewAddressBook()
	ab.Add("node-a", "127.0.0.1:9001")

	snapshot := ab.All()
	snapshot["node-a"] = "tampered"

	addr, _ := ab.Get("node-a")
	if addr != "127.0.0.1:9001" {
		t.Errorf("mutating a snapshot from All() should not affect the AddressBook, got %q", addr)
	}
}

func TestAddressBookCount(t *testing.T) {
	ab := NewAddressBook()
	if ab.Count() != 0 {
		t.Errorf("expected empty AddressBook to have count 0, got %d", ab.Count())
	}
	ab.Add("node-a", "127.0.0.1:9001")
	ab.Add("node-b", "127.0.0.1:9002")
	if ab.Count() != 2 {
		t.Errorf("expected count 2, got %d", ab.Count())
	}
}
