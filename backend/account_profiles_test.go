package main

import (
	"encoding/hex"
	"strings"
	"testing"
)

func TestAccountProfileMySQL(t *testing.T) {
	f := moderationDB(t)
	handle := "profile-" + hex.EncodeToString(f.user[:5])
	payload := `{"displayId":"` + strings.ToUpper(handle) + `","displayName":"配信テスト"}`
	if w := f.call("POST", "/account/profile", "", payload); w.Code != 401 {
		t.Fatal(w.Code)
	}
	if w := f.call("POST", "/account/profile", f.userToken, payload); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := f.call("POST", "/account/profile", f.guestToken, payload); w.Code != 409 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := f.call("GET", "/account/profile", f.guestToken, ""); w.Code != 200 || strings.Contains(w.Body.String(), handle) {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, path := range []string{"/public/profiles/" + handle, "/public/users/" + hex.EncodeToString(f.user)} {
		if w := f.call("GET", path, "", ""); w.Code != 200 || !strings.Contains(w.Body.String(), "配信テスト") || !strings.Contains(w.Body.String(), hex.EncodeToString(f.user)) {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	newHandle := "renamed-" + hex.EncodeToString(f.user[:5])
	if w := f.call("POST", "/account/profile", f.userToken, `{"displayId":"`+newHandle+`","displayName":"Renamed"}`); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := f.call("GET", "/public/profiles/"+handle, "", ""); w.Code != 404 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := f.call("GET", "/public/users/"+hex.EncodeToString(f.user), "", ""); w.Code != 200 || !strings.Contains(w.Body.String(), newHandle) {
		t.Fatal(w.Code, w.Body.String())
	}
}
