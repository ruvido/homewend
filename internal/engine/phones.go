// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

package engine

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math/big"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/ruvido/homewend/internal/library"
)

// Phone is a phone paired with this computer (docs/phone.md). Its token is
// kept as a hash: phones.json read by someone else lets no phone in.
type Phone struct {
	ID      string    `json:"id"`
	Name    string    `json:"name"`
	Profile string    `json:"profile"` // the library's folder its photos go in
	Token   string    `json:"token"`   // SHA-256 of the token, hex
	Paired  time.Time `json:"paired"`
	Seen    time.Time `json:"seen,omitzero"`
	Backup  time.Time `json:"backup,omitzero"` // the last photo it brought in
	Photos  int       `json:"photos"`          // brought in, since paired
}

// Phones is phones.json: the phones, the port they reach this computer on,
// and the phones unpaired, kept as their history.
type Phones struct {
	Port     int        `json:"port,omitempty"`
	Phones   []Phone    `json:"phones"`
	Unpaired []Unpaired `json:"unpaired,omitempty"`
}

// Unpaired is a phone no longer paired: what it brought, and who ended it.
type Unpaired struct {
	Name    string    `json:"name"`
	Profile string    `json:"profile"`
	Paired  time.Time `json:"paired"`
	Ended   time.Time `json:"unpaired"`
	Photos  int       `json:"photos"`
	By      string    `json:"by"` // "phone" or "computer"
}

var (
	// ErrBadCode is a pairing code wrong, used, or too old.
	ErrBadCode = errors.New("the code is wrong, used, or too old")
	// ErrNotThisHash is a file whose bytes are not the hash it was sent as.
	ErrNotThisHash = errors.New("the bytes are not the photo named")
	// ErrNotBroughtIn is a photo received whole that the import left out.
	ErrNotBroughtIn = errors.New("the photo was received but not brought in")
	// ErrNoPhone is a token no paired phone has.
	ErrNoPhone = errors.New("this phone is not paired")
)

// codeLife is how long a pairing code is good for: the person is in front of
// both screens.
const codeLife = 5 * time.Minute

// phonesMu holds one change of phones.json at a time: two phones back up at
// once, each telling when it was last seen.
var phonesMu sync.Mutex

// pairing is the one code shown on the computer, and the profile chosen
// before it: a new code replaces it, a phone that uses it ends it.
var pairing struct {
	sync.Mutex
	code, profile string
	until         time.Time
}

func phonesPath() (string, error) {
	dir, err := configDir()
	return filepath.Join(dir, "phones.json"), err
}

// LoadPhones reads phones.json; none yet is no phones.
func LoadPhones() (Phones, error) {
	path, err := phonesPath()
	if err != nil {
		return Phones{}, err
	}
	return readJSON[Phones](path)
}

func readJSON[T any](path string) (T, error) {
	var v T
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return v, nil
	}
	if err != nil {
		return v, err
	}
	if err := json.Unmarshal(data, &v); err != nil {
		return v, fmt.Errorf("reading %s: %w", path, err)
	}
	return v, nil
}

// UpdatePhones changes phones.json as it is now and writes it.
func UpdatePhones(change func(*Phones) error) (Phones, error) {
	phonesMu.Lock()
	defer phonesMu.Unlock()
	path, err := phonesPath()
	if err != nil {
		return Phones{}, err
	}
	p, err := readJSON[Phones](path)
	if err != nil {
		return p, err
	}
	if err := change(&p); err != nil {
		return p, err
	}
	return p, writeJSON(path, p)
}

// StartPairing makes the code a phone sends to pair, for profile.
func StartPairing(profile string) (string, error) {
	if err := CheckProfile(profile); err != nil {
		return "", err
	}
	pairing.Lock()
	defer pairing.Unlock()
	pairing.code, pairing.profile, pairing.until = rand.Text(), profile, time.Now().Add(codeLife)
	return pairing.code, nil
}

// StopPairing forgets the code: the person closed the card.
func StopPairing() {
	pairing.Lock()
	defer pairing.Unlock()
	pairing.code = ""
}

// Pair takes the code the phone read, once, and gives the phone its token.
func Pair(code, name string) (Phone, string, error) {
	pairing.Lock()
	ok := pairing.code != "" && time.Now().Before(pairing.until) &&
		subtle.ConstantTimeCompare([]byte(code), []byte(pairing.code)) == 1
	profile := pairing.profile
	if ok {
		pairing.code = ""
	}
	pairing.Unlock()
	if !ok {
		return Phone{}, "", ErrBadCode
	}
	token := rand.Text()
	phone := Phone{ID: rand.Text()[:8], Name: name, Profile: profile, Token: hashText(token), Paired: time.Now()}
	_, err := UpdatePhones(func(p *Phones) error {
		p.Phones = append(p.Phones, phone)
		return nil
	})
	return phone, token, err
}

// Pairing says whether the code shown is still waiting for a phone.
func Pairing() bool {
	pairing.Lock()
	defer pairing.Unlock()
	return pairing.code != "" && time.Now().Before(pairing.until)
}

// seenEvery is how often a phone that keeps asking is noted seen.
const seenEvery = 10 * time.Second

var errSeen = errors.New("seen just now")

// PhoneOf is the phone with this token, and notes it seen.
func PhoneOf(token string) (Phone, error) {
	want := hashText(token)
	var phone Phone
	_, err := UpdatePhones(func(p *Phones) error {
		i := slices.IndexFunc(p.Phones, func(ph Phone) bool {
			return subtle.ConstantTimeCompare([]byte(ph.Token), []byte(want)) == 1
		})
		if i < 0 {
			return ErrNoPhone
		}
		phone = p.Phones[i]
		// A phone asks for thousands of small copies in a row: the file is
		// not written for each.
		if time.Since(phone.Seen).Abs() < seenEvery {
			return errSeen
		}
		p.Phones[i].Seen = time.Now()
		return nil
	})
	if errors.Is(err, errSeen) {
		err = nil
	}
	return phone, err
}

// Unpair forgets a phone, unpaired by "phone" or "computer": its token lets
// it in no more, and its history stays.
func Unpair(id, by string) error {
	defer stopWaiting(id)
	_, err := UpdatePhones(func(p *Phones) error {
		i := slices.IndexFunc(p.Phones, func(ph Phone) bool { return ph.ID == id })
		if i < 0 {
			return nil
		}
		ph := p.Phones[i]
		p.Unpaired = append(p.Unpaired, Unpaired{Name: ph.Name, Profile: ph.Profile, Paired: ph.Paired, Ended: time.Now(), Photos: ph.Photos, By: by})
		p.Phones = slices.Delete(p.Phones, i, i+1)
		return nil
	})
	return err
}

func hashText(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// PhoneCert is the listener's certificate, made once and kept beside
// phones.json, and the SHA-256 of its public key the phone pins.
func PhoneCert() (tls.Certificate, []byte, error) {
	dir, err := configDir()
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	certFile, keyFile := filepath.Join(dir, "phone-cert.pem"), filepath.Join(dir, "phone-key.pem")
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if errors.Is(err, fs.ErrNotExist) {
		cert, err = makeCert(certFile, keyFile)
	}
	if err != nil {
		return cert, nil, err
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return cert, nil, err
	}
	pin := sha256.Sum256(leaf.RawSubjectPublicKeyInfo)
	return cert, pin[:], nil
}

// makeCert makes a self-signed certificate: the phone trusts it by its key,
// pinned at pairing, so no name or authority in it is checked.
func makeCert(certFile, keyFile string) (tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return tls.Certificate{}, err
	}
	tmpl := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "Homewend"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().AddDate(30, 0, 0),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return tls.Certificate{}, err
	}
	if err := os.MkdirAll(filepath.Dir(certFile), 0o755); err != nil {
		return tls.Certificate{}, err
	}
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		return tls.Certificate{}, err
	}
	if err := os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644); err != nil {
		return tls.Certificate{}, err
	}
	return tls.LoadX509KeyPair(certFile, keyFile)
}

// Held is what a phone is told of the photos it asks about.
type Held struct {
	Have     []string `json:"have"`
	Out      []string `json:"out"`
	Archived []string `json:"archived"`
	// Its library's marks; none with no library, or one that keeps none.
	*library.Shelf
}

// Have splits the hashes phone asks about, by its own library: in it with
// their file in place, or deleted from it and kept out of every import;
// archived are those of have that are archived. A photo another library has
// is in neither list: whether this one is to have it too is said by sending
// it, with its name, and it is then not read (ReceivePhoto). A question brings
// nothing in: the phone asks about photos it means to delete as well.
func Have(phone Phone, hashes []string) (Held, error) {
	out := Held{Have: []string{}, Out: []string{}, Archived: []string{}}
	root, catalog, err := lookAtLibrary()
	if err != nil || catalog == nil {
		return out, err
	}
	defer catalog.Close()
	for _, h := range hashes {
		held, err := library.Holds(catalog, root, library.Key{Hash: h, Profile: phone.Profile})
		if err != nil {
			return out, err
		}
		switch {
		case held.Out():
			out.Out = append(out.Out, h)
		case held.Known:
			out.Have = append(out.Have, h)
			if held.Archived {
				out.Archived = append(out.Archived, h)
			}
		}
	}
	shelf, err := catalog.Shelf(phone.Profile)
	if errors.Is(err, library.ErrNoMarks) {
		return out, nil
	}
	out.Shelf = &shelf
	return out, err
}

// MarkOf is Mark on phone's photos of these hashes, no other library's; with
// the library away an error, or the phone would forget what it queued.
func MarkOf(phone Phone, state library.State, hashes []string, on bool) ([]string, error) {
	keys := make([]library.Key, len(hashes))
	for i, h := range hashes {
		keys[i] = library.Key{Hash: h, Profile: phone.Profile}
	}
	_, c, err := openHere(library.OpenCatalog)
	if err != nil || c == nil {
		return []string{}, err
	}
	marked, err := markIn(c, state, keys, on)
	done := make([]string, len(marked))
	for i, k := range marked {
		done[i] = k.Hash
	}
	return done, err
}

// PhoneLibrary is what a phone is told of the library it backs up into:
// its disk, and how many photos its profile holds there. Known is false
// when there is no library.
type PhoneLibrary struct {
	Known  bool
	Free   int64
	Total  int64
	Photos int
	// Bytes the library's files take, by kind: "video", "raw", "" photos.
	Bytes map[string]int64
}

// LibraryOf is what the library is for phone.
func LibraryOf(phone Phone) (PhoneLibrary, error) {
	root, catalog, err := lookAtLibrary()
	if err != nil || root == "" {
		return PhoneLibrary{}, err
	}
	if catalog != nil {
		defer catalog.Close()
	}
	l := PhoneLibrary{Known: true}
	if l.Free, l.Total, err = Space(root); err != nil || catalog == nil {
		return l, err
	}
	if l.Photos, err = catalog.Count(library.Filter{Profile: phone.Profile}); err != nil {
		return l, err
	}
	l.Bytes, err = catalog.BytesByKind()
	return l, err
}

// Received is what became of a photo a phone sent.
type Received struct {
	Path string `json:"path,omitempty"` // where the library holds it
	Out  bool   `json:"out,omitempty"`  // deleted here: not brought back
	New  bool   `json:"-"`              // placed by this call
}

// ReceivePhoto brings in one photo from phone: written inside the library,
// checked against hash, imported as a card's file is, into the phone's
// profile, then its upload removed. The bytes arrive before the library's
// turn is taken: a slow phone does not hold up a card's import. Those another
// library has are not read from the phone.
func ReceivePhoto(ctx context.Context, phone Phone, hash, name string, size int64, body io.Reader) (Received, error) {
	root, catalog, err := lookAtLibrary()
	if err != nil {
		return Received{}, err
	}
	key := library.Key{Hash: hash, Profile: phone.Profile}
	var elsewhere bool
	if catalog != nil {
		held, err := library.Holds(catalog, root, key)
		elsewhere = held.Elsewhere
		catalog.Close()
		if err != nil || held.Out() {
			return Received{Out: held.Out()}, err
		}
		if held.Known {
			return Received{Path: held.Path}, nil
		}
	}
	if root == "" {
		return Received{}, ErrNoLibrary
	}
	if err := fits(root, size); !elsewhere && err != nil {
		return Received{}, err
	}
	// Busy now is said before a byte is read: a video is not sent again and
	// again over the network while a card's import runs.
	unlock, err := takeLibrary(ctx, root)
	if err != nil {
		return Received{}, err
	}
	unlock()
	// A folder of each request's own, on the library's disk: placing is a
	// rename, and a phone that sends again before an answer, or two files
	// at once, removes no other upload.
	phones := filepath.Join(root, library.WorkDir, "phone")
	if err := os.MkdirAll(phones, 0o755); err != nil {
		return Received{}, err
	}
	dir, err := os.MkdirTemp(phones, phone.ID+"-")
	if err != nil {
		return Received{}, err
	}
	defer os.RemoveAll(dir)
	file := filepath.Join(dir, filepath.Base(filepath.Clean("/"+name)))
	// In its turn: what is read of another library's file is what is linked.
	known := ""
	if elsewhere {
		if unlock, err = waitLibrary(ctx, root, nil); err != nil {
			return Received{}, err
		}
		if sharedInto(file, root, hash) {
			known = hash
		} else {
			unlock()
		}
	}
	if known == "" {
		if err := receive(file, hash, body); err != nil {
			return Received{}, err
		}
		// Its bytes are here: it waits its turn rather than being sent again.
		if unlock, err = waitLibrary(ctx, root, nil); err != nil {
			return Received{}, err
		}
	}
	defer unlock()
	rw, err := library.OpenCatalog(catalogPath(root))
	if err != nil {
		return Received{}, err
	}
	defer rw.Close()
	folder := library.Folder{Root: dir, Items: []library.Item{{Path: file, Hash: known}}}
	opt := library.Options{Profile: phone.Profile, SeparateMessaging: true, Account: library.LocalSource + phone.Name}
	// Picked: a phone's every photo comes in, a screenshot too, which a
	// card's look would set apart.
	how := library.ImportOptions{Link: canLink(dir), Fits: func(n int64) error { return fits(root, n) },
		Asked: library.Asked{Choice: &library.Choice{Pick: []string{filepath.Base(file)}}}}
	// Every byte is here and checked: a phone that drops the connection now
	// (a timeout, the Wi-Fi) does not throw the photo away; its next
	// /v1/have finds it.
	done, err := library.Import(context.WithoutCancel(ctx), folder, root, rw, opt, how, nil)
	if err != nil {
		return Received{}, err
	}
	path, _, err := rw.PathOf(key)
	if err != nil {
		return Received{}, err
	}
	// Not in, and no error: never answered as "already there", or the phone
	// would not send it again.
	if path == "" {
		return Received{}, ErrNotBroughtIn
	}
	if done.Placed == 0 {
		return Received{Path: path}, nil
	}
	_, err = UpdatePhones(func(p *Phones) error {
		if i := slices.IndexFunc(p.Phones, func(ph Phone) bool { return ph.ID == phone.ID }); i >= 0 {
			p.Phones[i].Backup = time.Now()
			p.Phones[i].Photos++
		}
		return nil
	})
	return Received{Path: path, New: true}, err
}

// sharedInto makes file a second name for the photo of hash in another
// library, or a copy where the disk has no hard links, and says whether it
// did: if not, the phone's bytes are the ones.
func sharedInto(file, root, hash string) bool {
	_, catalog, err := lookAtLibrary()
	if err != nil || catalog == nil {
		return false
	}
	defer catalog.Close()
	from, err := library.Shared(catalog, root, hash, "")
	if err != nil || from == "" {
		return false
	}
	if os.Link(from, file) == nil {
		return true
	}
	f, err := os.Open(from)
	if err != nil {
		return false
	}
	defer f.Close()
	return receive(file, hash, f) == nil
}

// receive writes body to file and checks it is the photo of hash.
func receive(file, hash string, body io.Reader) error {
	f, err := os.Create(file)
	if err != nil {
		return err
	}
	sum := sha256.New()
	_, err = io.Copy(io.MultiWriter(f, sum), body)
	if err = errors.Join(err, f.Sync(), f.Close()); err != nil {
		return err
	}
	if hex.EncodeToString(sum.Sum(nil)) != hash {
		return ErrNotThisHash
	}
	return nil
}

// fits says whether size bytes fit on the library's disk.
func fits(root string, size int64) error {
	free, err := Free(root)
	if err != nil {
		return err
	}
	if size > free {
		return NoSpaceError{Need: size, Free: free}
	}
	return nil
}
