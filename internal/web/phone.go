// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/ruvido/homewend/internal/engine"
	"github.com/ruvido/homewend/internal/library"
)

// maxHave is how many hashes one /v1/have asks about (docs/phone.md), and
// maxAsk the bytes a call's JSON may take: anyone on the network can call.
const (
	maxHave = 1000
	maxAsk  = 128 << 10
)

// phoneListener is the second listener, on the local network, for the
// phones' calls only: nothing of /api/ is reachable from it. Over TLS with
// the computer's own certificate, which the phone pins by its key.
type phoneListener struct {
	ln   net.Listener
	srv  *http.Server
	port int
	pin  []byte // SHA-256 of the certificate's public key
}

// listenPhones binds the port kept in phones.json, or a free one the first
// time, kept so the QR's address stays true across restarts.
func listenPhones() (*phoneListener, error) {
	cert, pin, err := engine.PhoneCert()
	if err != nil {
		return nil, err
	}
	p, err := engine.LoadPhones()
	if err != nil {
		return nil, err
	}
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", p.Port))
	if err != nil && p.Port != 0 {
		// Taken by another program since: a new port, and the phones find
		// the computer again by pairing.
		ln, err = net.Listen("tcp", ":0")
	}
	if err != nil {
		return nil, err
	}
	port := ln.Addr().(*net.TCPAddr).Port
	if port != p.Port {
		if _, err := engine.UpdatePhones(func(p *engine.Phones) error { p.Port = port; return nil }); err != nil {
			ln.Close()
			return nil, err
		}
	}
	l := &phoneListener{port: port, pin: pin,
		ln: tls.NewListener(ln, &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS13})}
	// No ReadTimeout: a video takes minutes. An idle connection is closed.
	l.srv = &http.Server{Handler: phoneRoutes(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: time.Minute}
	return l, nil
}

// pairText is what the QR says: the addresses, the key's pin, the code.
func (l *phoneListener) pairText(code string) string {
	var hosts []string
	for _, ip := range localAddresses() {
		hosts = append(hosts, net.JoinHostPort(ip, strconv.Itoa(l.port)))
	}
	q := url.Values{"h": {strings.Join(hosts, ",")},
		"k": {base64.RawURLEncoding.EncodeToString(l.pin)}, "c": {code}}
	return "homewend://pair?" + q.Encode()
}

// localAddresses are this computer's IPv4 addresses on the networks it is
// on, the loopback left out.
func localAddresses() []string {
	var ips []string
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	for _, i := range ifaces {
		if i.Flags&net.FlagUp == 0 || i.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := i.Addrs()
		for _, a := range addrs {
			if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil {
				ips = append(ips, n.IP.String())
			}
		}
	}
	return ips
}

func phoneRoutes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/pair", func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Code, Name string }
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxAsk)).Decode(&in); err != nil || in.Name == "" {
			phoneError(w, http.StatusBadRequest, "a code and a name are needed")
			return
		}
		phone, token, err := engine.Pair(in.Code, in.Name)
		if err != nil {
			phoneReply(w, nil, err)
			return
		}
		host, _ := os.Hostname()
		phoneReply(w, map[string]string{"phone": phone.ID, "token": token, "computer": host}, nil)
	})
	mux.Handle("POST /v1/have", paired(func(w http.ResponseWriter, r *http.Request, _ engine.Phone) {
		var in struct{ Hashes []string }
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxAsk)).Decode(&in); err != nil || len(in.Hashes) > maxHave {
			phoneError(w, http.StatusBadRequest, fmt.Sprintf("at most %d hashes", maxHave))
			return
		}
		have, out, err := engine.Have(in.Hashes)
		phoneReply(w, map[string][]string{"have": have, "out": out}, err)
	}))
	mux.Handle("PUT /v1/photo/{hash}", paired(func(w http.ResponseWriter, r *http.Request, phone engine.Phone) {
		hash := r.PathValue("hash")
		if b, err := hex.DecodeString(hash); err != nil || len(b) != 32 || strings.ToLower(hash) != hash {
			phoneError(w, http.StatusBadRequest, "the hash is SHA-256 in lowercase hex")
			return
		}
		// A file's name, not a path: "..", "." or one with a slash is refused.
		name := r.URL.Query().Get("name")
		if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`) || len(name) > 255 || r.ContentLength < 0 {
			phoneError(w, http.StatusBadRequest, "a file's name and a Content-Length are needed")
			return
		}
		// A kind of file the library does not take is said before its bytes
		// are sent, not sent again at every backup.
		if !library.IsMedia(name) {
			phoneError(w, http.StatusUnsupportedMediaType, "not a photo or a video the library takes")
			return
		}
		got, err := engine.ReceivePhoto(r.Context(), phone, hash, name, r.ContentLength, r.Body)
		if err == nil && got.New {
			phoneJSON(w, http.StatusCreated, got)
			return
		}
		phoneReply(w, got, err)
	}))
	return mux
}

// paired lets in only a request with the token of a paired phone.
func paired(next func(http.ResponseWriter, *http.Request, engine.Phone)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok {
			phoneError(w, http.StatusUnauthorized, engine.ErrNoPhone.Error())
			return
		}
		phone, err := engine.PhoneOf(token)
		if err != nil {
			phoneReply(w, nil, err)
			return
		}
		next(w, r, phone)
	})
}

// phoneReply answers a phone: JSON, or an error as {"error": …} with its
// status (docs/phone.md, "The contract").
func phoneReply(w http.ResponseWriter, v any, err error) {
	switch {
	case err == nil:
		phoneJSON(w, http.StatusOK, v)
	case errors.Is(err, engine.ErrBadCode):
		phoneError(w, http.StatusForbidden, err.Error())
	case errors.Is(err, engine.ErrNoPhone):
		phoneError(w, http.StatusUnauthorized, err.Error())
	case errors.Is(err, engine.ErrNotThisHash):
		phoneError(w, http.StatusUnprocessableEntity, err.Error())
	case errors.As(err, new(engine.NoSpaceError)):
		phoneError(w, http.StatusInsufficientStorage, err.Error())
	case errors.Is(err, engine.ErrLibraryBusy):
		phoneError(w, http.StatusConflict, err.Error())
	case errors.Is(err, engine.ErrNoLibrary), errors.As(err, new(engine.LibraryGoneError)):
		phoneError(w, http.StatusServiceUnavailable, err.Error())
	default:
		log.Printf("phone: %v", err)
		phoneError(w, http.StatusInternalServerError, err.Error())
	}
}

func phoneError(w http.ResponseWriter, status int, msg string) {
	phoneJSON(w, status, map[string]string{"error": msg})
}

func phoneJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
