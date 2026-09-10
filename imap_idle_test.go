package main

import (
	"bufio"
	"net"
	"strings"
	"testing"
	"time"
)

func TestIDLEDoneKeepsSessionOpen(t *testing.T) {
	initLogger()
	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()

	go NewIMAPServer("", nil, nil, nil).handle(serverConn)

	reader := bufio.NewReader(clientConn)
	readLine := func() string {
		t.Helper()
		if err := clientConn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
			t.Fatal(err)
		}
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(line)
	}
	write := func(line string) {
		t.Helper()
		if _, err := clientConn.Write([]byte(line + "\r\n")); err != nil {
			t.Fatal(err)
		}
	}

	if got := readLine(); !strings.HasPrefix(got, "* OK") {
		t.Fatalf("greeting = %q", got)
	}
	write("A1 IDLE")
	if got := readLine(); got != "+ idling" {
		t.Fatalf("IDLE continuation = %q", got)
	}
	write("DONE")
	if got := readLine(); got != "A1 OK IDLE completed" {
		t.Fatalf("IDLE completion = %q", got)
	}

	write("A2 NOOP")
	if got := readLine(); got != "A2 OK NOOP completed" {
		t.Fatalf("session was not usable after IDLE; got %q", got)
	}
}
