// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"net"
	"testing"
	"time"

	"github.com/ruvido/homewend/internal/engine"
)

// The phones' port is kept while phones know it: taken by another program,
// the listener waits a moment, then is not opened, and the port stays. With no
// phone paired another one does.
func TestThePhonesPortIsKept(t *testing.T) {
	newLibrary(t)
	was := portWait
	portWait = 200 * time.Millisecond
	defer func() { portWait = was }()
	taken, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatal(err)
	}
	defer taken.Close()
	port := taken.Addr().(*net.TCPAddr).Port
	keep := func(phones []engine.Phone) {
		if _, err := engine.UpdatePhones(func(p *engine.Phones) error { p.Port, p.Phones = port, phones; return nil }); err != nil {
			t.Fatal(err)
		}
	}
	kept := func() int {
		p, err := engine.LoadPhones()
		if err != nil {
			t.Fatal(err)
		}
		return p.Port
	}

	keep([]engine.Phone{{ID: "ron's", Profile: "ron"}})
	if l, err := listenPhones(); err == nil {
		l.ln.Close()
		t.Fatal("opened on another port")
	}
	if kept() != port {
		t.Errorf("the port moved to %d", kept())
	}

	keep(nil)
	l, err := listenPhones()
	if err != nil {
		t.Fatal(err)
	}
	defer l.ln.Close()
	if l.port == port || kept() != l.port {
		t.Errorf("listening on %d, kept %d", l.port, kept())
	}

	// Freed while it waits: taken again.
	keep([]engine.Phone{{ID: "ron's", Profile: "ron"}})
	l.ln.Close()
	portWait = 2 * time.Second
	go func() { time.Sleep(300 * time.Millisecond); taken.Close() }()
	again, err := listenPhones()
	if err != nil {
		t.Fatal(err)
	}
	defer again.ln.Close()
	if again.port != port {
		t.Errorf("listening on %d", again.port)
	}
}
