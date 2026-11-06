package setup

import (
	"fmt"
	"net"
	"strings"
	"testing"
)

func TestCheckPortsFreeFree(t *testing.T) {
	if err := CheckPortsFree(); err != nil {
		t.Skipf("ports 80/443 busy on this machine: %v", err)
	}
}

func TestCheckPortsFreeBusy(t *testing.T) {
	free, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer free.Close()
	held, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	freePort := free.Addr().(*net.TCPAddr).Port
	heldPort := held.Addr().(*net.TCPAddr).Port
	// Close the free one so it really is free.
	free.Close()

	if err := checkPortsFree(freePort, freePort); err != nil {
		t.Fatalf("free port reported busy: %v", err)
	}
	err = checkPortsFree(freePort, heldPort)
	if err == nil {
		t.Fatal("busy port not reported")
	}
	if !strings.Contains(err.Error(), fmt.Sprint(heldPort)) {
		t.Fatalf("error does not name the port: %v", err)
	}
	if !strings.Contains(err.Error(), "занят") {
		t.Fatalf("error is not user-facing: %v", err)
	}
}
