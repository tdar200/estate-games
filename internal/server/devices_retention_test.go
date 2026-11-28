package server

import (
	"testing"
	"time"
)

// A device not seen for a year is deleted with the friend links others keep to
// it; a device seen within the year, and its own record, stay.
func TestDevicesDeletedAfterAYearUnseen(t *testing.T) {
	h := newHarness(t)
	old := h.device("Old", "rocket", "198.51.100.201")
	fresh := h.device("Fresh", "teapot", "198.51.100.202")
	h.srv.mu.Lock()
	now := h.srv.now()
	h.srv.devices[old.id].LastSeenAt = now.Add(-deviceRetention - time.Hour).UnixMilli()
	h.srv.devices[old.id].Friends = map[string]*friendLink{fresh.id: {Since: 1, Games: 1, Name: "Fresh"}}
	h.srv.devices[fresh.id].LastSeenAt = now.Add(-deviceRetention + 24*time.Hour).UnixMilli()
	h.srv.devices[fresh.id].Friends = map[string]*friendLink{old.id: {Since: 1, Games: 1, Name: "Old"}}
	h.srv.pruneDevices(now)
	_, oldKept := h.srv.devices[old.id]
	freshRec := h.srv.devices[fresh.id]
	h.srv.mu.Unlock()
	if oldKept {
		t.Fatal("a device unseen for over a year was kept")
	}
	if freshRec == nil {
		t.Fatal("a device seen within the year was deleted")
	}
	if freshRec.Friends[old.id] != nil {
		t.Fatal("the friend link to a deleted device was kept")
	}
}
