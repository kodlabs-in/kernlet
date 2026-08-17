//go:build linux

package main

import (
	"fmt"
	"io"
	"net"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/kodlabs-in/kernlet/internal/guestproto"
)

func startNetworkCheckServer() error {
	listener, err := net.Listen("tcp4", fmt.Sprintf(":%d", guestproto.NetworkCheckPort))
	if err != nil {
		return fmt.Errorf("listen for workload network checks: %w", err)
	}

	go func() {
		if err := serveNetworkChecks(listener); err != nil {
			panic(err)
		}
	}()

	return nil
}

func serveNetworkChecks(listener net.Listener) error {
	for {
		connection, err := listener.Accept()
		if err != nil {
			return fmt.Errorf("accept workload network check: %w", err)
		}

		go handleNetworkCheck(connection)
	}
}

func handleNetworkCheck(connection net.Conn) {
	defer connection.Close()

	_ = connection.SetDeadline(
		time.Now().Add(2 * time.Second),
	)

	request := make([]byte, len(guestproto.NetworkCheckRequest))

	if _, err := io.ReadFull(connection, request); err != nil {
		return
	}

	if string(request) != guestproto.NetworkCheckRequest {
		return
	}

	_, _ = io.WriteString(connection, guestproto.NetworkCheckResponse)
}

func verifyGuestNetwork(gateway string) error {
	address := net.JoinHostPort(gateway, strconv.Itoa(guestproto.NetworkCheckPort))

	connection, err := net.DialTimeout("tcp4", address, 2*time.Second)
	if err != nil {
		return fmt.Errorf("connect to guest gateway %s: %w", address, err)
	}

	defer connection.Close()

	if err := connection.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		return fmt.Errorf("set network-check deadline: %w", err)
	}

	if _, err := io.WriteString(connection, guestproto.NetworkCheckRequest); err != nil {
		return fmt.Errorf("send network-check request: %w", err)
	}

	response := make([]byte, len(guestproto.NetworkCheckResponse))

	if _, err := io.ReadFull(connection, response); err != nil {
		return fmt.Errorf("read network-check response: %w", err)
	}

	if string(response) != guestproto.NetworkCheckResponse {
		return fmt.Errorf("unexpected network-check response %q", string(response))
	}

	return nil
}

func networkInterfaceSummary() (string, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return "", fmt.Errorf("list network interfaces: %w", err)
	}

	summary := make([]string, 0, len(interfaces))

	for _, networkInterface := range interfaces {
		state := "down"

		if networkInterface.Flags&net.FlagUp != 0 {
			state = "up"
		}

		summary = append(summary, networkInterface.Name+":"+state)
	}

	sort.Strings(summary)

	return strings.Join(summary, ","), nil
}
