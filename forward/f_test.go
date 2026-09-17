package forward

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"testing"
	"time"
)

func TestNormalizeListenAddress(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		want    string
		wantErr bool
	}{
		{name: "port only", value: "8080", want: "127.0.0.1:8080"},
		{name: "all IPv4 interfaces", value: ":8080", want: ":8080"},
		{name: "explicit IPv4", value: "192.0.2.1:8080", want: "192.0.2.1:8080"},
		{name: "explicit IPv6", value: "[::1]:8080", want: "[::1]:8080"},
		{name: "empty", value: "", wantErr: true},
		{name: "not a port", value: "http", wantErr: true},
		{name: "zero port", value: "0", wantErr: true},
		{name: "port too large", value: "70000", wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := NormalizeListenAddress(test.value)
			if test.wantErr {
				if err == nil {
					t.Fatalf("NormalizeListenAddress(%q) succeeded, want error", test.value)
				}
				return
			}
			if err != nil {
				t.Fatalf("NormalizeListenAddress(%q): %v", test.value, err)
			}
			if got != test.want {
				t.Fatalf("NormalizeListenAddress(%q) = %q, want %q", test.value, got, test.want)
			}
		})
	}
}

func TestParseServiceURL(t *testing.T) {
	tests := []struct {
		name       string
		value      string
		wantListen string
		wantTarget string
		wantErr    bool
	}{
		{name: "GOST IPv4 form", value: "tcp://:8080/192.0.2.10:80", wantListen: ":8080", wantTarget: "192.0.2.10:80"},
		{name: "local only", value: "tcp://127.0.0.1:5432/db.internal:5432", wantListen: "127.0.0.1:5432", wantTarget: "db.internal:5432"},
		{name: "IPv6", value: "tcp://[::1]:8080/[2001:db8::1]:80", wantListen: "[::1]:8080", wantTarget: "[2001:db8::1]:80"},
		{name: "missing scheme", value: ":8080/192.0.2.10:80", wantErr: true},
		{name: "unsupported scheme", value: "udp://:8080/192.0.2.10:80", wantErr: true},
		{name: "missing target", value: "tcp://:8080", wantErr: true},
		{name: "query parameters", value: "tcp://:8080/192.0.2.10:80?foo=bar", wantErr: true},
		{name: "credentials", value: "tcp://user:pass@:8080/192.0.2.10:80", wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service, err := ParseServiceURL(test.value)
			if test.wantErr {
				if err == nil {
					t.Fatalf("ParseServiceURL(%q) succeeded, want error", test.value)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseServiceURL(%q): %v", test.value, err)
			}
			if service.Scheme != "tcp" || service.ListenAddress != test.wantListen || service.TargetAddress != test.wantTarget {
				t.Fatalf("ParseServiceURL(%q) = %#v, want tcp %s -> %s", test.value, service, test.wantListen, test.wantTarget)
			}
		})
	}
}

func TestValidateTargetAddress(t *testing.T) {
	valid := []string{"192.0.2.10:80", "example.test:443", "[2001:db8::1]:22"}
	for _, address := range valid {
		if err := ValidateTargetAddress(address); err != nil {
			t.Errorf("ValidateTargetAddress(%q): %v", address, err)
		}
	}

	invalid := []string{"", "192.0.2.10", ":80", "example.test:http", "example.test:70000"}
	for _, address := range invalid {
		if err := ValidateTargetAddress(address); err == nil {
			t.Errorf("ValidateTargetAddress(%q) succeeded, want error", address)
		}
	}
}

func TestProxyConnectionsCopiesBothDirections(t *testing.T) {
	localConnection, localPeer := net.Pipe()
	targetConnection, targetPeer := net.Pipe()
	deadline := time.Now().Add(3 * time.Second)
	for _, connection := range []net.Conn{localConnection, localPeer, targetConnection, targetPeer} {
		if err := connection.SetDeadline(deadline); err != nil {
			t.Fatalf("set deadline: %v", err)
		}
	}

	done := make(chan struct{})
	go func() {
		proxyConnections(localConnection, targetConnection, false)
		close(done)
	}()

	assertCopied := func(source, destination net.Conn, payload []byte) {
		t.Helper()
		writeDone := make(chan error, 1)
		go func() {
			_, err := source.Write(payload)
			writeDone <- err
		}()
		got := make([]byte, len(payload))
		if _, err := io.ReadFull(destination, got); err != nil {
			t.Fatalf("read copied payload: %v", err)
		}
		if err := <-writeDone; err != nil {
			t.Fatalf("write copied payload: %v", err)
		}
		if !bytes.Equal(got, payload) {
			t.Fatalf("copied payload = %q, want %q", got, payload)
		}
	}

	assertCopied(localPeer, targetPeer, []byte("client to target"))
	assertCopied(targetPeer, localPeer, []byte("target to client"))
	_ = localPeer.Close()
	_ = targetPeer.Close()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("bidirectional proxy did not stop after both peers closed")
	}
}

func TestServeForwardsWithoutTunnelServer(t *testing.T) {
	targetListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for target: %v", err)
	}
	defer targetListener.Close()

	forwardListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for forwarder: %v", err)
	}

	wantPayload := []byte("direct forwarding")
	targetDone := make(chan error, 1)
	go func() {
		connection, err := targetListener.Accept()
		if err != nil {
			targetDone <- fmt.Errorf("accept target connection: %w", err)
			return
		}
		defer connection.Close()
		payload := make([]byte, len(wantPayload))
		if _, err := io.ReadFull(connection, payload); err != nil {
			targetDone <- fmt.Errorf("read target payload: %w", err)
			return
		}
		if _, err := connection.Write(payload); err != nil {
			targetDone <- fmt.Errorf("echo target payload: %w", err)
			return
		}
		targetDone <- nil
	}()

	serveDone := make(chan error, 1)
	go func() {
		serveDone <- serve(forwardListener, targetListener.Addr().String(), false, 1)
	}()

	clientConnection, err := net.DialTimeout("tcp", forwardListener.Addr().String(), time.Second)
	if err != nil {
		t.Fatalf("connect to forwarder: %v", err)
	}
	if err := clientConnection.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatalf("set client deadline: %v", err)
	}
	if _, err := clientConnection.Write(wantPayload); err != nil {
		t.Fatalf("write forwarded payload: %v", err)
	}
	gotPayload := make([]byte, len(wantPayload))
	if _, err := io.ReadFull(clientConnection, gotPayload); err != nil {
		t.Fatalf("read forwarded payload: %v", err)
	}
	if !bytes.Equal(gotPayload, wantPayload) {
		t.Fatalf("forwarded payload = %q, want %q", gotPayload, wantPayload)
	}
	if err := <-targetDone; err != nil {
		t.Fatal(err)
	}

	_ = clientConnection.Close()
	_ = forwardListener.Close()
	select {
	case err := <-serveDone:
		if err != nil {
			t.Fatalf("serve returned error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("forwarding server did not stop after listener closed")
	}
}

func TestStartServicesClosesEarlierListenersWhenOneFails(t *testing.T) {
	firstProbe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve first address: %v", err)
	}
	firstAddress := firstProbe.Addr().String()
	_ = firstProbe.Close()

	occupiedListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("occupy second address: %v", err)
	}
	defer occupiedListener.Close()

	err = StartServices([]Service{
		{Scheme: "tcp", ListenAddress: firstAddress, TargetAddress: "192.0.2.10:80"},
		{Scheme: "tcp", ListenAddress: occupiedListener.Addr().String(), TargetAddress: "192.0.2.10:80"},
	}, false, 1)
	if err == nil {
		t.Fatal("StartServices succeeded with an occupied listener")
	}

	listener, err := net.Listen("tcp", firstAddress)
	if err != nil {
		t.Fatalf("first listener was not released after startup failed: %v", err)
	}
	_ = listener.Close()
}
