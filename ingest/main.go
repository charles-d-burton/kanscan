package main

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/charles-d-burton/kanscan/shared"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/viper"
)

//MessageBus Interface for making generic connections to message busses
type MessageBus interface {
	Connect(host, port string) error
	Publish(scan *shared.Scan) error
	Close()
}

var (
	messageBus   MessageBus
	enqueueTopic string
)

func main() {
	log.SetFormatter(&log.JSONFormatter{})
	v := viper.New()
	v.SetEnvPrefix("ingest")
	v.AutomaticEnv()
	if !v.IsSet("port") || !v.IsSet("host") {
		log.Fatal("Must set host and port for message bus")
	}
	if !v.IsSet("enqueue_topic") {
		enqueueTopic = "ingest-enqueue"
	}
	bus, err := connectBus(v)
	if err != nil {
		log.Fatal(err)
	}
	defer bus.Close()
	messageBus = bus
	router := gin.Default()
	router.POST("/scan", handlePost)
	router.Run(":9090")
}

//Connect to a message bus, this is abstracted to an interface so implementations of other busses e.g. Rabbit are easier
//TODO: Clean this mess up
func connectBus(v *viper.Viper) (MessageBus, error) {
	var bus MessageBus
	if v.IsSet("bus_type") {
		busType := v.GetString("bus_type")
		switch busType {
		case "nats":
			var natsConn NatsConn
			err := natsConn.Connect(v.GetString("host"), v.GetString("port"))
			if err != nil {
				return nil, err
			}
			bus = &natsConn
		default:
			var natsConn NatsConn
			err := natsConn.Connect(v.GetString("host"), v.GetString("port"))
			if err != nil {
				return nil, err
			}
			bus = &natsConn
		}
	} else {
		var natsConn NatsConn
		err := natsConn.Connect(v.GetString("host"), v.GetString("port"))
		if err != nil {
			return nil, err
		}
		bus = &natsConn
	}
	return bus, nil
}

func handlePost(c *gin.Context) {
	var scanRequest shared.ScanRequest
	if err := c.ShouldBindJSON(&scanRequest); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if scanRequest.Address == "" && scanRequest.Host == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "host or address undefined"})
		return
	} else if scanRequest.Address != "" && scanRequest.Host != "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "host and address defined"})
		return
	}
	if scanRequest.Address != "" {
		parts := strings.Split(scanRequest.Address, "/") //Check for CIDR notation
		if len(parts) < 2 {
			scanRequest.Address = scanRequest.Address + "/32"
		} else if len(parts) > 2 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "bad address"})
			return
		}
		cidrval, err := strconv.Atoi(parts[1])
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if cidrval < 24 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "subnet out of range"})
			return
		}
		ip, _, err := net.ParseCIDR(scanRequest.Address)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if ip.IsLoopback() {
			c.JSON(http.StatusBadRequest, gin.H{"error": "cannot scan loopback"})
			return
		}
	}
	scanRequest.ID = uuid.New().String()
	if err := enQueueRequest(&scanRequest); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
}

func enQueueRequest(scanreq *shared.ScanRequest) error {
	var scans []shared.Scan
	if scanreq.Host != "" {
		addr, err := net.LookupIP(scanreq.Host)
		if err != nil {
			return errors.New("Unknown Host")
		} else {
			fmt.Println("IP address: ", addr)
			for _, address := range addr {
				var scan shared.Scan
				scan.IP = address.String()
				scan.Request = *scanreq
				scan.Type = shared.Discovery
				scans = append(scans, scan)
			}
		}
	} else {
		addrs, err := Hosts(scanreq.Address)
		if err != nil {
			return err
		}
		var scan shared.Scan
		for _, addr := range addrs {
			scan.IP = addr
			scan.Request = *scanreq
			scan.Type = shared.Discovery
			scans = append(scans, scan)
		}

	}
	for _, scan := range scans {
		log.Println(scan)
		err := messageBus.Publish(&scan)
		if err != nil {
			log.Warn(err)
			return err
		}
	}
	return nil
}

//Hosts split cidr into individual IP addresses
func Hosts(cidr string) ([]string, error) {
	var ip uint32 // ip address

	var ipS uint32 // Start IP address range
	var ipE uint32 // End IP address range
	cidrParts := strings.Split(cidr, "/")

	ip = iPv4ToUint32(cidrParts[0])
	bits, _ := strconv.ParseUint(cidrParts[1], 10, 32)

	if ipS == 0 || ipS > ip {
		ipS = ip
	}

	ip = ip | (0xFFFFFFFF >> bits)

	if ipE < ip {
		ipE = ip
	}
	//ipStart := uInt32ToIPv4(ipS)
	log.Infof("Start of range: %d\n", lastOctet(ipS))
	//ipEnd := uInt32ToIPv4(ipE)
	log.Infof("End of Range: %d\n", lastOctet(ipE))
	ips := make([]string, 0)
	for w := lastOctet(ipS); w <= lastOctet(ipE); w++ {
		ips = append(ips, uInt32ToIPv4(w))
	}
	return ips, nil
}

//Convert IPv4 to uint32
func iPv4ToUint32(iPv4 string) uint32 {

	ipOctets := [4]uint64{}

	for i, v := range strings.SplitN(iPv4, ".", 4) {
		ipOctets[i], _ = strconv.ParseUint(v, 10, 32)
	}

	result := (ipOctets[0] << 24) | (ipOctets[1] << 16) | (ipOctets[2] << 8) | ipOctets[3]

	return uint32(result)
}

//Convert uint32 to IP
func uInt32ToIPv4(iPuInt32 uint32) (iP string) {
	iP = fmt.Sprintf("%d.%d.%d.%d",
		iPuInt32>>24,
		(iPuInt32&0x00FFFFFF)>>16,
		(iPuInt32&0x0000FFFF)>>8,
		iPuInt32&0x000000FF)
	return iP
}

func lastOctet(iPuInt32 uint32) uint32 {
	return iPuInt32 & 0x000000FF
}
