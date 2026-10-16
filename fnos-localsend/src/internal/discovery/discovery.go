// Package discovery implements LocalSend v2.2 device discovery:
// multicast announcements (224.0.0.167:53317), a TTL-based device
// registry, and manual IP(:port) probing.
package discovery

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/alexindigo/localsend-nas/internal/localsend"
)

const (
	// MulticastGroup is the protocol-fixed IPv4 multicast group.
	MulticastGroup = "224.0.0.167"
	// DefaultPort is the protocol-default LocalSend TCP/UDP port.
	DefaultPort = 53317

	announceInterval = 30 * time.Second
	announceJitter   = 5 * time.Second
	registryTTL      = 120 * time.Second
	janitorInterval  = 30 * time.Second
)

// Device is a registry entry.
type Device struct {
	Info     localsend.Info
	IP       string
	LastSeen time.Time
	Manual   bool // manual entries evade TTL eviction and persist on disk
}

// Discovery runs announcement/listen loops and owns the device registry.
type Discovery struct {
	info   localsend.Info // our own announcement payload
	client *localsend.Client
	log    *slog.Logger

	mu      sync.Mutex
	devices map[string]*Device // fingerprint → device

	targetsPath string

	listening atomic.Bool // joined ≥1 multicast interface
}

// New builds a Discovery. info is our own Info DTO (announce:false is set
// on send). dataDir stores persisted manual targets (targets.json).
func New(info localsend.Info, client *localsend.Client, dataDir string, log *slog.Logger) *Discovery {
	return &Discovery{
		info:        info,
		client:      client,
		log:         log,
		devices:     map[string]*Device{},
		targetsPath: filepath.Join(dataDir, "targets.json"),
	}
}

// Start launches announce, listen, and janitor loops; all stop with ctx.
func (d *Discovery) Start(ctx context.Context) {
	d.loadTargets()
	go d.announceLoop(ctx)
	go d.listenLoop(ctx)
	go d.janitorLoop(ctx)
}

// Listening reports whether the multicast listener joined any interface.
func (d *Discovery) Listening() bool { return d.listening.Load() }

// Get resolves a fingerprint to a known device.
func (d *Discovery) Get(fingerprint string) (Device, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	dev, ok := d.devices[fingerprint]
	if !ok {
		return Device{}, false
	}
	return *dev, true
}

// Snapshot returns all known devices sorted by alias.
func (d *Discovery) Snapshot() []Device {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]Device, 0, len(d.devices))
	for _, dev := range d.devices {
		out = append(out, *dev)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Info.Alias != out[j].Info.Alias {
			return out[i].Info.Alias < out[j].Info.Alias
		}
		return out[i].Info.Fingerprint < out[j].Info.Fingerprint
	})
	return out
}

// Upsert records a device from a multicast announcement or a /register
// callback (invoked by the LocalSend server). Self-announcements are ignored.
func (d *Discovery) Upsert(info localsend.Info, sourceIP string) {
	if info.Fingerprint == "" || info.Fingerprint == d.info.Fingerprint {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if dev, ok := d.devices[info.Fingerprint]; ok {
		dev.Info = info
		dev.IP = sourceIP
		dev.LastSeen = time.Now()
		return
	}
	d.devices[info.Fingerprint] = &Device{Info: info, IP: sourceIP, LastSeen: time.Now()}
	d.log.Info("device discovered", "alias", info.Alias, "ip", sourceIP, "fingerprint", shortFP(info.Fingerprint))
}

// Add probes address (host or host:port) and registers the device as a
// persisted manual target.
func (d *Discovery) Add(ctx context.Context, address string) (*Device, error) {
	host, port, err := parseAddress(address)
	if err != nil {
		return nil, err
	}
	info, err := d.client.Probe(ctx, host, port)
	if err != nil {
		return nil, fmt.Errorf("probe %s: %w", address, err)
	}
	if info.Fingerprint == d.info.Fingerprint {
		return nil, errors.New("address points to this node itself")
	}
	// v2.2 /info omits the port (it is a transport detail carried by
	// announcements); the probed address's port is authoritative.
	if info.Port == 0 {
		info.Port = port
	}
	d.mu.Lock()
	dev, ok := d.devices[info.Fingerprint]
	if !ok {
		dev = &Device{}
		d.devices[info.Fingerprint] = dev
	}
	dev.Info = *info
	dev.IP = host
	dev.LastSeen = time.Now()
	dev.Manual = true
	d.mu.Unlock()
	if err := d.saveTargets(); err != nil {
		d.log.Warn("persist manual targets", "error", err)
	}
	d.log.Info("manual target added", "alias", info.Alias, "address", net.JoinHostPort(host, fmt.Sprint(port)))
	cp := *dev
	return &cp, nil
}

// Remove forgets a device (manual targets are also unpersisted).
// Note: a still-online discovered device will reappear on its next
// announcement (~30s); removal is for clearing stale entries.
func (d *Discovery) Remove(fingerprint string) bool {
	d.mu.Lock()
	_, ok := d.devices[fingerprint]
	if ok {
		delete(d.devices, fingerprint)
	}
	d.mu.Unlock()
	if !ok {
		return false
	}
	if err := d.saveTargets(); err != nil {
		d.log.Warn("persist manual targets", "error", err)
	}
	return true
}

// --- announce ---

func (d *Discovery) announceLoop(ctx context.Context) {
	d.announce()
	for {
		t := announceInterval + jitter()
		select {
		case <-ctx.Done():
			return
		case <-time.After(t):
			d.announce()
		}
	}
}

func jitter() time.Duration {
	n, err := rand.Int(rand.Reader, big.NewInt(int64(2*announceJitter)))
	if err != nil {
		return 0
	}
	return time.Duration(n.Int64()) - announceJitter
}

// announce sends our info blob to the multicast group via every eligible
// interface. Sockets are per-send so interface changes are picked up.
func (d *Discovery) announce() {
	payload, err := json.Marshal(d.announcement())
	if err != nil {
		return
	}
	group := &net.UDPAddr{IP: net.ParseIP(MulticastGroup), Port: DefaultPort}
	for _, ip := range eligibleIPv4() {
		conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: ip, Port: 0})
		if err != nil {
			continue
		}
		if _, err := conn.WriteToUDP(payload, group); err != nil {
			d.log.Debug("multicast announce failed", "iface-ip", ip, "error", err)
		}
		conn.Close()
	}
}

func (d *Discovery) announcement() localsend.Info {
	info := d.info
	info.Announce = true
	return info
}

// --- listen ---

func (d *Discovery) listenLoop(ctx context.Context) {
	group := &net.UDPAddr{IP: net.ParseIP(MulticastGroup), Port: DefaultPort}
	ifaces, err := net.Interfaces()
	if err != nil {
		d.log.Warn("list interfaces", "error", err)
		return
	}
	var wg sync.WaitGroup
	joined := 0
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagMulticast == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		conn, err := net.ListenMulticastUDP("udp4", &iface, group)
		if err != nil {
			d.log.Debug("join multicast group failed", "iface", iface.Name, "error", err)
			continue
		}
		joined++
		wg.Add(1)
		go func(ifaceName string) {
			defer wg.Done()
			d.readAnnouncements(ctx, conn, ifaceName)
		}(iface.Name)
	}
	if joined == 0 {
		d.log.Warn("no multicast-capable interface; not listening for announcements")
	} else {
		d.listening.Store(true)
	}
	// Socket reads poll with deadlines, so ctx cancellation is observed
	// without closing sockets here.
	wg.Wait()
}

func (d *Discovery) readAnnouncements(ctx context.Context, conn *net.UDPConn, ifaceName string) {
	defer conn.Close()
	buf := make([]byte, 64*1024)
	for {
		if ctx.Err() != nil {
			return
		}
		// Deadline polling keeps the loop responsive to ctx cancellation.
		conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		n, src, err := conn.ReadFromUDP(buf)
		if err != nil {
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				continue
			}
			return
		}
		var info localsend.Info
		if err := json.Unmarshal(buf[:n], &info); err != nil {
			continue // not a LocalSend announcement
		}
		d.Upsert(info, src.IP.String())
	}
}

// --- registry maintenance ---

func (d *Discovery) janitorLoop(ctx context.Context) {
	t := time.NewTicker(janitorInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			d.evict()
		}
	}
}

func (d *Discovery) evict() {
	cutoff := time.Now().Add(-registryTTL)
	d.mu.Lock()
	defer d.mu.Unlock()
	for fp, dev := range d.devices {
		if !dev.Manual && dev.LastSeen.Before(cutoff) {
			d.log.Info("device expired", "alias", dev.Info.Alias, "fingerprint", shortFP(fp))
			delete(d.devices, fp)
		}
	}
}

// --- manual target persistence ---

func (d *Discovery) loadTargets() {
	data, err := os.ReadFile(d.targetsPath)
	if err != nil {
		return // first start
	}
	var addrs []string
	if err := json.Unmarshal(data, &addrs); err != nil {
		d.log.Warn("parse targets.json", "error", err)
		return
	}
	for _, addr := range addrs {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if _, err := d.Add(ctx, addr); err != nil {
			d.log.Info("saved target unreachable", "address", addr, "error", err)
		}
		cancel()
	}
}

func (d *Discovery) saveTargets() error {
	d.mu.Lock()
	var addrs []string
	for _, dev := range d.devices {
		if dev.Manual {
			addrs = append(addrs, net.JoinHostPort(dev.IP, fmt.Sprint(dev.Info.Port)))
		}
	}
	d.mu.Unlock()
	sort.Strings(addrs)
	data, err := json.MarshalIndent(addrs, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(d.targetsPath, data, 0o600)
}

// --- helpers ---

func parseAddress(address string) (host string, port int, err error) {
	address = strings.TrimSpace(address)
	if address == "" {
		return "", 0, errors.New("empty address")
	}
	if h, p, splitErr := net.SplitHostPort(address); splitErr == nil {
		if _, err := fmt.Sscanf(p, "%d", &port); err != nil || port < 1 || port > 65535 {
			return "", 0, fmt.Errorf("invalid port in %q", address)
		}
		return h, port, nil
	}
	if strings.Contains(address, ":") {
		return "", 0, fmt.Errorf("invalid address %q: want host or host:port", address)
	}
	return address, DefaultPort, nil
}

// eligibleIPv4 lists IPv4 addresses of up, multicast-capable, non-loopback
// interfaces.
func eligibleIPv4() []net.IP {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out []net.IP
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagMulticast == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			var ip net.IP
			switch a := addr.(type) {
			case *net.IPNet:
				ip = a.IP
			case *net.IPAddr:
				ip = a.IP
			}
			if ip4 := ip.To4(); ip4 != nil {
				out = append(out, ip4)
			}
		}
	}
	return out
}

func shortFP(fp string) string {
	if len(fp) > 12 {
		return fp[:12]
	}
	return fp
}
