package snowflake

import (
	"errors"
	"hash/fnv"
	"net"
	"sync"

	"github.com/bwmarrin/snowflake"
)

var (
	nodeOnce sync.Once
	node     *snowflake.Node
	nodeErr  error
)

func init() {
	snowflake.Epoch = 1735689600000
	snowflake.NodeBits = 16
	snowflake.StepBits = 8
}

// New returns the process-global Snowflake node used for event identifiers.
func New() (*snowflake.Node, error) {
	nodeOnce.Do(func() {
		node, nodeErr = newNode()
	})
	return node, nodeErr
}

func newNode() (*snowflake.Node, error) {
	addresses, err := net.InterfaceAddrs()
	if err != nil {
		return nil, err
	}

	var v4Addr, v6Addr net.IP

	for i := range addresses {
		ipNet, ok := addresses[i].(*net.IPNet)
		if !ok || ipNet.IP.IsLoopback() {
			continue
		}

		ip := ipNet.IP

		if v4 := ip.To4(); v4 != nil {
			if !v4.IsLinkLocalUnicast() && !v4.IsLinkLocalMulticast() {
				v4Addr = v4
				break
			}
		} else {
			if !ip.IsLinkLocalUnicast() && !ip.IsLinkLocalMulticast() {
				v6Addr = ip
			}
		}
	}

	if v4Addr == nil && v6Addr == nil {
		return nil, errors.New("no available IP addresses for nodeID generation")
	}

	nodeID := int64(0)
	if v4Addr != nil {
		nodeID = int64(convertAddrToUint16(v4Addr))
	} else {
		nodeID = int64(convertAddrToUint16(v6Addr))
	}

	return snowflake.NewNode(int64(nodeID))
}

func convertAddrToUint16(addr net.IP) uint16 {
	h := fnv.New32a()
	h.Write(addr)
	sum := h.Sum32()

	return uint16((sum >> 16) ^ (sum & 0xFFFF))
}
