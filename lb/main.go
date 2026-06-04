package main

import (
	"log"
	"net"
	"os"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

const (
	DEFAULT_PORT    = 9999
	DEFAULT_BACKLOG = 8192
)

func createListener(port int, backlog int) (int, error) {
	fd, err := unix.Socket(unix.AF_INET, unix.SOCK_STREAM|unix.SOCK_NONBLOCK|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return -1, err
	}
	unix.SetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_REUSEADDR, 1)
	unix.SetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_REUSEPORT, 1)
	// TCP_DEFER_ACCEPT is Linux specific
	unix.SetsockoptInt(fd, unix.IPPROTO_TCP, unix.TCP_DEFER_ACCEPT, 1)

	addr := &unix.SockaddrInet4{Port: port}
	copy(addr.Addr[:], net.ParseIP("0.0.0.0").To4())

	if err := unix.Bind(fd, addr); err != nil {
		return -1, err
	}
	if err := unix.Listen(fd, backlog); err != nil {
		return -1, err
	}
	return fd, nil
}

func sendFds(sock int, path string, fds []int) {
	addr := &unix.SockaddrUnix{Name: path}
	rights := unix.UnixRights(fds...)
	err := unix.Sendmsg(sock, []byte("1"), rights, addr, 0)
	if err != nil {
		// Log but continue
		_ = err
	}
}

func main() {
	portStr := os.Getenv("PORT")
	port := DEFAULT_PORT
	if p, err := strconv.Atoi(portStr); err == nil {
		port = p
	}

	upstreamsEnv := os.Getenv("UPSTREAMS")
	var upstreams []string
	for _, s := range strings.Split(upstreamsEnv, ",") {
		if s != "" {
			upstreams = append(upstreams, s)
		}
	}

	listenFd, err := createListener(port, DEFAULT_BACKLOG)
	if err != nil {
		log.Fatalf("failed to create listener: %v", err)
	}

	udsFd, err := unix.Socket(unix.AF_UNIX, unix.SOCK_DGRAM, 0)
	if err != nil {
		log.Fatalf("failed to create UDS socket: %v", err)
	}
	unix.SetsockoptInt(udsFd, unix.SOL_SOCKET, unix.SO_SNDBUF, 16*1024*1024)

	epfd, err := unix.EpollCreate1(0)
	if err != nil {
		log.Fatalf("epoll_create1 error: %v", err)
	}

	event := &unix.EpollEvent{
		Events: unix.EPOLLIN,
		Fd:     int32(listenFd),
	}
	if err := unix.EpollCtl(epfd, unix.EPOLL_CTL_ADD, listenFd, event); err != nil {
		log.Fatalf("epoll_ctl error: %v", err)
	}

	events := make([]unix.EpollEvent, 1024)
	var rr int

	batches := make([][]int, len(upstreams))
	for i := range batches {
		batches[i] = make([]int, 0, 64)
	}

	log.Printf("Fallback LB (Pure Go) started with epoll on port %d", port)

	for {
		n, err := unix.EpollWait(epfd, events, -1)
		if err != nil {
			if err == unix.EINTR {
				continue
			}
			log.Fatalf("epoll_wait error: %v", err)
		}

		for i := range batches {
			batches[i] = batches[i][:0]
		}

		for i := 0; i < n; i++ {
			if int(events[i].Fd) == listenFd {
				for {
					clientFd, _, err := unix.Accept4(listenFd, unix.SOCK_NONBLOCK|unix.SOCK_CLOEXEC)
					if err != nil {
						break
					}
					unix.SetsockoptInt(clientFd, unix.IPPROTO_TCP, unix.TCP_NODELAY, 1)

					targetIdx := rr % len(upstreams)
					batches[targetIdx] = append(batches[targetIdx], clientFd)
					rr++
				}
			}
		}

		for i, batch := range batches {
			if len(batch) > 0 {
				for j := 0; j < len(batch); j += 16 {
					end := j + 16
					if end > len(batch) {
						end = len(batch)
					}
					sendFds(udsFd, upstreams[i], batch[j:end])
				}
				for _, fd := range batch {
					unix.Close(fd)
				}
			}
		}
	}
}
