// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"errors"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/ruvido/homewend/internal/engine"
)

// The phones' port is the kept one if free, else the first free of the list,
// never another; all taken, the listener is not opened and says why.
func TestThePhonesPortIsOfTheList(t *testing.T) {
	newLibrary(t)
	was, waited := phonePorts, portWait
	defer func() { phonePorts, portWait = was, waited }()
	portWait = 200 * time.Millisecond
	// Four free ports, as the list would be.
	var ports []int
	for range 4 {
		ln, err := net.Listen("tcp", ":0")
		if err != nil {
			t.Fatal(err)
		}
		ports = append(ports, ln.Addr().(*net.TCPAddr).Port)
		ln.Close()
	}
	phonePorts = ports
	kept := func(port int) {
		if _, err := engine.UpdatePhones(func(p *engine.Phones) error { p.Port = port; return nil }); err != nil {
			t.Fatal(err)
		}
	}
	take := func(port int) net.Listener {
		ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
		if err != nil {
			t.Fatal(err)
		}
		return ln
	}
	open := func(want int) {
		t.Helper()
		l, err := listenPhones()
		if err != nil {
			t.Fatal(err)
		}
		l.ln.Close()
		p, _ := engine.LoadPhones()
		if l.port != want || p.Port != want {
			t.Errorf("listening on %d, kept %d, want %d", l.port, p.Port, want)
		}
	}
	kept(0)
	open(ports[0])
	kept(ports[2])
	open(ports[2])
	// The kept one taken: the first free of the list.
	a, b := take(ports[2]), take(ports[0])
	open(ports[1])
	// All taken: none, and the kept port stays.
	c, d := take(ports[1]), take(ports[3])
	if _, err := listenPhones(); !errors.Is(err, ErrPortsTaken) {
		t.Errorf("all taken: %v", err)
	}
	for _, ln := range []net.Listener{a, b, c, d} {
		ln.Close()
	}
}
