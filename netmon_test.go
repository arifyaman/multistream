package main

import (
	"net"
	"os"
	"testing"
)

func TestPIDConnectedTo(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	port := l.Addr().(*net.TCPAddr).Port

	hold := make(chan struct{})
	go func() {
		conn, _ := l.Accept()
		if conn != nil {
			defer conn.Close()
			<-hold
		}
	}()
	defer close(hold)

	conn, err := net.Dial("tcp", l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	ok, err := PIDConnectedTo(os.Getpid(), "127.0.0.1", port)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Error("want connected=true for own established connection")
	}

	ok, err = PIDConnectedTo(os.Getpid(), "127.0.0.1", port+1)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("want connected=false for a different port")
	}

	if ok, err := PIDConnectedTo(0, "127.0.0.1", port); err != nil || ok {
		t.Errorf("pid 0: ok=%v err=%v, want false,nil", ok, err)
	}

	if _, err := PIDConnectedTo(99999999, "127.0.0.1", port); err == nil {
		t.Error("want error for nonexistent pid")
	}
}

func TestEstablishedRemoteInodesBadIP(t *testing.T) {
	if _, err := establishedRemoteInodes("not-an-ip", 1935); err == nil {
		t.Error("want error for non-IPv4 address")
	}
}
