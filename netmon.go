package main

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// pidSocketInodes returns the set of socket inodes held by pid,
// read from /proc/<pid>/fd. The caller must have permission to read
// that process' file descriptors (same user or root).
func pidSocketInodes(pid int) (map[uint64]bool, error) {
	dir := fmt.Sprintf("/proc/%d/fd", pid)
	fds, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	inodes := map[uint64]bool{}
	for _, fd := range fds {
		lnk, err := os.Readlink(filepath.Join(dir, fd.Name()))
		if err != nil {
			continue
		}
		if !strings.HasPrefix(lnk, "socket:[") || !strings.HasSuffix(lnk, "]") {
			continue
		}
		s := lnk[len("socket:[") : len(lnk)-1]
		if in, err := strconv.ParseUint(s, 10, 64); err == nil {
			inodes[in] = true
		}
	}
	return inodes, nil
}

// establishedRemoteInodes returns the socket inodes of all ESTABLISHED
// TCP connections to remoteIP:remotePort, parsed from /proc/net/tcp.
func establishedRemoteInodes(remoteIP string, remotePort int) (map[uint64]bool, error) {
	ip4 := net.ParseIP(remoteIP)
	if ip4 = ip4.To4(); ip4 == nil {
		return nil, fmt.Errorf("remoteIP must be IPv4, got %q", remoteIP)
	}
	// /proc/net/tcp stores addresses as little-endian hex, port as 4-digit hex.
	rem := fmt.Sprintf("%02X%02X%02X%02X:%04X", ip4[3], ip4[2], ip4[1], ip4[0], remotePort)
	data, err := os.ReadFile("/proc/net/tcp")
	if err != nil {
		return nil, err
	}
	inodes := map[uint64]bool{}
	lines := strings.Split(string(data), "\n")
	for i, line := range lines {
		if i == 0 || strings.TrimSpace(line) == "" {
			continue
		}
		f := strings.Fields(line)
		// sl local rem st txq:rxq tr:tm retrnsmt uid timeout inode ref pointer drops
		if len(f) < 10 {
			continue
		}
		if f[3] == "01" && f[2] == rem {
			if in, err := strconv.ParseUint(f[9], 10, 64); err == nil {
				inodes[in] = true
			}
		}
	}
	return inodes, nil
}

// PIDConnectedTo reports whether pid holds an ESTABLISHED TCP connection
// to remoteIP:remotePort. It is how the CLI tells "ffmpeg is alive" from
// "ffmpeg is actually pulling from mediamtx".
func PIDConnectedTo(pid int, remoteIP string, remotePort int) (bool, error) {
	if pid <= 0 {
		return false, nil
	}
	remote, err := establishedRemoteInodes(remoteIP, remotePort)
	if err != nil {
		return false, err
	}
	own, err := pidSocketInodes(pid)
	if err != nil {
		return false, err
	}
	for in := range own {
		if remote[in] {
			return true, nil
		}
	}
	return false, nil
}
