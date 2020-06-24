package biz

import (
	"encoding/json"
	"fmt"
	"net/http"

	nprotoo "github.com/cloudwebrtc/nats-protoo"
	"github.com/pion/ion/pkg/discovery"
	"github.com/pion/ion/pkg/log"
	"github.com/pion/ion/pkg/proto"
	"github.com/pion/ion/pkg/signal"
	"github.com/pion/ion/pkg/util"
	"google.golang.org/grpc"

	pb "github.com/pion/ion/pkg/proto/sfu"
)

// ParseProtoo Unmarshals a protoo payload.
func ParseProtoo(msg json.RawMessage, msgType interface{}) *nprotoo.Error {
	if err := json.Unmarshal(msg, &msgType); err != nil {
		log.Errorf("Biz.Entry parse error %v", err.Error())
		return util.NewNpError(http.StatusBadRequest, fmt.Sprintf("Error parsing request object %v", err.Error()))
	}
	return nil
}

// Entry is the biz entry
func Entry(method string, peer *signal.Peer, msg json.RawMessage, accept signal.RespondFunc, reject signal.RejectFunc) {
	var result interface{}
	topErr := util.NewNpError(http.StatusBadRequest, fmt.Sprintf("Unkown method [%s]", method))

	//TODO DRY this up
	switch method {
	case proto.ClientJoin:
		var msgData proto.JoinMsg
		if topErr = ParseProtoo(msg, &msgData); topErr == nil {
			result, topErr = join(peer, msgData)
		}
	case proto.ClientLeave:
		var msgData proto.LeaveMsg
		if topErr = ParseProtoo(msg, &msgData); topErr == nil {
			result, topErr = leave(peer, msgData)
		}
	case proto.ClientPublish:
		var msgData pb.PublishRequest
		if topErr = ParseProtoo(msg, &msgData); topErr == nil {
			result, topErr = publish(peer, msgData)
		}
	case proto.ClientUnPublish:
		var msgData pb.UnpublishRequest
		if topErr = ParseProtoo(msg, &msgData); topErr == nil {
			result, topErr = unpublish(peer, msgData)
		}
	case proto.ClientSubscribe:
		var msgData pb.SubscribeRequest
		if topErr = ParseProtoo(msg, &msgData); topErr == nil {
			result, topErr = subscribe(peer, msgData)
		}
	case proto.ClientUnSubscribe:
		var msgData pb.UnsubscribeRequest
		if topErr = ParseProtoo(msg, &msgData); topErr == nil {
			result, topErr = unsubscribe(peer, msgData)
		}
	case proto.ClientBroadcast:
		var msgData proto.BroadcastMsg
		if topErr = ParseProtoo(msg, &msgData); topErr == nil {
			result, topErr = broadcast(peer, msgData)
		}
	case proto.ClientTrickleICE:
		var msgData proto.TrickleMsg
		if topErr = ParseProtoo(msg, &msgData); topErr == nil {
			result, topErr = trickle(peer, msgData)
		}
	}

	if topErr != nil {
		reject(topErr.Code, topErr.Reason)
	} else {
		accept(result)
	}
}

func getRPCForIslb() (*nprotoo.Requestor, bool) {
	for _, item := range services {
		if item.Info["service"] == "islb" {
			id := item.Info["id"]
			rpc, found := rpcs[id]
			if !found {
				rpcID := discovery.GetRPCChannel(item)
				log.Infof("Create rpc [%s] for islb", rpcID)
				rpc = protoo.NewRequestor(rpcID)
				rpcs[id] = rpc
			}
			return rpc, true
		}
	}
	log.Warnf("No islb node was found.")
	return nil, false
}

func handleSFUBroadCast(msg nprotoo.Notification, subj string) {
	go func(msg nprotoo.Notification) {
		var data proto.MediaInfo
		if err := json.Unmarshal(msg.Data, &data); err != nil {
			log.Errorf("handleSFUBroadCast Unmarshall error %v", err)
			return
		}

		log.Infof("handleSFUBroadCast: method=%s, data=%v", msg.Method, data)

		switch msg.Method {
		case proto.SFUTrickleICE:
			signal.NotifyAllWithoutID(data.RID, data.UID, proto.ClientOnStreamAdd, data)
		case proto.SFUStreamRemove:
			islb, found := getRPCForIslb()
			if found {
				islb.AsyncRequest(proto.IslbOnStreamRemove, data)
			}
		}
	}(msg)
}

func getRPCForSFU(mid string) (string, pb.SFUClient, *nprotoo.Error) {
	islb, found := getRPCForIslb()
	if !found {
		return "", nil, util.NewNpError(500, "Not found any node for islb.")
	}
	result, nerr := islb.SyncRequest(proto.IslbFindService, util.Map("service", "sfu", "mid", mid))
	if nerr != nil {
		return "", nil, nerr
	}

	var answer proto.GetSFURPCParams
	if err := json.Unmarshal(result, &answer); err != nil {
		return "", nil, &nprotoo.Error{Code: 123, Reason: "Unmarshal error getRPCForSFU"}
	}

	log.Infof("SFU result => %s", result)
	addr := answer.GRPC
	sfu, found := sfus[addr]
	if !found {
		conn, err := grpc.Dial(answer.GRPC, grpc.WithInsecure(), grpc.WithBlock())
		if err != nil {
			log.Panicf("did not connect: %v", err)
		}
		sfu = pb.NewSFUClient(conn)
		sfus[addr] = sfu
	}
	return answer.ID, sfu, nil
}
