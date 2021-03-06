package client

import (
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"net"

	"github.com/simonfuhrer/go-nbd/pkg/nbd"
	"golang.org/x/net/proxy"
)

var DefaultDialer net.Dialer = net.Dialer{}

type Client struct {
	conn       net.Conn
	dialer     net.Dialer
	exportName string
	connected  bool
}

// Create a client based on fixed newstyle negotiation.
func New(network, addr, exportName string, proxydialer ...proxy.Dialer) (*Client, error) {
	var err error
	c := &Client{
		exportName: exportName,
		connected:  false,
		dialer:     net.Dialer{},
	}

	if len(proxydialer) > 0 && proxydialer[0] != nil {
		c.conn, err = proxydialer[0].Dial(network, addr)
	} else {
		c.conn, err = c.dialer.Dial(network, addr)
	}
	if err != nil {
		return nil, err
	}
	if err = c.pingFixedNewStyle(); err != nil {
		return nil, err
	}

	return c, nil
}

func (c *Client) pingFixedNewStyle() error {
	var magic uint64
	if err := binary.Read(c.conn, binary.BigEndian, &magic); err != nil {
		return fmt.Errorf("read of magic errored: %v", err)
	}
	if magic != nbd.NBD_MAGIC {
		return fmt.Errorf("bad magic %v", magic)
	}

	var optsMagic uint64
	if err := binary.Read(c.conn, binary.BigEndian, &optsMagic); err != nil {
		return fmt.Errorf("read of opts magic errored: %v", err)
	}

	if optsMagic != nbd.NBD_OPTS_MAGIC {
		return fmt.Errorf("bad nbd_opts_magic %v", optsMagic)
	}

	var handshakeFlags uint16
	if err := binary.Read(c.conn, binary.BigEndian, &handshakeFlags); err != nil {
		return fmt.Errorf("read of handshake flags errored: %v", err)
	}

	if handshakeFlags != nbd.NBD_FLAG_FIXED_NEWSTYLE {
		return fmt.Errorf("unexpected handshake flags")
	}

	var clientFlags uint32 = nbd.NBD_FLAG_C_FIXED_NEWSTYLE
	if err := binary.Write(c.conn, binary.BigEndian, clientFlags); err != nil {
		return fmt.Errorf("could not send client flags %v", err)
	}

	return c.upgradeToTlsConnection()
}

func (c *Client) upgradeToTlsConnection() error {
	tlsOpt := nbd.NbdClientOpt{
		NbdOptMagic: nbd.NBD_OPTS_MAGIC,
		NbdOptId:    nbd.NBD_OPT_STARTTLS,
		NbdOptLen:   0,
	}

	if err := binary.Write(c.conn, binary.BigEndian, tlsOpt); err != nil {
		return fmt.Errorf("could not send start tls option %v", err)
	}

	var tlsOptReply nbd.NbdOptReply
	if err := binary.Read(c.conn, binary.BigEndian, &tlsOptReply); err != nil {
		return fmt.Errorf("could not receive Tls option reply %v", err)
	}

	if tlsOptReply.NbdOptReplyMagic != nbd.NBD_REP_MAGIC {
		return fmt.Errorf("tls option reply had wrong magic (%x)", tlsOptReply.NbdOptReplyMagic)
	}
	if tlsOptReply.NbdOptId != nbd.NBD_OPT_STARTTLS {
		return fmt.Errorf("tls option reply had wrong id")
	}
	if tlsOptReply.NbdOptReplyType != nbd.NBD_REP_ACK {
		return fmt.Errorf("tls option reply had wrong reply type")
	}
	if tlsOptReply.NbdOptReplyLength != 0 {
		return fmt.Errorf("tls option reply had bogus length")
	}

	config := &tls.Config{
		InsecureSkipVerify: true, // skip verify at the moment
	}
	connTls := tls.Client(c.conn, config)
	if err := connTls.Handshake(); err != nil {
		return fmt.Errorf("tls handshake failed: %v", err)
	}
	c.conn = connTls
	return nil
}

func (c *Client) Close() error {
	if c.connected {
		nbdRequest := nbd.NbdRequest{
			NbdRequestMagic: nbd.NBD_REQUEST_MAGIC,
			NbdCommandType:  nbd.NBD_CMD_DISC,
			NbdOffset:       0,
			NbdLength:       0,
			NbdHandle:       0,
			NbdCommandFlags: 0,
		}
		if err := binary.Write(c.conn, binary.BigEndian, &nbdRequest); err != nil {
			return err
		}
	}
	return c.conn.Close()
}

func (c *Client) Connect() error {
	optExportNameOpt := nbd.NbdClientOpt{
		NbdOptMagic: nbd.NBD_OPTS_MAGIC,
		NbdOptId:    nbd.NBD_OPT_EXPORT_NAME,
		NbdOptLen:   uint32(0 + len(c.exportName)),
	}

	if err := binary.Write(c.conn, binary.BigEndian, optExportNameOpt); err != nil {
		return fmt.Errorf("could not write optExportNameOpt %v ", err)
	}
	if err := binary.Write(c.conn, binary.BigEndian, []byte(c.exportName)); err != nil {
		return fmt.Errorf("could not write export name %v", err)
	}
	var nbdExportDetails nbd.NbdExportDetails

	if err := binary.Read(c.conn, binary.BigEndian, &nbdExportDetails); err != nil {
		return fmt.Errorf("could not read nbdExportDetails reply %v", err)
	}

	// ignore transmission flags
	ignore := make([]byte, 124)
	if err := binary.Read(c.conn, binary.BigEndian, &ignore); err != nil {
		return fmt.Errorf("could not read transmissionFlags %v", err)
	}
	c.connected = true

	return nil
}

func (c *Client) Read(offset uint64, length uint32) ([]byte, error) {
	data := make([]byte, length)
	if !c.connected {
		if err := c.Connect(); err != nil {
			return nil, err
		}
	}
	nbdRequest := nbd.NbdRequest{
		NbdRequestMagic: nbd.NBD_REQUEST_MAGIC,
		NbdCommandType:  nbd.NBD_CMD_READ,
		NbdOffset:       offset,
		NbdLength:       length,
		NbdHandle:       0,
		NbdCommandFlags: 0,
	}
	if err := binary.Write(c.conn, binary.BigEndian, &nbdRequest); err != nil {
		return nil, fmt.Errorf("could not write nbdRequest %v", err)
	}

	var nbdReply nbd.NbdReply
	if err := binary.Read(c.conn, binary.BigEndian, &nbdReply); err != nil {
		return nil, fmt.Errorf("could not read nbdReply %v", err)
	}

	// read data
	if err := binary.Read(c.conn, binary.BigEndian, &data); err != nil {
		return nil, fmt.Errorf("could not read data %v", err)
	}
	return data, nil
}
