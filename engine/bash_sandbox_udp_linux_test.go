//go:build linux

package engine

import (
	"fmt"
	"net"
	"os/exec"
	"strings"
	"testing"
	"time"

	llsyscall "github.com/landlock-lsm/go-landlock/landlock/syscall"
)

// Since Landlock ABI v10 UDP is filtered too: a closed sandbox sends no
// datagram (no data out through DNS), and opening a TCP port opens DNS (UDP
// 53) only, not every UDP port.
func TestBashSandboxUDP(t *testing.T) {
	if v, err := llsyscall.LandlockGetABIVersion(); err != nil || v < 10 {
		t.Skip("UDP filtering needs Landlock ABI v10")
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not found (the test sends with bash's /dev/udp)")
	}
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	port := conn.LocalAddr().(*net.UDPAddr).Port
	send := fmt.Sprintf(`bash -c 'echo LEAK > /dev/udp/127.0.0.1/%d'`, port)
	received := func() bool {
		conn.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
		buf := make([]byte, 16)
		n, _, err := conn.ReadFrom(buf)
		return err == nil && strings.Contains(string(buf[:n]), "LEAK")
	}
	for name, sb := range map[string]*SandboxConfig{
		"default":         DefaultSandbox(),
		"443 open":        {Enabled: true, Network: &SandboxNetwork{Connect: []uint16{443}}},
		"udp port as tcp": {Enabled: true, Network: &SandboxNetwork{Connect: []uint16{uint16(port)}}},
	} {
		_, _, run := sandboxFixture(t, sb)
		run(send)
		if received() {
			t.Errorf("%s: a datagram left the sandbox", name)
		}
	}
	_, _, run := sandboxFixture(t, &SandboxConfig{Enabled: true, Network: &SandboxNetwork{All: true}})
	run(send)
	if !received() {
		t.Error("network all: the datagram did not go out")
	}
}
