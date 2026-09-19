package aceapi

import (
	"bufio"
	"context"
	"net"
	"testing"
	"time"
)

func TestCancellationInterruptsAuthentication(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	hello := make(chan struct{})
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		reader := bufio.NewReader(conn)
		_, _ = reader.ReadString('\n')
		close(hello)
		_, _ = reader.ReadString('\n')
	}()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := New("127.0.0.1", listener.Addr().(*net.TCPAddr).Port)
	if err := client.ConnectContext(ctx); err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	done := make(chan error, 1)
	go func() { done <- client.Authenticate() }()
	select {
	case <-hello:
	case <-time.After(time.Second):
		t.Fatal("handshake never started")
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled authentication succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation did not interrupt read")
	}
	<-serverDone
}
