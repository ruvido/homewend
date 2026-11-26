// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/mdns"
	"github.com/ruvido/homewend/internal/engine"
	"github.com/ruvido/homewend/internal/library"
)

// maxHave is how many hashes one call asks about (docs/phone.md), and
// maxAsk the bytes a call's JSON may take: anyone on the network can call.
const (
	maxHave = 1000
	maxAsk  = 128 << 10
)

// features are the calls a phone may find, or not on an older computer.
var features = []string{"marks", "edits", "library"}

// phoneListener is the second listener, on the local network, for the
// phones' calls only: nothing of /api/ is reachable from it. Over TLS with
// the computer's own certificate, which the phone pins by its key.
type phoneListener struct {
	ln   net.Listener
	srv  *http.Server
	port int
	pin  []byte // SHA-256 of the certificate's public key
	// The listener said on the local network by DNS-SD, for a phone whose
	// addresses no longer answer; nil when multicast could not start.
	mdns *mdns.Server
}

// phonePorts are the only ports the phones' listener takes, in this order
// (docs/phone.md, "The port"): a phone that does not find the computer where
// it was tries them all.
var phonePorts = []int{45483, 45484, 45485, 45486}

// portWait is how long the ports are tried again: a Homewend closing still
// holds its own for a moment.
var portWait = 5 * time.Second

// ErrPortsTaken is every port of the list taken by other programs.
var ErrPortsTaken = errors.New("phones can't reach this computer: ports 45483–45486 are in use")

// listenOn takes the kept port if it is free, else the first free one of
// phonePorts, never another.
func listenOn(kept int) (net.Listener, error) {
	ports := phonePorts
	if slices.Contains(ports, kept) {
		ports = append([]int{kept}, slices.DeleteFunc(slices.Clone(ports), func(p int) bool { return p == kept })...)
	}
	for end := time.Now().Add(portWait); ; time.Sleep(portWait / 20) {
		for _, port := range ports {
			if ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port)); err == nil {
				return ln, nil
			}
		}
		if time.Now().After(end) {
			return nil, ErrPortsTaken
		}
	}
}

// service is the DNS-SD type the listener is advertised as (docs/phone.md).
const service = "_homewend._tcp"

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
	ln, err := listenOn(p.Port)
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
		ln: tls.NewListener(phoneConns{ln}, &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS13})}
	// No ReadTimeout: a video takes minutes. An idle connection is closed.
	l.srv = &http.Server{Handler: phoneRoutes(l), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: time.Minute}
	// shortcut: the addresses advertised are those at start; a new address
	// is advertised after the app starts again, meanwhile /v1/hello tells it.
	var ips []net.IP
	for _, a := range localAddresses() {
		ips = append(ips, net.ParseIP(a))
	}
	// No network yet: nothing is advertised rather than the loopback the
	// library would look up instead.
	if len(ips) == 0 {
		log.Printf("phones: not advertised on the network: no address yet")
		return l, nil
	}
	// The instance is the host's first label: a dot in it ("Mac.local", a
	// domain) would be read as part of the service type. Its address is
	// said under .local, where a phone's resolver looks.
	host, _ := os.Hostname()
	inst, _, _ := strings.Cut(host, ".")
	zone, err := mdns.NewMDNSService(inst, service, "", inst+".local.", port, ips, []string{"k=" + l.key()})
	if err == nil {
		// The library logs every odd packet on the network: not ours to read.
		l.mdns, err = mdns.NewServer(&mdns.Config{Zone: zone, Logger: log.New(io.Discard, "", 0)})
	}
	if err != nil {
		// The phone still finds the computer at the addresses it knows.
		log.Printf("phones: not advertised on the network: %v", err)
	}
	return l, nil
}

// phoneConns closes a connection whose write was given up: TLS cannot go on
// after it, and would wait 5 seconds more for its goodbye to be taken.
type phoneConns struct{ net.Listener }

func (l phoneConns) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	return phoneConn{c}, err
}

type phoneConn struct{ net.Conn }

func (c phoneConn) Write(b []byte) (int, error) {
	n, err := c.Conn.Write(b)
	if errors.Is(err, os.ErrDeadlineExceeded) {
		c.Conn.Close()
	}
	return n, err
}

// pairText is what the QR says: the addresses, the key's pin, the code.
func (l *phoneListener) pairText(code string) string {
	q := url.Values{"h": {strings.Join(l.hosts(), ",")}, "k": {l.key()}, "c": {code}}
	return "homewend://pair?" + q.Encode()
}

// hosts are where a phone reaches the listener now: every network's address.
func (l *phoneListener) hosts() []string {
	hosts := []string{}
	for _, ip := range localAddresses() {
		hosts = append(hosts, net.JoinHostPort(ip, strconv.Itoa(l.port)))
	}
	return hosts
}

// key is the pin a phone checks, as the QR and DNS-SD say it.
func (l *phoneListener) key() string { return base64.RawURLEncoding.EncodeToString(l.pin) }

func (l *phoneListener) shutdown(ctx context.Context) error {
	if l.mdns != nil {
		l.mdns.Shutdown()
	}
	return l.srv.Shutdown(ctx)
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

// heard logs a phone's hello when it comes back after a silence, and now and
// then while it stays: whether a phone reached the computer, and from where,
// without a line every half minute.
func heard(phone engine.Phone, from string) {
	hellos.Lock()
	defer hellos.Unlock()
	now := time.Now()
	last := hellos.at[phone.ID]
	hellos.at[phone.ID] = now
	if now.Sub(last) > time.Minute || now.Sub(hellos.said[phone.ID]) > 10*time.Minute {
		hellos.said[phone.ID] = now
		log.Printf("phone %q: hello from %s", phone.Name, from)
	}
}

var hellos = struct {
	sync.Mutex
	at, said map[string]time.Time
}{at: map[string]time.Time{}, said: map[string]time.Time{}}

// askedHashes reads the hashes a phone sends, at most maxHave, and answers
// 400 when they are not that.
func askedHashes(w http.ResponseWriter, r *http.Request) ([]string, bool) {
	var in struct{ Hashes []string }
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxAsk)).Decode(&in); err != nil || len(in.Hashes) > maxHave {
		phoneError(w, http.StatusBadRequest, fmt.Sprintf("at most %d hashes", maxHave))
		return nil, false
	}
	return in.Hashes, true
}

// askedHash is the hash a call names, and answers 400 when it is not one.
func askedHash(w http.ResponseWriter, r *http.Request) (string, bool) {
	hash := r.PathValue("hash")
	if b, err := hex.DecodeString(hash); err != nil || len(b) != 32 || strings.ToLower(hash) != hash {
		phoneError(w, http.StatusBadRequest, "the hash is SHA-256 in lowercase hex")
		return "", false
	}
	return hash, true
}

func phoneRoutes(l *phoneListener) http.Handler {
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
		phoneReply(w, map[string]any{"phone": phone.ID, "token": token, "computer": host, "owner": phone.Profile, "ownerId": phone.OwnerID, "features": features}, nil)
	})
	// The phone asks, when it opens, whether it is still paired, with whom,
	// and where the computer is now: an address changed meanwhile heals.
	mux.Handle("GET /v1/hello", paired(func(w http.ResponseWriter, r *http.Request, phone engine.Phone) {
		heard(phone, r.RemoteAddr)
		host, _ := os.Hostname()
		hello := map[string]any{"computer": host, "owner": phone.Profile, "ownerId": phone.OwnerID, "hosts": l.hosts(), "features": features}
		// The library unanswered (its disk away, a catalog not read): hello
		// still says the phone is paired and where the computer is.
		if lib, err := engine.LibraryOf(phone); err == nil && lib.Known {
			hello["disk"] = map[string]int64{"free": lib.Free, "total": lib.Total,
				"photos": lib.Bytes[""], "raw": lib.Bytes["raw"], "videos": lib.Bytes["video"]}
			hello["photos"] = lib.Photos
		}
		phoneReply(w, hello, nil)
	}))
	// Unpaired from the phone: the computer forgets it as from its own menu.
	mux.Handle("POST /v1/unpair", paired(func(w http.ResponseWriter, r *http.Request, phone engine.Phone) {
		phoneReply(w, struct{}{}, engine.Unpair(phone.ID, "phone"))
	}))
	mux.Handle("POST /v1/have", paired(func(w http.ResponseWriter, r *http.Request, phone engine.Phone) {
		hashes, ok := askedHashes(w, r)
		if !ok {
			return
		}
		held, err := engine.Have(phone, hashes)
		phoneReply(w, held, err)
	}))
	mux.Handle("POST /v1/marks", paired(func(w http.ResponseWriter, r *http.Request, phone engine.Phone) {
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxAsk))
		if errors.As(err, new(*http.MaxBytesError)) {
			phoneError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("at most %d bytes", maxAsk))
			return
		}
		var q library.MarkQuestion
		if err != nil || json.Unmarshal(body, &q) != nil {
			phoneReply(w, nil, library.ErrNotMarks)
			return
		}
		answer, err := engine.Marks(phone, q)
		phoneReply(w, answer, err)
	}))
	// Held until there is something to hear, or for a while.
	mux.Handle("GET /v1/marks", paired(func(w http.ResponseWriter, r *http.Request, phone engine.Phone) {
		after, err := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
		if err != nil || after < 0 {
			phoneReply(w, nil, library.ErrNotMarks)
			return
		}
		var states []library.State
		for _, s := range strings.FieldsFunc(r.URL.Query().Get("states"), func(r rune) bool { return r == ',' }) {
			states = append(states, library.State(s))
		}
		var list *string
		if r.URL.Query().Has("list") {
			list = new(r.URL.Query().Get("list"))
		}
		changed, listed, err := engine.WaitForMarks(r.Context(), phone, r.URL.Query().Get("shelf"), after, states, list)
		answer := map[string]bool{"changed": changed}
		if listed {
			answer["list"] = true
		}
		phoneReply(w, answer, err)
	}))
	// What the phone's owner sets on its photos: the call, the state, on or
	// off, and the reply's key.
	for _, m := range []struct {
		path  string
		state library.State
		on    bool
		reply string
	}{
		{"archive", library.Archived, true, "archived"},
		{"unarchive", library.Archived, false, "unarchived"},
		{"trash", library.Trashed, true, "trashed"},
		{"untrash", library.Trashed, false, "untrashed"},
	} {
		mux.Handle("POST /v1/"+m.path, paired(func(w http.ResponseWriter, r *http.Request, phone engine.Phone) {
			hashes, ok := askedHashes(w, r)
			if !ok {
				return
			}
			done, err := engine.MarkOf(phone, m.state, hashes, m.on)
			phoneReply(w, map[string][]string{m.reply: done}, err)
		}))
	}
	mux.Handle("PUT /v1/photo/{hash}", paired(func(w http.ResponseWriter, r *http.Request, phone engine.Phone) {
		hash, ok := askedHash(w, r)
		if !ok {
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
	libraryRoutes(mux)
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
	case errors.Is(err, engine.ErrNotHere):
		phoneError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, engine.ErrFileGone):
		phoneError(w, http.StatusGone, err.Error())
	// The phone is gone, or Homewend quits.
	case errors.Is(err, context.Canceled):
		phoneError(w, http.StatusServiceUnavailable, err.Error())
	case errors.Is(err, library.ErrNotMarks):
		phoneError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, engine.ErrNoLibrary), errors.Is(err, engine.ErrEmptyLibrary), errors.Is(err, library.ErrNoMarks), errors.As(err, new(engine.LibraryGoneError)):
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
	out := json.NewEncoder(w)
	out.SetEscapeHTML(false) // an edit is as it was said, and no longer
	out.Encode(v)
}
