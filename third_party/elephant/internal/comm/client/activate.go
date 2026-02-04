// Package client provides simple functions to communicate with the socket.
package client

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"net"
	"strings"

	"github.com/abenz1267/elephant/v2/pkg/pb/pb"
)

func Activate(data string) {
	v := strings.Split(data, ";")

	req := pb.ActivateRequest{
		Provider:   v[0],
		Identifier: v[1],
		Action:     v[2],
		Query:      v[3],
		Arguments:  v[4],
	}

	b, err := json.Marshal(&req)
	if err != nil {
		panic(err)
	}

	conn, err := net.Dial("unix", socket)
	if err != nil {
		panic(err)
	}
	defer conn.Close()

	var buffer bytes.Buffer
	buffer.Write([]byte{1})
	buffer.Write([]byte{1})

	lengthBuf := make([]byte, 4)
	binary.BigEndian.PutUint32(lengthBuf, uint32(len(b)))
	buffer.Write(lengthBuf)
	buffer.Write(b)

	_, err = conn.Write(buffer.Bytes())
	if err != nil {
		panic(err)
	}
}
