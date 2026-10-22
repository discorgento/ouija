//go:build linux

package engine

import (
	"fmt"
	"runtime"
	"syscall"
	"unsafe"
)

// Landlock filters TCP and (since ABI v10) UDP, and nothing else: ICMP
// (where net.ipv4.ping_group_range opens ping sockets to users), MPTCP (on
// by default since Linux 5.6, and Landlock's TCP rules do not cover it),
// SCTP, VSOCK and the rest would carry data out of a sandbox with a closed
// network. A seccomp filter, installed after Landlock and before the shell
// starts, lets socket() make only what Landlock filters, Unix sockets
// (scoped by Landlock) and the local netlink and crypto sockets: TCP and UDP
// over IPv4 and IPv6. MPTCP and the other protocols fail as unsupported, so
// programs that try MPTCP first fall back to TCP; ICMP, raw and packet
// sockets fail with EPERM. io_uring is refused too, since it makes sockets
// without the socket() system call. Like Landlock the filter is inherited by
// every process the command starts and cannot be lifted.

// seccompArch is what the filter needs to know of an architecture: its audit
// id and the numbers of the calls it filters, natively and for the 32-bit
// programs it can run.
type seccompArch struct {
	native, compat    uint32 // AUDIT_ARCH_*
	socket, x32Socket uint32 // x32Socket: 0 where there is no x32 ABI
	compatSocket      uint32
	compatSocketcall  uint32 // the 32-bit socketcall multiplexer
	seccompSyscall    uintptr

	// The calls that read or write another process's memory or descriptors
	// (ptrace, process_vm_readv, process_vm_writev), natively, for x32 and
	// for 32-bit programs: refused, so a confined command cannot read the
	// API keys in ouija's memory where it shares ouija's PID namespace.
	peek, x32Peek, compatPeek []uint32
}

// ioUringSetup is io_uring_setup's number on every architecture, for 32-bit
// programs too (it was added after the tables were unified).
const ioUringSetup = 425

// pidfdGetfd copies another process's file descriptor; its number is the
// same everywhere too.
const pidfdGetfd = 438

// seccompArches are the architectures the filter knows; elsewhere it is not
// installed (security.md says so).
var seccompArches = map[string]seccompArch{
	"amd64": {native: 0xc000003e, compat: 0x40000003, socket: 41, x32Socket: 0x40000000 | 41,
		compatSocket: 359, compatSocketcall: 102, seccompSyscall: 317,
		peek: []uint32{101, 310, 311}, x32Peek: []uint32{0x40000000 | 521, 0x40000000 | 539, 0x40000000 | 540},
		compatPeek: []uint32{26, 347, 348}},
	"arm64": {native: 0xc00000b7, compat: 0x40000028, socket: 198,
		compatSocket: 281, compatSocketcall: 102, seccompSyscall: 277,
		peek: []uint32{117, 270, 271}, compatPeek: []uint32{26, 376, 377}},
}

const (
	bpfLdAbsW = 0x20 // BPF_LD | BPF_W | BPF_ABS
	bpfJeqK   = 0x15 // BPF_JMP | BPF_JEQ | BPF_K
	bpfAndK   = 0x54 // BPF_ALU | BPF_AND | BPF_K
	bpfRetK   = 0x06 // BPF_RET | BPF_K

	seccompRetAllow = 0x7fff0000
	seccompRetErrno = 0x00050000

	// struct seccomp_data offsets (little endian: an argument's low word).
	seccompNr      = 0
	seccompArchOff = 4
	seccompArg0    = 16

	afUnix, afInet, afInet6, afNetlink, afPacket, afAlg = 1, 2, 10, 16, 17, 38
	netlinkSockDiag                                     = 4 // every socket on the host, listed
	sockStream, sockDgram, sockRaw                      = 1, 2, 3
	sockTypeMask                                        = 0xf // the type without SOCK_NONBLOCK, SOCK_CLOEXEC
	ipprotoICMP, ipprotoTCP, ipprotoUDP, ipprotoICMP6   = 1, 6, 17, 58

	prSetNoNewPrivs        = 38
	seccompSetModeFilter   = 1
	seccompFilterFlagTsync = 1
)

// bpfInsn is a classic BPF instruction with symbolic jump targets.
type bpfInsn struct {
	code   uint16
	k      uint32
	jt, jf string // "" is the next instruction
	label  string
}

// sockFilter is the kernel's struct sock_filter.
type sockFilter struct {
	code   uint16
	jt, jf uint8
	k      uint32
}

// sockFprog is the kernel's struct sock_fprog.
type sockFprog struct {
	len    uint16
	filter *sockFilter
}

// socketFilter assembles the filter for arch a: socket() makes TCP and UDP
// sockets over IPv4 and IPv6, Unix, netlink and AF_ALG sockets, and nothing
// else; io_uring_setup fails as if io_uring did not exist. Every other
// system call is let through.
func socketFilter(a seccompArch, net socketPolicy) ([]sockFilter, error) {
	ld := func(label string, off uint32) bpfInsn { return bpfInsn{code: bpfLdAbsW, k: off, label: label} }
	jeq := func(k uint32, jt, jf string) bpfInsn { return bpfInsn{code: bpfJeqK, k: k, jt: jt, jf: jf} }
	ret := func(label string, k uint32) bpfInsn { return bpfInsn{code: bpfRetK, k: k, label: label} }
	errno := func(e syscall.Errno) uint32 { return seccompRetErrno | uint32(e) }
	deny := func(nrs ...uint32) []bpfInsn {
		var out []bpfInsn
		for _, nr := range nrs {
			out = append(out, jeq(nr, "deny", ""))
		}
		return out
	}
	// With the network unrestricted, sockets are not checked at all; the
	// rules against reading other processes stay.
	check, socketcall := "check", "deny"
	if net == socketsAll {
		check, socketcall = "allow", "allow"
	}
	prog := []bpfInsn{
		ld("", seccompArchOff),
		jeq(a.native, "native", ""),
		jeq(a.compat, "compat", "allow"),
		ld("native", seccompNr),
		jeq(a.socket, check, ""),
	}
	prog = append(prog, deny(append(a.peek, pidfdGetfd)...)...)
	if a.x32Socket != 0 {
		prog = append(prog,
			jeq(a.x32Socket, check, ""),
			jeq(0x40000000|ioUringSetup, "nosys", ""))
		prog = append(prog, deny(append(a.x32Peek, 0x40000000|pidfdGetfd)...)...)
	}
	prog = append(prog,
		jeq(ioUringSetup, "nosys", "allow"),
		ld("compat", seccompNr),
		jeq(a.compatSocket, check, ""),
		jeq(ioUringSetup, "nosys", ""))
	prog = append(prog, deny(append(a.compatPeek, pidfdGetfd)...)...)
	// UDP, where the network is closed: Landlock filters it only from ABI
	// v10, and a closed sandbox needs none (no port open, no DNS).
	dgram := []bpfInsn{
		ld("dgram", seccompArg0+16), // protocol: UDP, not ICMP or UDP-Lite
		jeq(0, "udp", ""),
		jeq(ipprotoUDP, "udp", ""),
		jeq(ipprotoICMP, "deny", ""),
		jeq(ipprotoICMP6, "deny", "noproto"),
	}
	if net == socketsClosed {
		dgram = append(dgram, ret("udp", errno(syscall.EPERM)))
	} else {
		dgram = append(dgram, ret("udp", seccompRetAllow))
	}
	prog = append(prog,
		// 32-bit programs may reach socket() through socketcall, whose
		// arguments sit in memory a filter cannot read: refused whole.
		jeq(a.compatSocketcall, socketcall, "allow"),
		ld("check", seccompArg0), // domain
		jeq(afInet, "inet", ""),
		jeq(afInet6, "inet", ""),
		jeq(afUnix, "allow", ""),
		jeq(afNetlink, "netlink", ""),
		jeq(afAlg, "allow", ""),
		jeq(afPacket, "deny", "nofamily"),
		ld("netlink", seccompArg0+16), // protocol: not sock_diag
		jeq(netlinkSockDiag, "deny", "allow"),
		ld("inet", seccompArg0+8), // type
		bpfInsn{code: bpfAndK, k: sockTypeMask},
		jeq(sockRaw, "deny", ""),
		jeq(sockStream, "stream", ""),
		jeq(sockDgram, "dgram", "noproto"),
		ld("stream", seccompArg0+16), // protocol: TCP, not MPTCP or SCTP
		jeq(0, "allow", ""),
		jeq(ipprotoTCP, "allow", "noproto"),
	)
	prog = append(prog, dgram...)
	prog = append(prog,
		ret("noproto", errno(syscall.EPROTONOSUPPORT)),
		ret("nofamily", errno(syscall.EAFNOSUPPORT)),
		ret("nosys", errno(syscall.ENOSYS)),
		ret("deny", errno(syscall.EPERM)),
		ret("allow", seccompRetAllow),
	)
	at := map[string]int{}
	for i, in := range prog {
		if in.label != "" {
			at[in.label] = i
		}
	}
	out := make([]sockFilter, len(prog))
	for i, in := range prog {
		off := func(label string) (uint8, error) {
			if label == "" {
				return 0, nil
			}
			j, ok := at[label]
			if !ok || j <= i || j-i-1 > 255 {
				return 0, fmt.Errorf("seccomp filter: bad jump to %q", label)
			}
			return uint8(j - i - 1), nil
		}
		jt, err := off(in.jt)
		if err != nil {
			return nil, err
		}
		jf, err := off(in.jf)
		if err != nil {
			return nil, err
		}
		out[i] = sockFilter{code: in.code, jt: jt, jf: jf, k: in.k}
	}
	return out, nil
}

// confineSockets installs the filter on every thread of the process (the
// shell is exec'd next). No-op on an architecture the filter does not know.
// socketPolicy is what the network part of the sandbox policy leaves open.
type socketPolicy int

const (
	socketsClosed socketPolicy = iota // no port: TCP and UDP sockets only, no UDP
	socketsPorts                      // some ports: TCP and UDP sockets only
	socketsAll                        // network.all: any socket
)

func confineSockets(net socketPolicy) error {
	a, ok := seccompArches[runtime.GOARCH]
	if !ok {
		return nil // sandboxAvailable refused this architecture already
	}
	filter, err := socketFilter(a, net)
	if err != nil {
		return err
	}
	// Landlock set no_new_privs already; a filter needs it.
	if _, _, e := syscall.RawSyscall6(syscall.SYS_PRCTL, prSetNoNewPrivs, 1, 0, 0, 0, 0); e != 0 {
		return fmt.Errorf("prctl(PR_SET_NO_NEW_PRIVS): %w", e)
	}
	prog := sockFprog{len: uint16(len(filter)), filter: &filter[0]}
	// TSYNC: every thread, or none (r1 then names a thread that refused).
	r1, _, e := syscall.RawSyscall(a.seccompSyscall, seccompSetModeFilter, seccompFilterFlagTsync,
		uintptr(unsafe.Pointer(&prog)))
	if e != 0 || r1 != 0 {
		return fmt.Errorf("seccomp socket filter: %v (thread %d)", e, r1)
	}
	runtime.KeepAlive(filter)
	return nil
}
