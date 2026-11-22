// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/ruvido/homewend/internal/engine"
)

// A phone pairs with the code, then asks and sends as docs/phone.md says,
// each answer the status the contract names.
func TestAPhoneTalksAsTheContractSays(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("AppData", cfg)
	lib := t.TempDir()
	if err := engine.SetDefaultLibrary(lib); err != nil {
		t.Fatal(err)
	}
	h := phoneRoutes(&phoneListener{port: 1})
	call := func(method, target, token, body string) (int, map[string]any) {
		r := httptest.NewRequest(method, target, strings.NewReader(body))
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		var out map[string]any
		json.NewDecoder(w.Body).Decode(&out)
		return w.Code, out
	}

	if code, _ := call("POST", "/v1/pair", "", `{"code":"nope","name":"Pixel"}`); code != http.StatusForbidden {
		t.Errorf("a wrong code: %d, want 403", code)
	}
	pairCode, err := engine.StartPairing("ron")
	if err != nil {
		t.Fatal(err)
	}
	code, paired := call("POST", "/v1/pair", "", `{"code":"`+pairCode+`","name":"Pixel"}`)
	token, _ := paired["token"].(string)
	if code != http.StatusOK || token == "" || paired["phone"] == "" || paired["owner"] != "ron" {
		t.Fatalf("pairing: %d %v", code, paired)
	}
	if code, _ := call("POST", "/v1/have", "", `{"hashes":[]}`); code != http.StatusUnauthorized {
		t.Errorf("no token: %d, want 401", code)
	}

	photo := "the phone's photo"
	sum := sha256.Sum256([]byte(photo))
	hash := hex.EncodeToString(sum[:])
	if code, _ := call("PUT", "/v1/photo/"+hash+"?name=IMG_1.JPG", token, "not it"); code != http.StatusUnprocessableEntity {
		t.Errorf("wrong bytes: %d, want 422", code)
	}
	if code, out := call("PUT", "/v1/photo/"+hash+"?name=IMG_1.JPG", token, photo); code != http.StatusCreated || out["path"] == "" {
		t.Errorf("sent: %d %v, want 201 and its path", code, out)
	}
	if code, out := call("PUT", "/v1/photo/"+hash+"?name=IMG_1.JPG", token, photo); code != http.StatusOK || out["path"] == "" {
		t.Errorf("sent again: %d %v, want 200 and its path", code, out)
	}
	code, have := call("POST", "/v1/have", token, `{"hashes":["`+hash+`"]}`)
	if list, _ := have["have"].([]any); code != http.StatusOK || len(list) != 1 {
		t.Errorf("have: %d %v", code, have)
	}
	if code, out := call("POST", "/v1/archive", token, `{"hashes":["`+hash+`"]}`); code != http.StatusOK || len(out["archived"].([]any)) != 1 {
		t.Errorf("archive: %d %v", code, out)
	}
	if _, have := call("POST", "/v1/have", token, `{"hashes":["`+hash+`"]}`); len(have["archived"].([]any)) != 1 {
		t.Errorf("have after archive: %v", have)
	}
	if code, out := call("POST", "/v1/unarchive", token, `{"hashes":["`+hash+`"]}`); code != http.StatusOK || len(out["unarchived"].([]any)) != 1 {
		t.Errorf("unarchive: %d %v", code, out)
	}
	if code, out := call("POST", "/v1/trash", token, `{"hashes":["`+hash+`"]}`); code != http.StatusOK || len(out["trashed"].([]any)) != 1 {
		t.Errorf("trash: %d %v", code, out)
	}
	if _, have := call("POST", "/v1/have", token, `{"hashes":["`+hash+`"]}`); len(have["out"].([]any)) != 1 {
		t.Errorf("have after trash: %v, want it out", have)
	}
	if code, out := call("POST", "/v1/untrash", token, `{"hashes":["`+hash+`"]}`); code != http.StatusOK || len(out["untrashed"].([]any)) != 1 {
		t.Errorf("untrash: %d %v", code, out)
	}
	if code, _ := call("PUT", "/v1/photo/"+strings.ToUpper(hash)+"?name=IMG_1.JPG", token, photo); code != http.StatusBadRequest {
		t.Errorf("an uppercase hash: %d, want 400", code)
	}
	if code, out := call("GET", "/v1/hello", token, ""); code != http.StatusOK || out["computer"] == "" || out["owner"] != "ron" || out["hosts"] == nil || out["photos"] != float64(1) || !hasDisk(out) {
		t.Errorf("hello: %d %v", code, out)
	}
	// The library's disk away: hello still answers, without the disk.
	if err := os.Rename(lib, lib+"-away"); err != nil {
		t.Fatal(err)
	}
	if code, out := call("GET", "/v1/hello", token, ""); code != http.StatusOK || out["disk"] != nil {
		t.Errorf("hello, the disk away: %d %v, want 200 without the disk", code, out)
	}
	if err := os.Rename(lib+"-away", lib); err != nil {
		t.Fatal(err)
	}
	if code, _ := call("POST", "/v1/unpair", token, ""); code != http.StatusOK {
		t.Errorf("unpair: %d, want 200", code)
	}
	if code, _ := call("GET", "/v1/hello", token, ""); code != http.StatusUnauthorized {
		t.Errorf("hello after unpairing: %d, want 401", code)
	}
}

// hasDisk is whether a hello answered the library's disk, with a size.
func hasDisk(out map[string]any) bool {
	disk, _ := out["disk"].(map[string]any)
	total, _ := disk["total"].(float64)
	return total > 0
}
