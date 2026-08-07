package netpolicy

import (
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

func TestParseForward(t *testing.T) {
	f, err := ParseForward("3000:dev-ctn-app:8080")
	if err != nil {
		t.Fatal(err)
	}
	if f.ListenPort != 3000 || f.TargetHost != "dev-ctn-app" || f.TargetPort != 8080 {
		t.Fatalf("parsed = %+v", f)
	}
	if f.String() != "3000:dev-ctn-app:8080" {
		t.Errorf("String() = %q", f.String())
	}

	for _, bad := range []string{"", "3000", "3000:host", "x:host:1", "3000:host:y",
		"0:host:80", "3000::80", "70000:host:80"} {
		if _, err := ParseForward(bad); err == nil {
			t.Errorf("ParseForward(%q) should have failed", bad)
		}
	}
}

func TestForwardRelaysBothDirections(t *testing.T) {
	// The workload's server, standing in for a container on the internal
	// network.
	backend, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	go func() {
		for {
			conn, err := backend.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				b := make([]byte, 64)
				n, _ := conn.Read(b)
				fmt.Fprintf(conn, "echo:%s", string(b[:n]))
			}()
		}
	}()

	host, port, _ := net.SplitHostPort(backend.Addr().String())
	var target int
	fmt.Sscanf(port, "%d", &target)

	f := &Forward{ListenPort: 0, TargetHost: host, TargetPort: target}
	if err := f.Listen(); err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	conn, err := net.Dial("tcp", f.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))

	if _, err := conn.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(conn)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "echo:hello") {
		t.Fatalf("relayed = %q", got)
	}
}

func TestForwardToADeadTargetDoesNotHang(t *testing.T) {
	// A server that has not started yet is the common case: the port is
	// forwarded before the workload binds it.
	f := &Forward{ListenPort: 0, TargetHost: "127.0.0.1", TargetPort: 1}
	if err := f.Listen(); err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	conn, err := net.Dial("tcp", f.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))

	if _, err := io.ReadAll(conn); err != nil {
		t.Fatalf("client was left hanging: %v", err)
	}
}

func TestForwardCloseWaitsForConnections(t *testing.T) {
	f := &Forward{ListenPort: 0, TargetHost: "127.0.0.1", TargetPort: 1}
	if err := f.Listen(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
}
