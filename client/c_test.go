package client

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"testing"
	"time"

	"github.com/hashicorp/yamux"
)

func TestHandleLocalSocksRequestRejectsUnsupportedNoAuthMethod(t *testing.T) {
	proxyConn, peerConn := net.Pipe()
	done := make(chan struct{})
	go func() {
		handleLocalSocksRequest(proxyConn, nil, "", "", func(string, ...interface{}) {}, 0)
		close(done)
	}()
	defer peerConn.Close()

	if _, err := peerConn.Write([]byte{0x05, 0x01, 0x02}); err != nil {
		t.Fatalf("write greeting: %v", err)
	}
	response := make([]byte, 2)
	if _, err := io.ReadFull(peerConn, response); err != nil {
		t.Fatalf("read method selection: %v", err)
	}
	if got, want := string(response), string([]byte{0x05, 0xff}); got != want {
		t.Fatalf("method selection = %v, want %v", response, []byte{0x05, 0xff})
	}
	<-done
}

func TestHandleLocalSocksRequestForwardsDomainOverYamux(t *testing.T) {
	clientSession, serverSession := newYamuxPair(t)
	proxyConn, peerConn := net.Pipe()
	deadline := time.Now().Add(3 * time.Second)
	if err := peerConn.SetDeadline(deadline); err != nil {
		t.Fatalf("set peer deadline: %v", err)
	}

	handlerDone := make(chan struct{})
	go func() {
		handleLocalSocksRequest(proxyConn, clientSession, "", "", func(string, ...interface{}) {}, 1)
		close(handlerDone)
	}()

	serverDone := make(chan error, 1)
	go func() {
		stream, err := serverSession.Accept()
		if err != nil {
			serverDone <- fmt.Errorf("accept yamux stream: %w", err)
			return
		}
		defer stream.Close()
		if err := stream.SetDeadline(deadline); err != nil {
			serverDone <- fmt.Errorf("set stream deadline: %w", err)
			return
		}

		wantRequest := append([]byte{0x03, byte(len("example.test"))}, []byte("example.test")...)
		wantRequest = append(wantRequest, 0x01, 0xbb)
		request := make([]byte, len(wantRequest))
		if _, err := io.ReadFull(stream, request); err != nil {
			serverDone <- fmt.Errorf("read target request: %w", err)
			return
		}
		if !bytes.Equal(request, wantRequest) {
			serverDone <- fmt.Errorf("target request = %v, want %v", request, wantRequest)
			return
		}
		if _, err := stream.Write([]byte{0x01}); err != nil {
			serverDone <- fmt.Errorf("write connect confirmation: %w", err)
			return
		}

		payload := make([]byte, len("hello through tunnel"))
		if _, err := io.ReadFull(stream, payload); err != nil {
			serverDone <- fmt.Errorf("read payload: %w", err)
			return
		}
		if _, err := stream.Write(payload); err != nil {
			serverDone <- fmt.Errorf("echo payload: %w", err)
			return
		}
		serverDone <- nil
	}()

	if _, err := peerConn.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		t.Fatalf("write greeting: %v", err)
	}
	method := make([]byte, 2)
	if _, err := io.ReadFull(peerConn, method); err != nil {
		t.Fatalf("read method selection: %v", err)
	}
	if want := []byte{0x05, 0x00}; !bytes.Equal(method, want) {
		t.Fatalf("method selection = %v, want %v", method, want)
	}

	request := append([]byte{0x05, 0x01, 0x00, 0x03, byte(len("example.test"))}, []byte("example.test")...)
	request = append(request, 0x01, 0xbb)
	if _, err := peerConn.Write(request); err != nil {
		t.Fatalf("write CONNECT request: %v", err)
	}
	reply := make([]byte, 10)
	if _, err := io.ReadFull(peerConn, reply); err != nil {
		t.Fatalf("read CONNECT reply: %v", err)
	}
	if reply[1] != 0x00 {
		t.Fatalf("CONNECT reply code = %#x, want success", reply[1])
	}

	wantPayload := []byte("hello through tunnel")
	if _, err := peerConn.Write(wantPayload); err != nil {
		t.Fatalf("write tunneled payload: %v", err)
	}
	gotPayload := make([]byte, len(wantPayload))
	if _, err := io.ReadFull(peerConn, gotPayload); err != nil {
		t.Fatalf("read tunneled payload: %v", err)
	}
	if !bytes.Equal(gotPayload, wantPayload) {
		t.Fatalf("tunneled payload = %q, want %q", gotPayload, wantPayload)
	}

	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}
	_ = peerConn.Close()
	select {
	case <-handlerDone:
	case <-time.After(3 * time.Second):
		t.Fatal("SOCKS5 handler did not stop after local connection closed")
	}
}

func TestHandleLocalSocksRequestAuthenticatesUsernameAndPassword(t *testing.T) {
	proxyConn, peerConn := net.Pipe()
	defer peerConn.Close()
	if err := peerConn.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatalf("set peer deadline: %v", err)
	}

	done := make(chan struct{})
	go func() {
		handleLocalSocksRequest(proxyConn, nil, "alice", "secret", func(string, ...interface{}) {}, 1)
		close(done)
	}()

	if _, err := peerConn.Write([]byte{0x05, 0x02, 0x00, 0x02}); err != nil {
		t.Fatalf("write greeting: %v", err)
	}
	method := make([]byte, 2)
	if _, err := io.ReadFull(peerConn, method); err != nil {
		t.Fatalf("read method selection: %v", err)
	}
	if want := []byte{0x05, 0x02}; !bytes.Equal(method, want) {
		t.Fatalf("method selection = %v, want %v", method, want)
	}

	auth := append([]byte{0x01, byte(len("alice"))}, []byte("alice")...)
	auth = append(auth, byte(len("secret")))
	auth = append(auth, []byte("secret")...)
	if _, err := peerConn.Write(auth); err != nil {
		t.Fatalf("write credentials: %v", err)
	}
	authReply := make([]byte, 2)
	if _, err := io.ReadFull(peerConn, authReply); err != nil {
		t.Fatalf("read authentication reply: %v", err)
	}
	if want := []byte{0x01, 0x00}; !bytes.Equal(authReply, want) {
		t.Fatalf("authentication reply = %v, want %v", authReply, want)
	}

	// Use an unsupported command so the handler can finish without a yamux session.
	if _, err := peerConn.Write([]byte{0x05, 0x02, 0x00, 0x01}); err != nil {
		t.Fatalf("write unsupported request: %v", err)
	}
	reply := make([]byte, 10)
	if _, err := io.ReadFull(peerConn, reply); err != nil {
		t.Fatalf("read unsupported-command reply: %v", err)
	}
	if reply[1] != 0x07 {
		t.Fatalf("reply code = %#x, want command-not-supported", reply[1])
	}
	<-done
}

func newYamuxPair(t *testing.T) (*yamux.Session, *yamux.Session) {
	t.Helper()
	clientConn, serverConn := net.Pipe()
	clientSession, err := yamux.Client(clientConn, nil)
	if err != nil {
		t.Fatalf("create client yamux session: %v", err)
	}
	serverSession, err := yamux.Server(serverConn, nil)
	if err != nil {
		clientSession.Close()
		t.Fatalf("create server yamux session: %v", err)
	}
	t.Cleanup(func() {
		clientSession.Close()
		serverSession.Close()
	})
	return clientSession, serverSession
}
