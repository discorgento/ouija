//go:build linux

package engine

import (
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"os/exec"
	"runtime"
	"strings"
	"syscall"
	"testing"
)

// ICMP leaves no sandbox with a closed network: ping sockets (open to users
// where net.ipv4.ping_group_range allows), raw and packet sockets are
// refused, and so are MPTCP and SCTP (Landlock's TCP rules do not cover
// them) and VSOCK, while ordinary sockets are still made. `network: all`
// lifts it.
func TestBashSandboxRefusesICMPAndRawSockets(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not found (the test makes sockets with it)")
	}
	const try = `python3 -c 'import socket
for name, fam, typ, proto in [("icmp", 2, 2, 1), ("icmp6", 10, 2, 58), ("raw", 2, 3, 1), ("packet", 17, 3, 0), ("udp", 2, 2, 0), ("tcp", 10, 1, 0),
        ("tcp6", 10, 1, 6), ("unix", 1, 1, 0), ("mptcp", 2, 1, 262), ("sctp", 2, 1, 132), ("sctpseq", 10, 5, 132), ("vsock", 40, 1, 0)]:
    try:
        socket.socket(fam, typ, proto).close(); print(name, "ok")
    except OSError as e:
        print(name, "errno", e.errno)'`
	out, _ := exec.Command("sh", "-c", try).CombinedOutput()
	if !strings.Contains(string(out), "icmp ok") {
		t.Skipf("ping sockets are closed to this user anyway: %s", out)
	}
	_, _, run := sandboxFixture(t, DefaultSandbox())
	got := fmtData(run(try))
	for _, want := range []string{"icmp errno 1", "icmp6 errno 1", "raw errno 1", "packet errno 1", "udp errno 1", "tcp ok",
		"tcp6 ok", "unix ok", "mptcp errno 93", "sctp errno 93", "sctpseq errno 93", "vsock errno 97"} {
		if !strings.Contains(got, want) {
			t.Errorf("confined: want %q in\n%s", want, got)
		}
	}
	_, _, run = sandboxFixture(t, &SandboxConfig{Enabled: true, Network: &SandboxNetwork{Connect: []uint16{443}}})
	if got := fmtData(run(try)); !strings.Contains(got, "udp ok") || !strings.Contains(got, "icmp errno 1") {
		t.Errorf("a port open: want UDP sockets (DNS) and still no ICMP:\n%s", got)
	}
	_, _, run = sandboxFixture(t, &SandboxConfig{Enabled: true, Network: &SandboxNetwork{All: true}})
	if got := fmtData(run(try)); !strings.Contains(got, "icmp ok") {
		t.Errorf("network all: ICMP still refused:\n%s", got)
	}
}

// The filter decides as intended on every architecture it knows, for native
// and 32-bit programs (and x32), run through a classic BPF interpreter so
// the architectures this machine is not are checked too.
func TestSocketFilterDecisions(t *testing.T) {
	const (
		allow     = seccompRetAllow
		eperm     = seccompRetErrno | uint32(syscall.EPERM)
		noProto   = seccompRetErrno | uint32(syscall.EPROTONOSUPPORT)
		noFamily  = seccompRetErrno | uint32(syscall.EAFNOSUPPORT)
		noSys     = seccompRetErrno | uint32(syscall.ENOSYS)
		nonblock  = 0x800
		otherArch = 0x12345678
	)
	for arch, a := range seccompArches {
		f, err := socketFilter(a, socketsPorts)
		if err != nil {
			t.Fatalf("%s: %v", arch, err)
		}
		closed, err := socketFilter(a, socketsClosed)
		if err != nil {
			t.Fatalf("%s closed: %v", arch, err)
		}
		runIn := func(prog []sockFilter, audit, nr uint32, args ...uint32) uint32 {
			var d [64]byte
			binary.LittleEndian.PutUint32(d[seccompNr:], nr)
			binary.LittleEndian.PutUint32(d[seccompArchOff:], audit)
			for i, v := range args {
				binary.LittleEndian.PutUint32(d[seccompArg0+8*i:], v)
			}
			return runClassicBPF(t, prog, d[:])
		}
		run := func(audit, nr uint32, args ...uint32) uint32 { return runIn(f, audit, nr, args...) }
		all, err := socketFilter(a, socketsAll)
		if err != nil {
			t.Fatalf("%s all: %v", arch, err)
		}
		if got := runIn(all, a.native, a.socket, afInet, sockRaw, ipprotoICMP); got != allow {
			t.Errorf("%s network all: a raw socket got %#x, want allowed", arch, got)
		}
		if got := runIn(all, a.compat, a.compatSocketcall); got != allow {
			t.Errorf("%s network all: socketcall got %#x, want allowed", arch, got)
		}
		if got := runIn(all, a.native, a.peek[0]); got != eperm {
			t.Errorf("%s network all: ptrace got %#x, want EPERM", arch, got)
		}
		if got := runIn(closed, a.native, a.socket, afInet, sockDgram, 0); got != eperm {
			t.Errorf("%s closed network: UDP got %#x, want EPERM", arch, got)
		}
		if got := runIn(closed, a.native, a.socket, afInet, sockStream, 0); got != allow {
			t.Errorf("%s closed network: TCP sockets (Landlock filters them) got %#x", arch, got)
		}
		for _, nr := range append(a.peek, pidfdGetfd) {
			if got := run(a.native, nr); got != eperm {
				t.Errorf("%s: call %d (reads another process) got %#x, want EPERM", arch, nr, got)
			}
		}
		for _, nr := range append(a.compatPeek, pidfdGetfd) {
			if got := run(a.compat, nr); got != eperm {
				t.Errorf("%s compat: call %d got %#x, want EPERM", arch, nr, got)
			}
		}
		for _, nr := range a.x32Peek {
			if got := run(a.native, nr); got != eperm {
				t.Errorf("%s x32: call %#x got %#x, want EPERM", arch, nr, got)
			}
		}
		for _, tc := range []struct {
			name  string
			audit uint32
			nr    uint32
			args  []uint32
			want  uint32
		}{
			{"tcp", a.native, a.socket, []uint32{afInet, sockStream, 0}, allow},
			{"tcp6 nonblocking", a.native, a.socket, []uint32{afInet6, sockStream | nonblock, ipprotoTCP}, allow},
			{"udp", a.native, a.socket, []uint32{afInet, sockDgram, ipprotoUDP}, allow},
			{"unix", a.native, a.socket, []uint32{afUnix, sockStream, 0}, allow},
			{"netlink route", a.native, a.socket, []uint32{afNetlink, sockRaw, 0}, allow},
			{"netlink sock_diag", a.native, a.socket, []uint32{afNetlink, sockRaw, netlinkSockDiag}, eperm},
			{"mptcp", a.native, a.socket, []uint32{afInet, sockStream, 262}, noProto},
			{"sctp", a.native, a.socket, []uint32{afInet6, 5, 132}, noProto},
			{"udplite", a.native, a.socket, []uint32{afInet, sockDgram, 136}, noProto},
			{"icmp", a.native, a.socket, []uint32{afInet, sockDgram, ipprotoICMP}, eperm},
			{"icmp6", a.native, a.socket, []uint32{afInet6, sockDgram, ipprotoICMP6}, eperm},
			{"raw", a.native, a.socket, []uint32{afInet, sockRaw, 0}, eperm},
			{"packet", a.native, a.socket, []uint32{afPacket, sockRaw, 0}, eperm},
			{"vsock", a.native, a.socket, []uint32{40, sockStream, 0}, noFamily},
			{"smc", a.native, a.socket, []uint32{43, sockStream, 0}, noFamily},
			{"io_uring", a.native, ioUringSetup, nil, noSys},
			{"other call", a.native, a.socket + 1, nil, allow},
			{"compat mptcp", a.compat, a.compatSocket, []uint32{afInet, sockStream, 262}, noProto},
			{"compat tcp", a.compat, a.compatSocket, []uint32{afInet, sockStream, 0}, allow},
			{"compat socketcall", a.compat, a.compatSocketcall, nil, eperm},
			{"compat io_uring", a.compat, ioUringSetup, nil, noSys},
			{"unknown arch", otherArch, a.socket, []uint32{afInet, sockStream, 262}, allow},
		} {
			if got := run(tc.audit, tc.nr, tc.args...); got != tc.want {
				t.Errorf("%s %s: got %#x, want %#x", arch, tc.name, got, tc.want)
			}
		}
		if a.x32Socket != 0 {
			if got := run(a.native, a.x32Socket, afInet, sockStream, 262); got != noProto {
				t.Errorf("%s x32 mptcp: got %#x", arch, got)
			}
			if got := run(a.native, 0x40000000|ioUringSetup); got != noSys {
				t.Errorf("%s x32 io_uring: got %#x", arch, got)
			}
		}
	}
}

// runClassicBPF interprets the few instructions the filter uses.
func runClassicBPF(t *testing.T, prog []sockFilter, data []byte) uint32 {
	var acc uint32
	for pc := 0; pc < len(prog); pc++ {
		in := prog[pc]
		switch in.code {
		case bpfLdAbsW:
			acc = binary.LittleEndian.Uint32(data[in.k:])
		case bpfAndK:
			acc &= in.k
		case bpfJeqK:
			if acc == in.k {
				pc += int(in.jt)
			} else {
				pc += int(in.jf)
			}
		case bpfRetK:
			return in.k
		default:
			t.Fatalf("unknown instruction %#x", in.code)
		}
	}
	t.Fatal("the filter ran off its end")
	return 0
}

// MPTCP, which Landlock's TCP rules do not cover, reaches no server from a
// sandbox with a closed network: not even one on localhost.
func TestBashSandboxRefusesMPTCP(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not found (the test connects with it)")
	}
	var lc net.ListenConfig
	lc.SetMultipathTCP(true)
	ln, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	dial := fmt.Sprintf(`python3 -c 'import socket
s = socket.socket(2, 1, 262); s.settimeout(2); s.connect(("127.0.0.1", %d)); print("MPTCP-" + "CONNECTED")'`, ln.Addr().(*net.TCPAddr).Port)
	if out, _ := exec.Command("sh", "-c", dial).CombinedOutput(); !strings.Contains(string(out), "MPTCP-CONNECTED") {
		t.Skipf("MPTCP is unavailable here anyway: %s", out)
	}
	_, _, run := sandboxFixture(t, DefaultSandbox())
	if got := fmtData(run(dial)); strings.Contains(got, "MPTCP-CONNECTED") {
		t.Fatalf("an MPTCP connection left the sandbox:\n%s", got)
	}
}

// A confined command can neither read another process's memory (ouija's,
// which holds the API keys, where no PID namespace separates them) nor list
// the host's sockets through netlink's sock_diag.
func TestBashSandboxRefusesPeeking(t *testing.T) {
	a, ok := seccompArches[runtime.GOARCH]
	if !ok {
		t.Skip("no socket filter on this architecture")
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not found")
	}
	// process_vm_readv on the caller itself with nothing to copy: 0 when
	// allowed, -1 EPERM when the filter refuses it.
	peek := fmt.Sprintf(`python3 -c 'import ctypes, os
libc = ctypes.CDLL(None, use_errno=True)
r = libc.syscall(%d, os.getpid(), None, 0, None, 0, 0)
print("peek", r, ctypes.get_errno())
import socket
try:
    socket.socket(16, 3, %d).close(); print("diag ok")
except OSError as e:
    print("diag errno", e.errno)'`, a.peek[1], netlinkSockDiag)
	if out, _ := exec.Command("sh", "-c", peek).CombinedOutput(); !strings.Contains(string(out), "peek 0 ") {
		t.Skipf("process_vm_readv unavailable here anyway: %s", out)
	}
	_, _, run := sandboxFixture(t, DefaultSandbox())
	got := fmtData(run(peek))
	for _, want := range []string{"peek -1 1", "diag errno 1"} {
		if !strings.Contains(got, want) {
			t.Errorf("confined: want %q in\n%s", want, got)
		}
	}
}
