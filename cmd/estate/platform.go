package main

import (
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// defaultDataDir is $XDG_DATA_HOME/estate or ~/.local/share/estate on Linux,
// %AppData%\estate on Windows and ~/Library/Application Support/estate on
// macOS.
func defaultDataDir() string {
	if dir := os.Getenv("XDG_DATA_HOME"); dir != "" {
		return filepath.Join(dir, "estate")
	}
	if runtime.GOOS != "windows" && runtime.GOOS != "darwin" {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, ".local", "share", "estate")
		}
	}
	if dir, err := os.UserConfigDir(); err == nil {
		return filepath.Join(dir, "estate")
	}
	return "estate-data"
}

// lanIPv4 returns the machine's private IPv4 address for phones on the same
// network: the address of the default route if it is private, otherwise the
// first private address of a physical-looking interface.
func lanIPv4() string {
	if conn, err := net.Dial("udp4", "192.0.2.1:9"); err == nil {
		// UDP "connect" sends nothing; it only selects the outgoing interface.
		ip := conn.LocalAddr().(*net.UDPAddr).IP
		conn.Close()
		if ip.IsPrivate() {
			return ip.String()
		}
	}
	interfaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	type candidate struct {
		ip   net.IP
		rank int
	}
	var candidates []candidate
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 || virtualInterface(iface.Name) {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ipNet, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}
			ip := ipNet.IP.To4()
			if ip == nil || !ip.IsPrivate() {
				continue
			}
			rank := 2
			switch {
			case ip[0] == 192 && ip[1] == 168:
				rank = 0
			case ip[0] == 10:
				rank = 1
			}
			candidates = append(candidates, candidate{ip, rank})
		}
	}
	if len(candidates) == 0 {
		return ""
	}
	sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].rank < candidates[j].rank })
	return candidates[0].ip.String()
}

func virtualInterface(name string) bool {
	lower := strings.ToLower(name)
	for _, prefix := range []string{"docker", "br-", "veth", "virbr", "vmnet", "vboxnet", "vethernet", "tailscale", "zt", "utun", "wg"} {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return strings.Contains(lower, "virtualbox") || strings.Contains(lower, "hyper-v")
}

// openTable opens the table screen: a fullscreen kiosk Chrome window on
// Linux when available, otherwise the default browser.
func openTable(url string) error {
	switch runtime.GOOS {
	case "windows":
		return start(exec.Command("rundll32", "url.dll,FileProtocolHandler", url))
	case "darwin":
		return start(exec.Command("open", url))
	default:
		if os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
			return errors.New("no graphical display")
		}
		for _, browser := range []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser"} {
			if path, err := exec.LookPath(browser); err == nil {
				// No click is needed for sound on the table screen (the TV is often driven from the
				// phones, so nobody clicks it).
				return start(exec.Command(path, "--kiosk", "--app="+url, "--no-first-run", "--noerrdialogs", "--autoplay-policy=no-user-gesture-required"))
			}
		}
		if path, err := exec.LookPath("xdg-open"); err == nil {
			return start(exec.Command(path, url))
		}
		return errors.New("no browser found")
	}
}

// start runs a detached helper and reaps it when it exits.
func start(cmd *exec.Cmd) error {
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
