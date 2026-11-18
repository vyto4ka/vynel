package setup

import (
	"fmt"
	"net"
	"strings"
	"testing"
	"time"
)

func TestCheckPortsFreeFree(t *testing.T) {
	if err := CheckPortsFree(); err != nil {
		t.Skipf("ports 80/443 busy on this machine: %v", err)
	}
}

func TestCheckPortsFreeBusy(t *testing.T) {
	held, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	heldPort := held.Addr().(*net.TCPAddr).Port

	// A port we just released can be grabbed by another process on a busy CI runner
	// between Close and the check, so a released port is tried a few times.
	freePort := 0
	for i := 0; i < 10; i++ {
		free, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		p := free.Addr().(*net.TCPAddr).Port
		free.Close()
		if err := checkPortsFree(p, p); err == nil {
			freePort = p
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if freePort == 0 {
		t.Fatal("every released port reported busy")
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
