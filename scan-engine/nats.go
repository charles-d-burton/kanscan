package main

import (
	"errors"

	nats "github.com/nats-io/nats.go"
	log "github.com/sirupsen/logrus"
)

//TODO:  This needs connection handling logic added. Currently it's pretty rudimentary on failures

//NatsConn struct to satisfy the interface
type NatsConn struct {
	Conn *nats.Conn
	JS   nats.JetStreamContext
	//Sub  stan.Subscription
}

//Connect to the NATS message queue
func (natsConn *NatsConn) Connect(host, port string, errChan chan error) {
	log.Info("Connecting to NATS: ", host, ":", port)
	nh := "nats://" + host + ":" + port
	conn, err := nats.Connect(nh,
		nats.DisconnectErrHandler(func(_ *nats.Conn, err error) {
			errChan <- err
		}),
		nats.DisconnectHandler(func(_ *nats.Conn) {
			errChan <- errors.New("unexpectedly disconnected from nats")
		}),
	)
	if err != nil {
		errChan <- err
		return
	}
	natsConn.Conn = conn

	natsConn.JS, err = conn.JetStream()
	if err != nil {
		errChan <- err
		return
	}
}

//Publish push messages to NATS
func (natsConn *NatsConn) Publish(data []byte) error {
	log.Debugf("Publishing scan: %v to topic: %v", string(data), publish)
	_, err := natsConn.JS.Publish(publish, data)
	if err != nil {
		return err
	}
	return nil
}

/*
 * TODO: There's a bug here where a message needs to be acked back after a scan is finished
 */
//Subscribe subscribe to a topic in NATS TODO: Switch to encoded connections
func (natsConn *NatsConn) Subscribe(errChan chan error) chan []byte {
	log.Infof("Listening on topic: %v", subscription)
	bch := make(chan []byte, 1)

	natsConn.JS.Subscribe(subscription, func(m *nats.Msg) {
		log.Debug("message received from Jetstream")
		bch <- m.Data
		m.Ack() //TOOD: this right here is a bad idea, I can have to messages in flight with a probability of failure
	}, nats.Durable(durableName), nats.ManualAck())
	return bch
}

//Close the connection
func (natsConn *NatsConn) Close() {
	natsConn.Conn.Close()
}
