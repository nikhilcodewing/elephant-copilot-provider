package handlers

import (
	"bytes"
	"net"
)

func writeStatus(status int, conn net.Conn) (bool, error) {
	var buffer bytes.Buffer
	buffer.Write([]byte{byte(status)})

	lengthBuf := make([]byte, 4)
	buffer.Write(lengthBuf)

	_, err := conn.Write(buffer.Bytes())
	if err != nil {
		return false, err
	}

	return true, nil
}
