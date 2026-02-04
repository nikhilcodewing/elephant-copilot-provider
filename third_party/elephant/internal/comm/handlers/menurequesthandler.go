package handlers

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net"

	"github.com/abenz1267/elephant/v2/pkg/pb/pb"
	"google.golang.org/protobuf/proto"
)

type MenuRequest struct{}

func (a *MenuRequest) Handle(format uint8, cid uint32, conn net.Conn, data []byte) {
	req := &pb.MenuRequest{}

	switch format {
	case 0:
		if err := proto.Unmarshal(data, req); err != nil {
			slog.Error("menurequesthandler", "protobuf", err)

			return
		}
	case 1:
		if err := json.Unmarshal(data, req); err != nil {
			slog.Error("menurequesthandler", "protobuf", err)

			return
		}
	}

	ProviderUpdated <- fmt.Sprintf("%s:%s", "menus", req.Menu)
}
