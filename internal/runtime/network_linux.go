//go:build linux

package runtime

import (
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync/atomic"

	"github.com/vishvananda/netlink"
)

const (
	networkReadyFD      = 3
	workloadInterface   = "eth0"
	maxWorkloadNetworks = 16_384
)

var networkSequence atomic.Uint32

type workloadNetwork struct {
	GuestInterface  string
	PeerInterface   string
	GuestAddress    string
	WorkloadAddress string
	Gateway         string
}

func allocateWorkloadNetwork() (workloadNetwork, error) {
	slot := networkSequence.Add(1) - 1

	if slot >= maxWorkloadNetworks {
		return workloadNetwork{}, fmt.Errorf("workload network address space exhausted")
	}

	thirdOctet := slot / 64
	fourthOctet := (slot % 64) * 4

	return workloadNetwork{
		GuestInterface:  fmt.Sprintf("kvh%04x", slot),
		PeerInterface:   fmt.Sprintf("kvw%04x", slot),
		GuestAddress:    fmt.Sprintf("10.200.%d.%d/30", thirdOctet, fourthOctet+1),
		WorkloadAddress: fmt.Sprintf("10.200.%d.%d/30", thirdOctet, fourthOctet+2),
		Gateway:         fmt.Sprintf("10.200.%d.%d", thirdOctet, fourthOctet+1),
	}, nil
}

func createGuestNetwork(network workloadNetwork) error {
	attributes := netlink.NewLinkAttrs()
	attributes.Name = network.GuestInterface

	pair := netlink.NewVeth(attributes)
	pair.PeerName = network.PeerInterface

	if err := netlink.LinkAdd(pair); err != nil {
		return fmt.Errorf("create veth pair: %w", err)
	}

	cleanup := func() {
		link, err := netlink.LinkByName(network.GuestInterface)
		if err == nil {
			_ = netlink.LinkDel(link)
		}
	}

	guestLink, err := netlink.LinkByName(network.GuestInterface)
	if err != nil {
		cleanup()
		return fmt.Errorf("find guest interface %q: %w", network.GuestInterface, err)
	}

	guestAddress, err := netlink.ParseAddr(network.GuestAddress)
	if err != nil {
		cleanup()
		return fmt.Errorf("parse guest address %q: %w", network.GuestAddress, err)
	}

	if err := netlink.AddrAdd(guestLink, guestAddress); err != nil {
		cleanup()
		return fmt.Errorf("assign %s to %s: %w", network.GuestAddress, network.GuestInterface, err)
	}

	if err := netlink.LinkSetUp(guestLink); err != nil {
		cleanup()
		return fmt.Errorf("bring guest interface %s up: %w", network.GuestInterface, err)
	}

	return nil
}

func moveNetworkToProcess(network workloadNetwork, pid int) error {
	peer, err := netlink.LinkByName(network.PeerInterface)
	if err != nil {
		return fmt.Errorf("find workload peer %q: %w", network.PeerInterface, err)
	}

	if err := netlink.LinkSetNsPid(peer, pid); err != nil {
		return fmt.Errorf("move interface %s to process %d: %w", network.PeerInterface, pid, err)
	}

	return nil
}

func configureWorkloadNetwork(peerInterface string, workloadAddress string, gatewayAddress string) error {
	loopback, err := netlink.LinkByName("lo")
	if err != nil {
		return fmt.Errorf("find loopback interface: %w", err)
	}

	if err := netlink.LinkSetUp(loopback); err != nil {
		return fmt.Errorf("bring loopback interface up: %w", err)
	}

	peer, err := netlink.LinkByName(peerInterface)
	if err != nil {
		return fmt.Errorf("find workload interface %q: %w", peerInterface, err)
	}

	if err := netlink.LinkSetName(peer, workloadInterface); err != nil {
		return fmt.Errorf("rename interface %s to %s: %w", peerInterface, workloadInterface, err)
	}

	ethernet, err := netlink.LinkByName(workloadInterface)
	if err != nil {
		return fmt.Errorf("find renamed workload interface %q: %w", workloadInterface, err)
	}

	address, err := netlink.ParseAddr(workloadAddress)
	if err != nil {
		return fmt.Errorf("parse workload address %q: %w", workloadAddress, err)
	}

	if err := netlink.AddrAdd(ethernet, address); err != nil {
		return fmt.Errorf("assign %s to %s: %w", workloadAddress, workloadInterface, err)
	}

	if err := netlink.LinkSetUp(ethernet); err != nil {
		return fmt.Errorf("bring workload interface %s up: %w", workloadInterface, err)
	}

	gateway := net.ParseIP(gatewayAddress)

	if gateway == nil || gateway.To4() == nil {
		return fmt.Errorf("gateway %q is not a valid IPv4 address", gatewayAddress)
	}

	if err := netlink.RouteAdd(&netlink.Route{LinkIndex: ethernet.Attrs().Index, Gw: gateway}); err != nil {
		return fmt.Errorf("add default route through %s: %w", gatewayAddress, err)
	}

	return nil
}

func removeGuestNetwork(network workloadNetwork) error {
	link, err := netlink.LinkByName(network.GuestInterface)
	if err != nil {
		// When the workload network namespace disappears, Linux may
		// already have destroyed both ends of the veth pair.
		return nil
	}

	if err := netlink.LinkDel(link); err != nil {
		return fmt.Errorf("delete guest interface %s: %w", network.GuestInterface, err)
	}

	return nil
}

func waitForNetwork() error {
	ready := os.NewFile(networkReadyFD, "kernlet-network-ready")
	if ready == nil {
		return fmt.Errorf("open network readiness descriptor")
	}

	defer ready.Close()

	var signal [1]byte

	if _, err := io.ReadFull(ready, signal[:]); err != nil {
		return fmt.Errorf("wait for network configuration: %w", err)
	}

	if signal[0] != 1 {
		return fmt.Errorf("received invalid network readiness signal %d", signal[0])
	}

	return nil
}

func workloadEnvironment(imageEnvironment []string, gateway string) []string {
	environment := make([]string, 0, len(imageEnvironment)+1)

	for _, value := range imageEnvironment {
		if strings.HasPrefix(value, "KERNLET_GATEWAY=") {
			continue
		}

		environment = append(environment, value)
	}

	environment = append(environment, "KERNLET_GATEWAY="+gateway)

	return environment
}
