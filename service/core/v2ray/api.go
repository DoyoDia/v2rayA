package v2ray

import (
	"context"
	"net"
	"strconv"
	"time"

	"github.com/devfeel/mapper"
	"github.com/gin-gonic/gin"
	"github.com/v2fly/v2ray-core/v5/app/observatory"
	pb "github.com/v2fly/v2ray-core/v5/app/observatory/command"
	"github.com/v2rayA/v2rayA/db/configure"
	"github.com/v2rayA/v2rayA/pkg/util/log"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var (
	ApiProducts = []string{
		"observatory",
		"running_state",
	}
	ApiFeed *Feed
)

const (
	ApiFeedBoxSize  = 10
	ApiFeedInterval = 1 * time.Second
)

type OutboundStatus struct {
	Alive bool  `json:"alive"`
	Delay int64 `json:"delay"`
	//LastErrorReason string           `json:"last_error_reason"`
	OutboundTag  string           `json:"outbound_tag"`
	Which        *configure.Which `json:"which"`
	LastSeenTime int64            `json:"last_seen_time"`
	LastTryTime  int64            `json:"last_try_time"`
}

func init() {
	mapper.Register(&observatory.OutboundStatus{})

	ApiFeed = NewSubscriptions(ApiFeedBoxSize)
	for _, product := range ApiProducts {
		ApiFeed.RegisterProduct(product)
	}
}

type ObservatoryResp struct {
	OutboundName string
	Resp         *pb.GetOutboundStatusResponse
}

func getObservatoryResponses(conn *grpc.ClientConn, observatoryTags []string) (r []ObservatoryResp, err error) {
	c := pb.NewObservatoryServiceClient(conn)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if len(observatoryTags) == 0 {
		observatoryTags = append(observatoryTags, "")
	}
	for _, tag := range observatoryTags {
		resp, err := c.GetOutboundStatus(ctx, &pb.GetOutboundStatusRequest{
			Tag: tag,
		})
		if err != nil {
			return nil, err
		}
		r = append(r, ObservatoryResp{OutboundName: tag, Resp: resp})
	}
	return r, nil
}

// ObservatoryProducer monitors outbound status via gRPC API and publishes to ApiFeed.
// This function is only compatible with v2ray-core v5+ API structure.
// For xray-core, this function should not be called as it uses different gRPC services.
func ObservatoryProducer(apiPort int, observatoryTags []string) (closeFunc func()) {
	closed := make(chan struct{})
	go func() {
		const product = "observatory"
		var conn *grpc.ClientConn
	nextLoop:
		for {
			select {
			case <-closed:
				return
			default:
			}
			p := ProcessManager.Process()
			if p == nil {
				time.Sleep(ApiFeedInterval)
				continue
			}
			// Set up a connection to the server.
			if conn == nil {
				ctx, cancel := context.WithTimeout(context.Background(), ApiFeedInterval)
				defer cancel()
				c, err := grpc.DialContext(
					ctx,
					net.JoinHostPort("127.0.0.1", strconv.Itoa(apiPort)),
					grpc.WithInsecure(),
					grpc.WithBlock(),
				)
				if err != nil {
					log.Warn("ObservatoryProducer: did not connect: %v", err)
					continue nextLoop
				}
				defer c.Close()
				conn = c
			}
			resps, err := getObservatoryResponses(conn, observatoryTags)
			if err != nil {
				if status.Code(err) == codes.Unavailable {
					// the connection is reliable, and reconnect
					conn = nil
					continue nextLoop
				}
				log.Warn("ObservatoryProducer: %v", err)
			} else {
				css := configure.GetConnectedServers()
				for _, r := range resps {
					outboundStatus := r.Resp.GetStatus().GetStatus()
					os := make([]OutboundStatus, len(outboundStatus))
					for i := range outboundStatus {
						_ = mapper.AutoMapper(outboundStatus[i], &os[i])
						index := p.tag2WhichIndex[os[i].OutboundTag]
						if index >= css.Len() {
							continue nextLoop
						}
						os[i].Which = css.Get()[index]
						var w []configure.Which
						for _, v := range css.Get() {
							w = append(w, *v)
						}
					}
					msg := gin.H{
						"outboundName":   r.OutboundName,
						"outboundStatus": os,
					}
					ApiFeed.ProductMessage(product, msg)
				}
			}
			time.Sleep(ApiFeedInterval)
		}
	}()
	return func() {
		close(closed)
	}
}

// getXrayObservationResult queries the official xray-core ObservatoryService. Unlike
// the v2ray-compatible service, it takes an empty request and returns the entire
// observation result in a single call, under the xray-native gRPC service name.
// The request/response wire formats of fields 1-6 are identical to v2ray-core v5, so
// the v2ray-generated pb types decode xray's response correctly.
func getXrayObservationResult(conn *grpc.ClientConn) ([]*observatory.OutboundStatus, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	resp := &pb.GetOutboundStatusResponse{}
	err := conn.Invoke(ctx,
		"/xray.core.app.observatory.command.ObservatoryService/GetOutboundStatus",
		&pb.GetOutboundStatusRequest{}, resp)
	if err != nil {
		return nil, err
	}
	return resp.GetStatus().GetStatus(), nil
}

// XrayObservatoryProducer monitors outbound status via the official xray-core
// ObservatoryService and publishes per-group results to ApiFeed. Stock xray has a
// single global observatory, so the one observation result is regrouped using
// groupSelectors (balancer group tag -> member outbound tags) to mirror the per-group
// feed that the v2raya_core MultiObservatory path produces.
func XrayObservatoryProducer(apiPort int, groupSelectors map[string][]string) (closeFunc func()) {
	closed := make(chan struct{})
	// Precompute a membership set per group for fast lookups.
	groupMembers := make(map[string]map[string]struct{}, len(groupSelectors))
	for group, members := range groupSelectors {
		set := make(map[string]struct{}, len(members))
		for _, m := range members {
			set[m] = struct{}{}
		}
		groupMembers[group] = set
	}
	go func() {
		const product = "observatory"
		var conn *grpc.ClientConn
	nextLoop:
		for {
			select {
			case <-closed:
				return
			default:
			}
			p := ProcessManager.Process()
			if p == nil {
				time.Sleep(ApiFeedInterval)
				continue
			}
			if conn == nil {
				ctx, cancel := context.WithTimeout(context.Background(), ApiFeedInterval)
				defer cancel()
				c, err := grpc.DialContext(
					ctx,
					net.JoinHostPort("127.0.0.1", strconv.Itoa(apiPort)),
					grpc.WithInsecure(),
					grpc.WithBlock(),
				)
				if err != nil {
					log.Warn("XrayObservatoryProducer: did not connect: %v", err)
					continue nextLoop
				}
				defer c.Close()
				conn = c
			}
			statuses, err := getXrayObservationResult(conn)
			if err != nil {
				if status.Code(err) == codes.Unavailable {
					// the connection is unreliable, reconnect
					conn = nil
					continue nextLoop
				}
				log.Warn("XrayObservatoryProducer: %v", err)
				time.Sleep(ApiFeedInterval)
				continue
			}
			css := configure.GetConnectedServers()
			for group, members := range groupMembers {
				var os []OutboundStatus
				for _, st := range statuses {
					if _, ok := members[st.GetOutboundTag()]; !ok {
						continue
					}
					var o OutboundStatus
					_ = mapper.AutoMapper(st, &o)
					index := p.tag2WhichIndex[o.OutboundTag]
					if index >= css.Len() {
						continue nextLoop
					}
					o.Which = css.Get()[index]
					os = append(os, o)
				}
				msg := gin.H{
					"outboundName":   group,
					"outboundStatus": os,
				}
				ApiFeed.ProductMessage(product, msg)
			}
			time.Sleep(ApiFeedInterval)
		}
	}()
	return func() {
		close(closed)
	}
}
